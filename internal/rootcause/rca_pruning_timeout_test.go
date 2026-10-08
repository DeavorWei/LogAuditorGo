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

	// 构造生成 3 个重叠簇的完整级联故障链：
	// Cluster 1 (0s ~ 300s):
	//   Log 100: 0s  (时间锚点)
	//   Log 1:   280s IFNET/IF_DOWN (位于簇 1 末尾，此时下游在 300s 外尚未出现，propagate 返回 false，记录水位 2)
	// Cluster 2 (240s ~ 540s，带 60s 重叠):
	//   Log 1:   280s IFNET/IF_DOWN (hi=6 > upTo=2，水位机制允许再次扫描)
	//   Log 2:   310s BFD_SESS_DOWN (下游 1)
	//   Log 3:   320s OSPF NBR_CHG  (下游 2)
	//   Log 4:   330s BGP PEER_DOWN (下游 3)
	//   Log 5:   340s RM ROUTE_DEL  (下游 4)
	// Cluster 3 (480s ~ 780s):
	//   Log 101: 650s (时间锚点)
	logs := []*model.NormalizedLog{
		rcaLog(100, "SW-01", "DEVM", "DEV_NORMAL", base),
		rcaLog(1, "SW-01", "IFNET", "IF_DOWN", base.Add(280*time.Second)),
		rcaLog(2, "SW-01", "BFD", "BFD_SESS_DOWN", base.Add(310*time.Second)),
		rcaLog(3, "SW-01", "OSPF", "NBR_CHG", base.Add(320*time.Second)),
		rcaLog(4, "SW-01", "BGP", "PEER_DOWN", base.Add(330*time.Second)),
		rcaLog(5, "SW-01", "RM", "ROUTE_DELETE", base.Add(340*time.Second)),
		rcaLog(101, "SW-01", "DEVM", "DEV_NORMAL", base.Add(650*time.Second)),
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
	if cnt != 4 {
		t.Fatalf("expected 4 correlated logs across windows, got %d", cnt)
	}
}

type stepCancelContext struct {
	doneCh    chan struct{}
	callCount int
	threshold int
	err       error
}

func (c *stepCancelContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *stepCancelContext) Done() <-chan struct{} {
	c.callCount++
	if c.callCount >= c.threshold {
		select {
		case <-c.doneCh:
		default:
			close(c.doneCh)
			c.err = context.DeadlineExceeded
		}
	}
	return c.doneCh
}
func (c *stepCancelContext) Err() error { return c.err }
func (c *stepCancelContext) Value(key any) any { return nil }

// TestAnalyzeWithContext_MidwayTimeout 验证在分析遍历中途超时熔断时，
// 引擎返回部分已计算事件，Truncated=true，且 AnalyzedUntil 精确落在熔断处的日志时间
func TestAnalyzeWithContext_MidwayTimeout(t *testing.T) {
	base := time.Date(2026, 4, 15, 10, 0, 0, 0, time.Local)
	var logs []*model.NormalizedLog

	// 构造 50 批次独立事件，每批次相隔 5 秒
	for i := 0; i < 50; i++ {
		tRoot := base.Add(time.Duration(i*5) * time.Second)
		host := fmt.Sprintf("SW-%03d", i)
		logs = append(logs,
			rcaLog(uint(i*2+1), host, "IFNET", "IF_DOWN", tRoot),
			rcaLog(uint(i*2+2), host, "BFD", "BFD_SESS_DOWN", tRoot.Add(1*time.Second)),
		)
	}

	eng := rootcause.NewEngine()

	// 注入中途超时 Context：在检查点调用达到第 15 次时精确触发超时
	ctx := &stepCancelContext{
		doneCh:    make(chan struct{}),
		threshold: 15,
	}

	res := eng.AnalyzeWithContext(ctx, logs, 300)

	if !res.Truncated {
		t.Fatalf("expected res.Truncated to be true for midway timeout")
	}
	if res.Err != context.DeadlineExceeded {
		t.Fatalf("expected DeadlineExceeded, got %v", res.Err)
	}
	if len(res.Events) == 0 {
		t.Fatalf("expected at least 1 partial event generated before timeout, got 0")
	}
	if len(res.Events) >= 50 {
		t.Fatalf("expected strictly fewer than 50 events due to midway timeout, got %d", len(res.Events))
	}
	if res.AnalyzedUntil.IsZero() {
		t.Fatalf("expected non-zero AnalyzedUntil")
	}
	firstLogTime := logs[0].Timestamp
	lastLogTime := logs[len(logs)-1].Timestamp
	if !res.AnalyzedUntil.After(firstLogTime) {
		t.Fatalf("expected AnalyzedUntil (%v) to be after first log (%v)", res.AnalyzedUntil, firstLogTime)
	}
	if !res.AnalyzedUntil.Before(lastLogTime) {
		t.Fatalf("expected AnalyzedUntil (%v) to be strictly before last log (%v)", res.AnalyzedUntil, lastLogTime)
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

