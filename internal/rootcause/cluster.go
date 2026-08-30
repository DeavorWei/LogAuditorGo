package rootcause

import (
	"sort"
	"time"

	"logauditorgo/internal/model"
)

// LogCluster 时序日志聚类
type LogCluster struct {
	StartTime time.Time
	EndTime   time.Time
	Logs      []*model.NormalizedLog

	// StartIdx / EndIdx 是本簇在 Analyze 内部排序后的全量日志切片中的下标区间 [StartIdx, EndIdx)。
	// RCA-02: 倒排索引建立在全量日志上，簇只记录下标区间，
	// 从而避免为每个簇重复构建索引，也让簇与倒排索引共享同一套下标语义。
	StartIdx int
	EndIdx   int
}

// ClusterByTimeWindow 按固定时间窗口对日志进行聚类（默认 300 秒 = 5 分钟）。
//
// 注意：该函数采用**硬边界**划分，跨窗口边界的长因果链会被切断。
// 生产路径请使用 ClusterByOverlappingWindow。保留此函数仅为向后兼容。
func ClusterByTimeWindow(logs []*model.NormalizedLog, windowSeconds int) []LogCluster {
	return ClusterByOverlappingWindow(logs, windowSeconds, 0)
}

// ClusterByOverlappingWindow 采用**重叠滑动窗口**对日志聚类 (RCA-01)。
//
// 背景：ClusterByTimeWindow 此前是全仓唯一的时间窗口聚类实现，却从未被调用，
// RCA 实际上没有任何降噪阶段——与 README 宣称的"5 分钟滑动窗口聚类"不符。
//
// 为什么必须用重叠窗口：硬边界会把跨越 5 分钟分界的长故障链切碎。
// 例如第 4m50s 端口故障、第 5m10s BGP 断开，两者本属同一条链，
// 硬划分下第二个簇失去了根因，随即发生因果倒置或孤儿误判。
// 重叠窗口让边界附近的日志同时出现在相邻两个簇中，
// 再配合 Analyze 里的全局排他认领（claimed）保证每条日志最终只归属一个根因。
//
// 复杂度：O(n log n)（排序）+ O(n)（扫描），窗口重叠只会让每个日志最多被访问两次。
func ClusterByOverlappingWindow(logs []*model.NormalizedLog, windowSeconds, overlapSeconds int) []LogCluster {
	if len(logs) == 0 {
		return nil
	}
	if windowSeconds <= 0 {
		windowSeconds = DefaultWindowSeconds
	}
	if overlapSeconds < 0 {
		overlapSeconds = 0
	}
	if overlapSeconds >= windowSeconds {
		// 重叠量不能超过窗口本身，否则窗口会原地踏步甚至倒退
		overlapSeconds = windowSeconds / 2
	}
	windowDuration := time.Duration(windowSeconds) * time.Second
	stepDuration := time.Duration(windowSeconds-overlapSeconds) * time.Second

	// 稳定排序并保留原始下标映射
	type indexed struct {
		log *model.NormalizedLog
		idx int
	}
	indexedLogs := make([]indexed, 0, len(logs))
	for i, l := range logs {
		if l == nil {
			continue
		}
		indexedLogs = append(indexedLogs, indexed{log: l, idx: i})
	}
	if len(indexedLogs) == 0 {
		return nil
	}
	sort.SliceStable(indexedLogs, func(i, j int) bool {
		if !indexedLogs[i].log.Timestamp.Equal(indexedLogs[j].log.Timestamp) {
			return indexedLogs[i].log.Timestamp.Before(indexedLogs[j].log.Timestamp)
		}
		return indexedLogs[i].log.ID < indexedLogs[j].log.ID
	})

	n := len(indexedLogs)
	var clusters []LogCluster

	for i := 0; i < n; {
		startTime := indexedLogs[i].log.Timestamp
		endTime := startTime.Add(windowDuration)

		j := i
		for j < n && !indexedLogs[j].log.Timestamp.After(endTime) {
			j++
		}

		clusterLogs := make([]*model.NormalizedLog, 0, j-i)
		for k := i; k < j; k++ {
			clusterLogs = append(clusterLogs, indexedLogs[k].log)
		}
		clusters = append(clusters, LogCluster{
			StartTime: startTime,
			EndTime:   indexedLogs[j-1].log.Timestamp,
			Logs:      clusterLogs,
			StartIdx:  i,
			EndIdx:    j,
		})

		if j >= n {
			break
		}
		if overlapSeconds == 0 {
			i = j
			continue
		}

		// 下一个窗口向左回退 overlapSeconds：找到第一个时间戳 >= startTime+step 的日志。
		// 该下标严格大于 i，保证窗口一定会前进，不存在死循环。
		nextStart := startTime.Add(stepDuration)
		k := i + 1
		for k < n && indexedLogs[k].log.Timestamp.Before(nextStart) {
			k++
		}
		i = k
	}

	return clusters
}
