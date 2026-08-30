package rootcause_test

import (
	"encoding/json"
	"testing"
	"time"

	"logauditorgo/internal/model"
	"logauditorgo/internal/rootcause"
)

// 构造一条参与 RCA 的日志
func rcaLog(id uint, hostname, module, brief string, ts time.Time) *model.NormalizedLog {
	return &model.NormalizedLog{
		ID:        id,
		Hostname:  hostname,
		Module:    module,
		Brief:     brief,
		Severity:  3,
		Timestamp: ts,
	}
}

// correlatedCount 解析 RCAEvent.CorrelatedLogIDs（该字段是 JSON 数组字符串，不是切片）
func correlatedCount(t *testing.T, raw string) int {
	t.Helper()
	if raw == "" {
		return 0
	}
	var ids []uint
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		t.Fatalf("unmarshal CorrelatedLogIDs %q failed: %v", raw, err)
	}
	return len(ids)
}

// TestRCATempIDNoCollision 验证给未分配 ID 的日志补临时号时不会与真实 ID 碰撞 (RCA-05)。
//
// 原实现用 `uint(idx + 1)` 补号：当 logs[0].ID == 2 且 logs[1].ID == 0 时，
// 后者补出的临时号恰好也是 2。而 visited / visitedInDAG / depthMap 全部以 ID 为键，
// 碰撞会让后一条日志被当成"已访问"而静默丢弃，RCA 链路被截断。
func TestRCATempIDNoCollision(t *testing.T) {
	base := time.Date(2026, 4, 15, 10, 0, 0, 0, time.Local)

	logs := []*model.NormalizedLog{
		rcaLog(2, "SW-01", "IFNET", "IF_DOWN", base),                        // 真实 ID = 2
		rcaLog(0, "SW-01", "BFD", "BFD_SESS_DOWN", base.Add(1*time.Second)), // 旧实现补号 = idx+1 = 2 → 与上面碰撞
		rcaLog(0, "SW-01", "BGP", "PEER_DOWN", base.Add(2*time.Second)),     // 旧实现补号 = 3
	}

	eng := rootcause.NewEngine()
	events := eng.Analyze(logs, 300)
	if len(events) == 0 {
		t.Fatalf("expected at least one RCA event")
	}

	total := 0
	for _, ev := range events {
		total += correlatedCount(t, ev.CorrelatedLogIDs)
	}
	// BFD 与 BGP 两条下游事件都应被关联到 IF_DOWN；若发生 ID 碰撞，BGP 会被静默吞掉
	if total < 2 {
		t.Errorf("RCA-05: expected 2 correlated logs (BFD + BGP), got %d — downstream log dropped by ID collision", total)
	}

	if events[0].RootBrief != "IF_DOWN" {
		t.Errorf("RCA-05: expected root brief 'IF_DOWN', got %q", events[0].RootBrief)
	}
}

// TestRCAStableOrderForSameTimestamp 验证同秒日志按输入顺序稳定排序，根因选取可预期 (RCA-07)。
//
// 注意：sort.Slice 对固定输入是**确定的**，只是不保证稳定性（不保持相等元素的原有相对顺序）。
// 因此"重复运行 N 次结果一致"并不能复现该问题——本用例在此处定位为**防回归守卫**：
// 它锁定"同秒日志按输入顺序（ID 升序）取根、且分析结果可重复"这一不变量，
// 一旦有人把 SliceStable 改回 Slice 或引入 map 遍历顺序依赖，这里会立即报警。
func TestRCAStableOrderForSameTimestamp(t *testing.T) {
	base := time.Date(2026, 4, 15, 10, 0, 0, 0, time.Local)

	// 30 条全部同秒（超过 12 条以触发 pdqsort 的非稳定路径；小切片走插入排序是稳定的，测不出问题）。
	// 输入顺序中第一条是 IF_DOWN（ID=100），稳定排序下它应当被选为根因。
	logList := []*model.NormalizedLog{
		rcaLog(100, "SW-01", "IFNET", "IF_DOWN", base),
		rcaLog(101, "SW-01", "BFD", "BFD_SESS_DOWN", base),
		rcaLog(102, "SW-01", "BGP", "PEER_DOWN", base),
	}
	tail := []struct{ mod, brf string }{
		{"RM", "ROUTE_DELETE"},
		{"OSPF", "NBR_CHG"},
		{"IFNET", "LINK_DOWN"},
		{"IFNET", "IF_DOWN"},
		{"BFD", "BFD_SESS_DOWN"},
		{"BGP", "PEER_DOWN"},
	}
	for i := 0; i < 27; i++ {
		b := tail[i%len(tail)]
		logList = append(logList, rcaLog(uint(103+i), "SW-01", b.mod, b.brf, base))
	}

	eng := rootcause.NewEngine()
	events := eng.Analyze(logList, 300)
	if len(events) == 0 {
		t.Fatalf("expected at least one RCA event")
	}

	// 稳定排序下，输入中最靠前的链首日志（ID=100 的 IF_DOWN）应当成为首个根因
	if events[0].RootLogID != 100 {
		t.Errorf("RCA-07: expected root log ID 100 (first in input order among equal timestamps), got %d (%s/%s)",
			events[0].RootLogID, events[0].RootModule, events[0].RootBrief)
	}

	// 同一批输入重复分析，结果必须完全一致
	signature := func() string {
		out := ""
		for _, ev := range eng.Analyze(logList, 300) {
			out += ev.RootModule + "/" + ev.RootBrief + "#" + ev.CorrelatedLogIDs + ";"
		}
		return out
	}
	first := signature()
	for i := 0; i < 10; i++ {
		if got := signature(); got != first {
			t.Fatalf("RCA-07: analysis result is not reproducible.\nfirst=%s\ngot  =%s", first, got)
		}
	}
}
