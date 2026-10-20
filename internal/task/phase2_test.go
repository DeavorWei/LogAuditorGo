package task_test

import (
	"testing"

	"logauditorgo/internal/task"
)

// TestTaskDigestAndGlobalHotspots 验证跨任务检索摘要、未匹配热点聚合与删除清理。
func TestTaskDigestAndGlobalHotspots(t *testing.T) {
	svc, cleanup := newPhase1Service(t)
	defer cleanup()

	info, err := svc.CreateAndRunTask("摘要索引任务", "", phase1MultiHostLogs)
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	if err := svc.RefreshTaskDigest(info.TaskID); err != nil {
		t.Fatalf("refresh digest failed: %v", err)
	}

	// 1. 按主机名找回任务（跨任务库）
	items, total, err := svc.QueryTaskList(task.TaskListQuery{Keyword: "USG6000F"})
	if err != nil {
		t.Fatalf("query by hostname failed: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].TaskID != info.TaskID {
		t.Fatalf("hostname search failed: total=%d items=%+v", total, items)
	}

	// 2. 按模块找回任务
	_, total, err = svc.QueryTaskList(task.TaskListQuery{Keyword: "IFNET"})
	if err != nil || total != 1 {
		t.Fatalf("module search failed: total=%d err=%v", total, err)
	}

	// 3. 未匹配热点聚合
	hotspots, digestCount, err := svc.GetGlobalUnmatchedHotspots(10)
	if err != nil {
		t.Fatalf("global hotspots failed: %v", err)
	}
	if digestCount < 1 {
		t.Fatalf("expected at least 1 digest, got %d", digestCount)
	}
	if len(hotspots) < 3 {
		t.Fatalf("expected >= 3 hotspots, got %+v", hotspots)
	}
	found := false
	for _, item := range hotspots {
		if item.Module == "BGP" && item.Brief == "PEER_BACKWARD" {
			found = true
			if item.TaskCount != 1 || len(item.TaskIDs) != 1 || item.TaskIDs[0] != info.TaskID {
				t.Fatalf("hotspot task linkage wrong: %+v", item)
			}
			if item.Count < 1 {
				t.Fatalf("hotspot count must be positive: %+v", item)
			}
		}
	}
	if !found {
		t.Fatalf("BGP/PEER_BACKWARD not found in hotspots: %+v", hotspots)
	}

	// 4. 删除任务后 digest 同步清理
	if err := svc.DeleteTask(info.TaskID); err != nil {
		t.Fatalf("delete task failed: %v", err)
	}
	hotspots, _, err = svc.GetGlobalUnmatchedHotspots(10)
	if err != nil {
		t.Fatalf("hotspots after delete failed: %v", err)
	}
	if len(hotspots) != 0 {
		t.Fatalf("digest/hotspots should be removed with task, got %+v", hotspots)
	}
}
