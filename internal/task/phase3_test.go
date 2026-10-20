package task_test

import (
	"testing"

	"logauditorgo/internal/model"
	"logauditorgo/internal/task"
)

const phase3Logs = "Apr 15 2026 10:00:01 HOST-A %%01IFNET/4/IF_DOWN(l)[1]: Interface 100GE1/0/1 state turned to DOWN. (InterfaceName=100GE1/0/1)\n" +
	"Apr 15 2026 10:00:02 HOST-A %%01IFNET/4/IF_DOWN(l)[2]: Interface 100GE1/0/2 state turned to DOWN. (InterfaceName=100GE1/0/2)\n" +
	"Apr 15 2026 10:00:03 HOST-A %%01BGP/2/PEER_BACKWARD(l)[3]: The BGP peer went down. (PeerAddress=192.168.1.2)\n" +
	"# Huawei log export metadata\n" +
	"Apr 15 2026 10:06:04 HOST-A %%01OSPF/4/NBR_CHG(l)[4]: OSPF neighbor changed. (Neighbor=192.168.1.3)\n"

// TestPhase3Insights 验证注释默认隐藏、时间直方图、聚合视图与 RCA 默认关闭冒烟。
func TestPhase3Insights(t *testing.T) {
	svc, cleanup := newPhase1Service(t)
	defer cleanup()

	info, err := svc.CreateAndRunTask("阶段三-洞察", "", phase3Logs)
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	if info.LogCount != 5 {
		t.Fatalf("stored log count = %d, want 5 (comment included in storage)", info.LogCount)
	}

	// 1. 注释行计数与默认隐藏（查询侧）
	commentCount, err := svc.CountCommentLogs(info.TaskID)
	if err != nil || commentCount != 1 {
		t.Fatalf("comment count = %d, err=%v, want 1", commentCount, err)
	}
	hidden, totalHidden, err := svc.QueryTaskLogsUnified(info.TaskID, model.LogQueryRequestBody{
		Page: 1, PageSize: 20, ExcludeComment: true,
	})
	if err != nil || totalHidden != 4 || len(hidden) != 4 {
		t.Fatalf("exclude comment query = total %d len %d err=%v, want 4", totalHidden, len(hidden), err)
	}
	all, totalAll, err := svc.QueryTaskLogsUnified(info.TaskID, model.LogQueryRequestBody{Page: 1, PageSize: 20})
	if err != nil || totalAll != 5 || len(all) != 5 {
		t.Fatalf("full query = total %d len %d err=%v, want 5", totalAll, len(all), err)
	}

	// 2. 时间直方图
	histogram, err := svc.GetLogHistogram(info.TaskID, model.LogQueryRequestBody{ExcludeComment: true}, 12)
	if err != nil {
		t.Fatalf("histogram failed: %v", err)
	}
	if histogram.Total != 4 || len(histogram.Buckets) != 12 {
		t.Fatalf("histogram total=%d buckets=%d, want 4/12", histogram.Total, len(histogram.Buckets))
	}
	var bucketTotal int64
	for _, bucket := range histogram.Buckets {
		bucketTotal += bucket.Total
	}
	if bucketTotal != 4 {
		t.Fatalf("histogram bucket sum = %d, want 4", bucketTotal)
	}
	if histogram.Buckets[0].Start.IsZero() || histogram.BucketSeconds <= 0 {
		t.Fatalf("histogram time metadata invalid: %+v", histogram)
	}

	// 3. 聚合视图：两条 IF_DOWN 折叠为一组
	groups, err := svc.GetAggregatedLogs(info.TaskID, model.LogQueryRequestBody{ExcludeComment: true}, 300, 50)
	if err != nil {
		t.Fatalf("aggregate failed: %v", err)
	}
	if len(groups) != 3 {
		t.Fatalf("expected 3 aggregated groups, got %d: %+v", len(groups), groups)
	}
	foundIFDown := false
	for _, group := range groups {
		if group.Brief == "IF_DOWN" {
			foundIFDown = true
			if group.Count != 2 {
				t.Fatalf("IF_DOWN group count = %d, want 2", group.Count)
			}
			if group.SampleLogID == 0 || group.Hostname != "HOST-A" {
				t.Fatalf("IF_DOWN group metadata invalid: %+v", group)
			}
		}
	}
	if !foundIFDown {
		t.Fatalf("IF_DOWN group missing: %+v", groups)
	}

	// 4. RCA 默认关闭：导入后不得自动派发
	finalInfo, err := svc.GetTaskByID(info.TaskID)
	if err != nil {
		t.Fatalf("get task failed: %v", err)
	}
	if finalInfo.RCAStatus != model.RCAStatusPending || finalInfo.RcaCount != 0 {
		t.Fatalf("RCA must stay PENDING/0 when lab feature is off, got %s/%d", finalInfo.RCAStatus, finalInfo.RcaCount)
	}
}

// 保持 task 包导入用于潜在的类型引用（当前用例只使用 service 方法）。
var _ = task.TaskListQuery{}
