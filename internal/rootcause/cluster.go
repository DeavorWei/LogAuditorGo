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
}

// ClusterByTimeWindow 按滑动时间窗口对日志进行聚类（默认 300 秒 = 5 分钟）
func ClusterByTimeWindow(logs []*model.NormalizedLog, windowSeconds int) []LogCluster {
	if len(logs) == 0 {
		return nil
	}

	if windowSeconds <= 0 {
		windowSeconds = 300
	}
	windowDuration := time.Duration(windowSeconds) * time.Second

	// 按时间升序排序
	sortedLogs := make([]*model.NormalizedLog, len(logs))
	copy(sortedLogs, logs)
	sort.Slice(sortedLogs, func(i, j int) bool {
		return sortedLogs[i].Timestamp.Before(sortedLogs[j].Timestamp)
	})

	var clusters []LogCluster
	currentCluster := LogCluster{
		StartTime: sortedLogs[0].Timestamp,
		EndTime:   sortedLogs[0].Timestamp,
		Logs:      []*model.NormalizedLog{sortedLogs[0]},
	}

	for i := 1; i < len(sortedLogs); i++ {
		log := sortedLogs[i]
		if log.Timestamp.Sub(currentCluster.StartTime) <= windowDuration {
			currentCluster.Logs = append(currentCluster.Logs, log)
			currentCluster.EndTime = log.Timestamp
		} else {
			clusters = append(clusters, currentCluster)
			currentCluster = LogCluster{
				StartTime: log.Timestamp,
				EndTime:   log.Timestamp,
				Logs:      []*model.NormalizedLog{log},
			}
		}
	}
	clusters = append(clusters, currentCluster)

	return clusters
}
