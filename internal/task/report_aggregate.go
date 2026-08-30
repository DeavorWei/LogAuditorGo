package task

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"logauditorgo/internal/model"
	"logauditorgo/pkg/logger"
)

// deviceAggregate 单台设备的聚合统计结果。
//
// MinTime / MaxTime 先按字符串承接再解析：SQLite 的 timestamp 由 GORM 以文本形式存储，
// 聚合函数（MIN/MAX）返回的驱动值类型是 string，
// 直接 Scan 到 *time.Time 会报 "unsupported Scan, storing driver.Value type string"。
type deviceAggregate struct {
	DeviceID uint   `gorm:"column:device_id"`
	LogCount int64  `gorm:"column:log_count"`
	Matched  int64  `gorm:"column:matched_count"`
	MinTime  string `gorm:"column:min_time"`
	MaxTime  string `gorm:"column:max_time"`
}

// sqlTimeLayouts 兼容 SQLite 可能返回的多种时间文本格式
var sqlTimeLayouts = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05-07:00",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02T15:04:05",
}

// parseSQLTime 解析聚合函数返回的时间文本，失败返回 nil（绝不产出伪造时间）
func parseSQLTime(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	for _, layout := range sqlTimeLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return &t
		}
	}
	return nil
}

// severityRow 按设备 + 级别聚合的计数行
type severityRow struct {
	DeviceID uint  `gorm:"column:device_id"`
	Severity int   `gorm:"column:severity"`
	Count    int64 `gorm:"column:count"`
}

// moduleRow 按设备 + 模块聚合的计数行
type moduleRow struct {
	DeviceID uint   `gorm:"column:device_id"`
	Module   string `gorm:"column:module"`
	Count    int64  `gorm:"column:count"`
}

// aggregateDeviceReports 一次性聚合出所有设备的日志量、匹配量、级别分布、Top 模块与时间范围。
//
// DEV-07: 原实现在循环里对每台设备执行 4~5 次查询（logCnt / matchCnt / topMods / sevRows / min-max），
// 设备数一多查询次数就线性爆炸，且所有 error 都被丢弃——
// 时间线查询失败时会静默产出"有统计无时间线"的残缺报告。
//
// 这里改为 3 条 GROUP BY 查询覆盖全部设备，并统一上抛错误。
func aggregateDeviceReports(taskDB *gorm.DB) (map[uint]deviceAggregate, map[uint]map[int]int, map[uint][]model.ModuleCount, error) {
	aggRows := make([]deviceAggregate, 0)
	if err := taskDB.Model(&model.LogRecord{}).
		Select(`device_id,
			COUNT(*) AS log_count,
			COALESCE(SUM(CASE WHEN knowledge_id > 0 THEN 1 ELSE 0 END), 0) AS matched_count,
			MIN(timestamp) AS min_time,
			MAX(timestamp) AS max_time`).
		Group("device_id").
		Scan(&aggRows).Error; err != nil {
		return nil, nil, nil, fmt.Errorf("aggregate device log counts failed: %w", err)
	}

	sevRows := make([]severityRow, 0)
	if err := taskDB.Model(&model.LogRecord{}).
		Select("device_id, severity, COUNT(*) AS count").
		Group("device_id, severity").
		Scan(&sevRows).Error; err != nil {
		return nil, nil, nil, fmt.Errorf("aggregate device severity distribution failed: %w", err)
	}

	modRows := make([]moduleRow, 0)
	if err := taskDB.Model(&model.LogRecord{}).
		Select("device_id, module, COUNT(*) AS count").
		Group("device_id, module").
		Order("device_id asc, count desc").
		Scan(&modRows).Error; err != nil {
		return nil, nil, nil, fmt.Errorf("aggregate device module distribution failed: %w", err)
	}

	aggMap := make(map[uint]deviceAggregate, len(aggRows))
	for _, r := range aggRows {
		aggMap[r.DeviceID] = r
	}

	sevMap := make(map[uint]map[int]int)
	for _, r := range sevRows {
		if sevMap[r.DeviceID] == nil {
			sevMap[r.DeviceID] = make(map[int]int)
		}
		sevMap[r.DeviceID][r.Severity] = int(r.Count)
	}

	// Top 模块：SQL 只能全局排序，这里在内存中按设备取前 5 名
	modMap := make(map[uint][]model.ModuleCount)
	for _, r := range modRows {
		modMap[r.DeviceID] = append(modMap[r.DeviceID], model.ModuleCount{Module: r.Module, Count: int(r.Count)})
	}
	for id, mods := range modMap {
		if len(mods) > topModuleLimit {
			modMap[id] = mods[:topModuleLimit]
		}
	}

	return aggMap, sevMap, modMap, nil
}

// topModuleLimit 每台设备展示的主要模块数量
const topModuleLimit = 5

// timelineSpan SQL 聚合出的时间线跨度与总量
type timelineSpan struct {
	Total     int64
	MinTime   time.Time
	MaxTime   time.Time
	HasEvents bool
}

// aggregateTimelineSpan 通过 SQL 聚合计算时间线的起止时间与总条数。
//
// REANA-05: 原实现用 `timeline[0]` 与 `timeline[len-1]` 打印"时间跨度…共汇聚分析 N 条"，
// 而 timeline 是被硬截断的 500 条——大任务报告的时间跨度与条数严重失真，
// 用户会误以为整个任务只发生在几分钟内、只有几百条日志。
// 结论必须基于 SQL 聚合，时间线只用于展示。
func aggregateTimelineSpan(taskDB *gorm.DB, deviceIDs []uint) (timelineSpan, error) {
	var span timelineSpan

	query := taskDB.Model(&model.LogRecord{})
	if len(deviceIDs) > 0 {
		query = query.Where("device_id IN ?", deviceIDs)
	}

	// 同样以文本承接，避免驱动值类型不匹配
	type row struct {
		Total   int64
		MinTime string
		MaxTime string
	}
	var r row
	if err := query.Select("COUNT(*) AS total, MIN(timestamp) AS min_time, MAX(timestamp) AS max_time").
		Scan(&r).Error; err != nil {
		return span, fmt.Errorf("aggregate timeline span failed: %w", err)
	}

	span.Total = r.Total
	minT, maxT := parseSQLTime(r.MinTime), parseSQLTime(r.MaxTime)
	span.HasEvents = r.Total > 0 && minT != nil && maxT != nil
	if minT != nil {
		span.MinTime = *minT
	}
	if maxT != nil {
		span.MaxTime = *maxT
	}
	return span, nil
}

// logAggregateErrorf 统一的多设备报告聚合错误日志
func logAggregateErrorf(format string, args ...interface{}) {
	logger.Log.Errorf("[Task Service] "+format, args...)
}
