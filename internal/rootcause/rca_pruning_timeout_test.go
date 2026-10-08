package rootcause_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"logauditorgo/internal/model"
	"logauditorgo/internal/rootcause"
)

// TestAnalyzeWithContext_PreCanceled 验证传入已取消的 Context 时立即截断返回
func TestAnalyzeWithContext_PreCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	base := time.Date(2026, 4, 15, 10, 0, 0, 0, time.Local)
	logs := []*model.NormalizedLog{
		rcaLog(1, "SW-01", "IFNET", "IF_DOWN", base),
		rcaLog(2, "SW-01", "BFD", "BFD_SESS_DOWN", base.Add(1*time.Second)),
	}

	eng := rootcause.NewEngine()
	res := eng.AnalyzeWithContext(ctx, logs, 300)

	if !res.Truncated {
		t.Fatalf("expected res.Truncated to be true for pre-canceled context")
	}
	if res.Err == nil {
		t.Fatalf("expected non-nil res.Err")
	}
	if len(res.Events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(res.Events))
	}
}

// TestAnalyzeWithContext_Timeout 验证在分析过程中超时截断并保存部分已分析结果
func TestAnalyzeWithContext_Timeout(t *testing.T) {
	base := time.Date(2026, 4, 15, 10, 0, 0, 0, time.Local)
	var logs []*model.NormalizedLog

	// 构造多批次故障链，每隔一段时间产生一次独立故障
	for i := 0; i < 200; i++ {
		tRoot := base.Add(time.Duration(i*10) * time.Second)
		logs = append(logs,
			rcaLog(uint(i*3+1), "SW-01", "IFNET", "IF_DOWN", tRoot),
			rcaLog(uint(i*3+2), "SW-01", "BFD", "BFD_SESS_DOWN", tRoot.Add(1*time.Second)),
			rcaLog(uint(i*3+3), "SW-01", "BGP", "PEER_DOWN", tRoot.Add(2*time.Second)),
		)
	}

	eng := rootcause.NewEngine()

	// 使用非常短的超时时间
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(1 * time.Millisecond) // 确保超时已触发

	res := eng.AnalyzeWithContext(ctx, logs, 300)
	if !res.Truncated {
		t.Fatalf("expected res.Truncated to be true")
	}
	if res.Err != context.DeadlineExceeded && res.Err != context.Canceled {
		t.Fatalf("expected context error, got %v", res.Err)
	}
}

// TestAnalyzeWithContext_ScanWatermarkCorrectness 验证扫描水位机制下，跨越重叠窗口的故障链不会被漏报
func TestAnalyzeWithContext_ScanWatermarkCorrectness(t *testing.T) {
	base := time.Date(2026, 4, 15, 10, 0, 0, 0, time.Local)

	// Cluster 1 (0s ~ 300s):
	// IFNET/IF_DOWN 发生在 280s
	//
	// Cluster 2 (240s ~ 540s，带 60s 重叠):
	// 下游衍生 BFD_SESS_DOWN 发生在 310s (落入第二个簇)
	//
	// 在旧单簇无重叠时可能漏报；在扫描水位动态剪枝下，
	// 第一次扫描IFNET日志时可能尚未看到310s的下游（若仅在簇1范围），
	// 但在重叠簇2中，由于 hi > upTo，会重新扫描并成功串联。
	logs := []*model.NormalizedLog{
		rcaLog(1, "SW-01", "IFNET", "IF_DOWN", base.Add(280*time.Second)),
		rcaLog(2, "SW-01", "BFD", "BFD_SESS_DOWN", base.Add(310*time.Second)),
	}

	eng := rootcause.NewEngine()
	res := eng.AnalyzeWithContext(context.Background(), logs, 300)

	if res.Truncated {
		t.Fatalf("analysis should not be truncated")
	}
	if len(res.Events) != 1 {
		t.Fatalf("expected exactly 1 RCA event, got %d", len(res.Events))
	}
	if res.Events[0].RootLogID != 1 {
		t.Fatalf("expected root log ID 1, got %d", res.Events[0].RootLogID)
	}
	cnt := correlatedCount(t, res.Events[0].CorrelatedLogIDs)
	if cnt != 1 {
		t.Fatalf("expected 1 correlated log, got %d", cnt)
	}
}

// TestAnalyzeWithContext_EventCapReached 验证当分析产生的根因事件达到 2000 个上限时，
// 引擎正确标记 Truncated=true，并且 AnalyzedUntil 保持为实际处理到的位置而非全量末尾
func TestAnalyzeWithContext_EventCapReached(t *testing.T) {
	base := time.Date(2026, 4, 15, 10, 0, 0, 0, time.Local)
	var logs []*model.NormalizedLog

	// 构造 2050 个独立的根因+衍生事件对，不同主机名避免跨对聚合
	for i := 0; i < 2050; i++ {
		tRoot := base.Add(time.Duration(i*10) * time.Second)
		host := fmt.Sprintf("SW-%d", i)
		logs = append(logs,
			rcaLog(uint(i*2+1), host, "IFNET", "IF_DOWN", tRoot),
			rcaLog(uint(i*2+2), host, "BFD", "BFD_SESS_DOWN", tRoot.Add(1*time.Second)),
		)
	}

	eng := rootcause.NewEngine()
	res := eng.AnalyzeWithContext(context.Background(), logs, 300)

	if !res.Truncated {
		t.Fatalf("expected res.Truncated to be true when reaching event cap")
	}
	if len(res.Events) != 2000 {
		t.Fatalf("expected exactly 2000 events (event cap), got %d", len(res.Events))
	}
	lastLogTime := logs[len(logs)-1].Timestamp
	if res.AnalyzedUntil.Equal(lastLogTime) {
		t.Fatalf("res.AnalyzedUntil should not be the timestamp of the last log (%v), but the cap cutoff point (%v)", lastLogTime, res.AnalyzedUntil)
	}
	if res.AnalyzedUntil.IsZero() {
		t.Fatalf("res.AnalyzedUntil should not be zero")
	}
}

// TestAnalyzeWithContext_PropagateCancellation 验证在 Context 取消时能安全退出并标记 Truncated
func TestAnalyzeWithContext_PropagateCancellation(t *testing.T) {
	base := time.Date(2026, 4, 15, 10, 0, 0, 0, time.Local)
	var logs []*model.NormalizedLog

	logs = append(logs, rcaLog(1, "SW-01", "IFNET", "IF_DOWN", base))
	for i := 2; i <= 300; i++ {
		logs = append(logs, rcaLog(uint(i), "SW-01", "BFD", "BFD_SESS_DOWN", base.Add(time.Duration(i)*time.Millisecond)))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel or cancel during execution

	eng := rootcause.NewEngine()
	res := eng.AnalyzeWithContext(ctx, logs, 300)

	if !res.Truncated {
		t.Fatalf("expected res.Truncated to be true for canceled context")
	}
	if res.Err == nil {
		t.Fatalf("expected non-nil res.Err")
	}
}

