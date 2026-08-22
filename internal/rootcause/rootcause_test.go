package rootcause_test

import (
	"testing"
	"time"

	"logauditorgo/internal/model"
	"logauditorgo/internal/rootcause"
)

func TestRootCauseEngine(t *testing.T) {
	now := time.Now()

	// 模拟一次接口故障引发连环反应的日志序列
	logs := []*model.NormalizedLog{
		{
			ID:        101,
			Module:    "IFNET",
			Severity:  4,
			Brief:     "IF_DOWN",
			Timestamp: now,
		},
		{
			ID:        102,
			Module:    "BFD",
			Severity:  2,
			Brief:     "BFD_SESS_DOWN",
			Timestamp: now.Add(100 * time.Millisecond),
		},
		{
			ID:        103,
			Module:    "BGP",
			Severity:  2,
			Brief:     "PEER_BACKWARD",
			Timestamp: now.Add(350 * time.Millisecond),
		},
		{
			ID:        104,
			Module:    "RM",
			Severity:  4,
			Brief:     "ROUTE_DELETE",
			Timestamp: now.Add(500 * time.Millisecond),
		},
	}

	engine := rootcause.NewEngine(nil)
	events := engine.Analyze(logs, 300)

	if len(events) == 0 {
		t.Fatalf("expected at least 1 RCA event, got 0")
	}

	rca := events[0]
	if rca.RootLogID != 101 {
		t.Errorf("expected RootLogID 101, got %d", rca.RootLogID)
	}
	if rca.RootModule != "IFNET" || rca.RootBrief != "IF_DOWN" {
		t.Errorf("expected Root IFNET/IF_DOWN, got %s/%s", rca.RootModule, rca.RootBrief)
	}
	if rca.ImpactLevel != "CRITICAL" {
		t.Errorf("expected CRITICAL impact level, got %s", rca.ImpactLevel)
	}
	if rca.Confidence < 0.9 {
		t.Errorf("expected confidence >= 0.9, got %f", rca.Confidence)
	}

	t.Logf("RCA Success: Root=%s/%s, CorrelatedIDs=%s, Summary=%s",
		rca.RootModule, rca.RootBrief, rca.CorrelatedLogIDs, rca.RootCauseSummary)
}
func TestRootCauseEngineAllRulesAndIsolation(t *testing.T) {
	now := time.Now()
	engine := rootcause.NewEngine(nil)

	// 1. 测试规则 2: 光模块 CRC 异常 -> 接口 Down
	logsOpt := []*model.NormalizedLog{
		{ID: 201, Module: "TRANSCEIVER", Severity: 3, Brief: "TRANSCEIVER_FAIL", Timestamp: now},
		{ID: 202, Module: "ETHBASE", Severity: 4, Brief: "CRC_ERR", Timestamp: now.Add(2 * time.Second)},
		{ID: 203, Module: "IFNET", Severity: 4, Brief: "IF_DOWN", Timestamp: now.Add(5 * time.Second)},
	}
	eventsOpt := engine.Analyze(logsOpt, 300)
	if len(eventsOpt) == 0 || eventsOpt[0].RootModule != "TRANSCEIVER" {
		t.Errorf("Rule 2 (Optical CRC) failed: %+v", eventsOpt)
	}

	// 2. 测试规则 3: RADIUS 不可达 -> 用户鉴权失败 -> 下线
	logsRadius := []*model.NormalizedLog{
		{ID: 301, Module: "AAA", Severity: 2, Brief: "hwRadiusAuthServerDown", Timestamp: now},
		{ID: 302, Module: "AAA", Severity: 4, Brief: "USER_AUTH_FAIL", Timestamp: now.Add(1 * time.Second)},
		{ID: 303, Module: "PORTAL", Severity: 4, Brief: "USER_OFFLINE", Timestamp: now.Add(3 * time.Second)},
	}
	eventsRadius := engine.Analyze(logsRadius, 300)
	if len(eventsRadius) == 0 || eventsRadius[0].RootBrief != "hwRadiusAuthServerDown" {
		t.Errorf("Rule 3 (RADIUS Auth) failed: %+v", eventsRadius)
	}

	// 3. 测试规则 4: M-LAG Peerlink 中断 -> DAD -> 端口 ErrorDown
	logsMlag := []*model.NormalizedLog{
		{ID: 401, Module: "MLAG", Severity: 2, Brief: "PEERLINK_DOWN", Timestamp: now},
		{ID: 402, Module: "MLAG", Severity: 2, Brief: "DUAL_ACTIVE", Timestamp: now.Add(1 * time.Second)},
		{ID: 403, Module: "IFNET", Severity: 4, Brief: "PORT_ERRORDOWN", Timestamp: now.Add(2 * time.Second)},
	}
	eventsMlag := engine.Analyze(logsMlag, 300)
	if len(eventsMlag) == 0 || eventsMlag[0].RootBrief != "PEERLINK_DOWN" {
		t.Errorf("Rule 4 (M-LAG DAD) failed: %+v", eventsMlag)
	}

	// 4. 测试规则 5: CPU 过载 -> 心跳超时
	logsCpu := []*model.NormalizedLog{
		{ID: 501, Module: "DEVM", Severity: 2, Brief: "CPU_HIGH", Timestamp: now},
		{ID: 502, Module: "BGP", Severity: 2, Brief: "HOLD_TIME_EXPIRED", Timestamp: now.Add(10 * time.Second)},
	}
	eventsCpu := engine.Analyze(logsCpu, 300)
	if len(eventsCpu) == 0 || eventsCpu[0].RootBrief != "CPU_HIGH" {
		t.Errorf("Rule 5 (Resource Overload) failed: %+v", eventsCpu)
	}

	// 5. 测试多主机隔离 (Host A 的 IF_DOWN 不应关联 Host B 的 BFD_SESS_DOWN)
	logsMultiHost := []*model.NormalizedLog{
		{ID: 601, Hostname: "SW-01", Module: "IFNET", Severity: 4, Brief: "IF_DOWN", Timestamp: now},
		{ID: 602, Hostname: "SW-02", Module: "BFD", Severity: 2, Brief: "BFD_SESS_DOWN", Timestamp: now.Add(1 * time.Second)},
	}
	eventsIso := engine.Analyze(logsMultiHost, 300)
	if len(eventsIso) != 0 {
		t.Errorf("expected 0 correlated events across different hosts, got %d", len(eventsIso))
	}

	// 6. 测试滑动时间窗口超时 (超过 300s 不应关联)
	logsTimeout := []*model.NormalizedLog{
		{ID: 701, Hostname: "SW-01", Module: "IFNET", Severity: 4, Brief: "IF_DOWN", Timestamp: now},
		{ID: 702, Hostname: "SW-01", Module: "BFD", Severity: 2, Brief: "BFD_SESS_DOWN", Timestamp: now.Add(350 * time.Second)},
	}
	eventsTimeout := engine.Analyze(logsTimeout, 300)
	if len(eventsTimeout) != 0 {
		t.Errorf("expected 0 correlated events beyond 300s window, got %d", len(eventsTimeout))
	}
}

func TestRootCauseEngineDefensive(t *testing.T) {
	var engine *rootcause.Engine

	// Should not panic
	events := engine.Analyze([]*model.NormalizedLog{{}}, 300)
	if len(events) != 0 {
		t.Errorf("expected 0 events for nil engine")
	}

	engine2 := rootcause.NewEngine(nil)

	// Should filter nil logs
	logs := []*model.NormalizedLog{nil, nil}
	events = engine2.Analyze(logs, 300)
	if len(events) != 0 {
		t.Errorf("expected 0 events for all-nil logs")
	}

	// Test missing fields that could cause panic if not careful
	logs2 := []*model.NormalizedLog{
		nil,
		{
			ID:     1,
			Module: "BGP",
		},
	}
	events = engine2.Analyze(logs2, 300)
	if len(events) != 0 {
		t.Errorf("expected 0 events for invalid logs, got %d", len(events))
	}
}
