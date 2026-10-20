package task

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
)

// HistogramBucket 单个时间桶的严重度堆叠计数。
type HistogramBucket struct {
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Severe  int64     `json:"severe"`
	Error   int64     `json:"error"`
	Warning int64     `json:"warning"`
	Info    int64     `json:"info"`
	Total   int64     `json:"total"`
}

// LogHistogram 日志时间直方图（v1.4）。
type LogHistogram struct {
	Buckets       []HistogramBucket `json:"buckets"`
	BucketSeconds int64             `json:"bucket_seconds"`
	Total         int64             `json:"total"`
}

// GetLogHistogram 按筛选条件聚合日志时间直方图（严重度堆叠）。
//
// 实现读取 timestamp+severity 两列在内存分桶：10 万行约 3MB，远小于把整行明细拉进内存；
// 相比 SQLite julianday 分组，内存分桶对时间字符串格式/时区更稳健。
func (s *Service) GetLogHistogram(taskID string, req model.LogQueryRequestBody, buckets int) (*LogHistogram, error) {
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}
	if buckets <= 0 {
		buckets = 60
	}
	if buckets > 240 {
		buckets = 240
	}

	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, err
	}
	defer storage.ReleaseTaskDB(taskID)

	query, err := BuildLogFilterScope(taskDB.Model(&model.LogRecord{}), req)
	if err != nil {
		return nil, err
	}

	var span struct {
		MinTime string
		MaxTime string
		Count   int64
	}
	if err := query.Select("MIN(timestamp) AS min_time, MAX(timestamp) AS max_time, COUNT(*) AS count").
		Scan(&span).Error; err != nil {
		return nil, fmt.Errorf("query histogram span failed: %w", err)
	}
	histogram := &LogHistogram{Buckets: []HistogramBucket{}, Total: span.Count}
	if span.Count == 0 {
		return histogram, nil
	}
	startTime := parseSQLTime(span.MinTime)
	endTime := parseSQLTime(span.MaxTime)
	if startTime == nil || endTime == nil {
		return histogram, nil
	}
	spanSeconds := endTime.Sub(*startTime).Seconds()
	if spanSeconds <= 0 {
		spanSeconds = 1
	}
	bucketSeconds := int64(spanSeconds/float64(buckets)) + 1
	if bucketSeconds < 1 {
		bucketSeconds = 1
	}
	histogram.BucketSeconds = bucketSeconds
	for i := 0; i < buckets; i++ {
		histogram.Buckets = append(histogram.Buckets, HistogramBucket{
			Start: startTime.Add(time.Duration(int64(i) * bucketSeconds * int64(time.Second))),
			End:   startTime.Add(time.Duration(int64(i+1) * bucketSeconds * int64(time.Second))),
		})
	}

	type row struct {
		Timestamp string
		Severity  int
	}
	var rows []row
	if err := query.Select("timestamp, severity").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("query histogram rows failed: %w", err)
	}
	for _, item := range rows {
		ts := parseSQLTime(item.Timestamp)
		if ts == nil {
			continue
		}
		index := int(ts.Sub(*startTime).Seconds()) / int(bucketSeconds)
		if index < 0 {
			index = 0
		}
		if index >= len(histogram.Buckets) {
			index = len(histogram.Buckets) - 1
		}
		bucket := &histogram.Buckets[index]
		bucket.Total++
		switch {
		case item.Severity <= 2:
			bucket.Severe++
		case item.Severity <= 4:
			bucket.Error++
		case item.Severity <= 5:
			bucket.Warning++
		default:
			bucket.Info++
		}
	}
	return histogram, nil
}

// AggregatedLogGroup 聚合视图的一组日志（module+brief+hostname+时间窗）。
type AggregatedLogGroup struct {
	Module      string    `json:"module"`
	Brief       string    `json:"brief"`
	Hostname    string    `json:"hostname"`
	WindowStart time.Time `json:"window_start"`
	Count       int64     `json:"count"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
	MaxSeverity int       `json:"max_severity"`
	SampleLogID uint      `json:"sample_log_id"`
}

// GetAggregatedLogs 按 module+brief+hostname+时间窗聚合日志（v1.4）。
//
// 目标：把"同一个接口一分钟内刷 500 条相同日志"折叠成一条带次数与时间范围的卡片。
// 返回按次数降序的前 limit 组；窗口默认 300 秒，可调 5~3600 秒。
func (s *Service) GetAggregatedLogs(taskID string, req model.LogQueryRequestBody, windowSeconds, limit int) ([]AggregatedLogGroup, error) {
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}
	if windowSeconds <= 0 {
		windowSeconds = 300
	}
	if windowSeconds < 5 {
		windowSeconds = 5
	}
	if windowSeconds > 3600 {
		windowSeconds = 3600
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, err
	}
	defer storage.ReleaseTaskDB(taskID)

	query, err := BuildLogFilterScope(taskDB.Model(&model.LogRecord{}), req)
	if err != nil {
		return nil, err
	}

	type row struct {
		ID        uint
		Timestamp string
		Module    string
		Brief     string
		Hostname  string
		Severity  int
	}
	var rows []row
	if err := query.Select("id, timestamp, module, brief, hostname, severity").
		Order("timestamp ASC, id ASC").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("query aggregated logs failed: %w", err)
	}
	if len(rows) == 0 {
		return []AggregatedLogGroup{}, nil
	}

	type accumulator struct {
		group AggregatedLogGroup
	}
	groups := make(map[string]*accumulator)
	var origin time.Time
	for _, item := range rows {
		ts := parseSQLTime(item.Timestamp)
		if ts == nil {
			continue
		}
		if origin.IsZero() {
			origin = *ts
		}
		bucket := int64(ts.Sub(origin).Seconds()) / int64(windowSeconds)
		key := fmt.Sprintf("%s|%s|%s|%d", strings.ToUpper(item.Module), item.Brief, item.Hostname, bucket)
		agg := groups[key]
		if agg == nil {
			agg = &accumulator{group: AggregatedLogGroup{
				Module:      item.Module,
				Brief:       item.Brief,
				Hostname:    item.Hostname,
				WindowStart: origin.Add(time.Duration(bucket * int64(windowSeconds) * int64(time.Second))),
				FirstSeen:   *ts,
				LastSeen:    *ts,
				MaxSeverity: item.Severity,
				SampleLogID: item.ID,
			}}
			groups[key] = agg
		}
		agg.group.Count++
		if ts.Before(agg.group.FirstSeen) {
			agg.group.FirstSeen = *ts
		}
		if ts.After(agg.group.LastSeen) {
			agg.group.LastSeen = *ts
		}
		if item.Severity < agg.group.MaxSeverity {
			agg.group.MaxSeverity = item.Severity
		}
		if item.ID < agg.group.SampleLogID {
			agg.group.SampleLogID = item.ID
		}
	}

	result := make([]AggregatedLogGroup, 0, len(groups))
	for _, agg := range groups {
		result = append(result, agg.group)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].SampleLogID < result[j].SampleLogID
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

// CountCommentLogs 返回任务内注释行数量（用于"显示注释（N）"开关）。
func (s *Service) CountCommentLogs(taskID string) (int64, error) {
	if !isValidTaskID(taskID) {
		return 0, fmt.Errorf("invalid task id: %s", taskID)
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return 0, err
	}
	defer storage.ReleaseTaskDB(taskID)

	var count int64
	if err := taskDB.Model(&model.LogRecord{}).Where("module = ?", "COMMENT").Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
