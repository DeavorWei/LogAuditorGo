package task

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"logauditorgo/internal/logparser"
	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
)

const (
	// previewMaxScanLines 单文件预览扫描上限：超出后标记 sampled 并按字节比例外推。
	previewMaxScanLines = 100000
	// previewMaxFiles 预览展开的文件数上限，避免超大目录拖垮请求。
	previewMaxFiles = 200
	// previewDeadline 预览总时间预算，超时返回已扫描部分并标记 Truncated。
	previewDeadline = 20 * time.Second
	// previewEstimatedLogsPerSecond 估算耗时使用的保守吞吐（行/秒）。
	previewEstimatedLogsPerSecond = 25000
)

// ImportPreviewFile 预览中的单个文件。
type ImportPreviewFile struct {
	Name         string `json:"name"`
	Path         string `json:"path,omitempty"`
	Size         int64  `json:"size"`
	Lines        int64  `json:"lines"`
	ScannedLines int64  `json:"scanned_lines"`
	Sampled      bool   `json:"sampled"`
}

// ImportPreviewDevice 预览中检测到的设备与行数分布（采样口径）。
type ImportPreviewDevice struct {
	Hostname string `json:"hostname"`
	Lines    int64  `json:"lines"`
}

// ImportPreviewTimeRange 预览中检测到的时间范围。
type ImportPreviewTimeRange struct {
	Start *time.Time `json:"start,omitempty"`
	End   *time.Time `json:"end,omitempty"`
}

// ImportPreview 导入预览：回答"我选了什么 / 会得到什么"。
type ImportPreview struct {
	Files            []ImportPreviewFile     `json:"files"`
	TotalFiles       int                     `json:"total_files"`
	TotalBytes       int64                   `json:"total_bytes"`
	EstimatedLogs    int64                   `json:"estimated_logs"`
	Devices          []ImportPreviewDevice   `json:"devices"`
	TimeRange        *ImportPreviewTimeRange `json:"time_range,omitempty"`
	Conflicts        []string                `json:"conflicts"`
	EstimatedSeconds int                     `json:"estimated_seconds"`
	Truncated        bool                    `json:"truncated"`
	Sampled          bool                    `json:"sampled"`
}

type previewFileResult struct {
	file      ImportPreviewFile
	devices   map[string]int64
	timeStart *time.Time
	timeEnd   *time.Time
}

// PreviewImport 扫描待导入项并返回预览。
//
// 约束（方案审视 §2.3）：与真实导入共用空行/垃圾行判定、清洗与单行长度上限，
// 避免"预览 120 行、导入 118 条"的信任问题。预览只读，不写任务库。
func (s *Service) PreviewImport(taskID string, items []FileUploadItem) (*ImportPreview, error) {
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no log files to preview")
	}

	preview := &ImportPreview{
		Files:     make([]ImportPreviewFile, 0, len(items)),
		Devices:   []ImportPreviewDevice{},
		Conflicts: []string{},
	}

	// 现有文件名（去掉 [设备]_ 前缀后比较），用于冲突提示
	existing := make(map[string]bool)
	if taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID); err == nil {
		var files []model.TaskFile
		if err := taskDB.Where("task_id = ?", taskID).Find(&files).Error; err == nil {
			for _, file := range files {
				existing[stripDevicePrefix(file.FileName)] = true
			}
		}
		storage.ReleaseTaskDB(taskID)
	}

	deviceTotals := make(map[string]int64)
	var globalStart, globalEnd time.Time
	seenConflict := make(map[string]bool)
	startedAt := time.Now()

	for index, item := range items {
		if index >= previewMaxFiles || time.Since(startedAt) > previewDeadline {
			preview.Truncated = true
			break
		}

		result, err := s.previewOneItem(item)
		if err != nil {
			return nil, err
		}

		preview.Files = append(preview.Files, result.file)
		preview.TotalFiles++
		preview.TotalBytes += result.file.Size
		preview.EstimatedLogs += result.file.Lines
		if result.file.Sampled {
			preview.Sampled = true
		}
		for host, count := range result.devices {
			deviceTotals[host] += count
		}
		if result.timeStart != nil && (globalStart.IsZero() || result.timeStart.Before(globalStart)) {
			globalStart = *result.timeStart
		}
		if result.timeEnd != nil && (globalEnd.IsZero() || result.timeEnd.After(globalEnd)) {
			globalEnd = *result.timeEnd
		}
		if existing[result.file.Name] {
			name := stripDevicePrefix(result.file.Name)
			if !seenConflict[name] {
				seenConflict[name] = true
				preview.Conflicts = append(preview.Conflicts, name)
			}
		}
	}

	preview.Devices = sortDeviceTotals(deviceTotals)
	if !globalStart.IsZero() || !globalEnd.IsZero() {
		rangeItem := &ImportPreviewTimeRange{}
		if !globalStart.IsZero() {
			rangeItem.Start = &globalStart
		}
		if !globalEnd.IsZero() {
			rangeItem.End = &globalEnd
		}
		preview.TimeRange = rangeItem
	}

	seconds := int(math.Ceil(float64(preview.EstimatedLogs) / previewEstimatedLogsPerSecond))
	if seconds < 1 {
		seconds = 1
	}
	if seconds > 600 {
		seconds = 600
	}
	preview.EstimatedSeconds = seconds

	return preview, nil
}

func (s *Service) previewOneItem(item FileUploadItem) (*previewFileResult, error) {
	name := filepath.Base(strings.TrimSpace(item.FileName))
	if name == "" || name == "." {
		name = "uploaded_log.txt"
	}

	result := &previewFileResult{
		file:    ImportPreviewFile{Name: name, Path: item.FilePath, Size: item.FileSize},
		devices: make(map[string]int64),
	}

	var reader io.ReadCloser
	if item.FilePath != "" {
		f, err := os.Open(item.FilePath)
		if err != nil {
			return nil, fmt.Errorf("打开文件 %s 失败: %w", name, err)
		}
		reader = f
		if stat, statErr := f.Stat(); statErr == nil {
			result.file.Size = stat.Size()
		}
	} else {
		rc, err := item.Open()
		if err != nil {
			return nil, fmt.Errorf("读取内容 %s 失败: %w", name, err)
		}
		reader = rc
	}
	defer reader.Close()

	decoded, err := openDecodedReader(reader)
	if err != nil {
		return nil, fmt.Errorf("解码文件 %s 失败: %w", name, err)
	}

	scanner := bufio.NewScanner(decoded)
	scanner.Buffer(make([]byte, 64*1024), maxScanLineBytes)

	var scanned int64
	var bytesRead int64
	var start, end time.Time
	for scanner.Scan() {
		line := scanner.Text()
		if logparser.IsBlankOrGarbageLine(line) {
			continue
		}
		clean := logparser.SanitizeLogLine(line)
		if clean == "" {
			continue
		}
		if scanned >= previewMaxScanLines {
			result.file.Sampled = true
			break
		}
		bytesRead += int64(len(clean))
		scanned++

		norm, parseErr := logparser.ParseLine(clean)
		if parseErr != nil || norm == nil {
			continue
		}
		if host := strings.TrimSpace(norm.Hostname); host != "" {
			result.devices[host]++
		}
		ts := norm.Timestamp
		if !ts.IsZero() && ts.Year() > 1970 {
			if start.IsZero() || ts.Before(start) {
				start = ts
			}
			if end.IsZero() || ts.After(end) {
				end = ts
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("扫描文件 %s 失败（可能存在超过 %d 字节的超长行）: %w", name, maxScanLineBytes, err)
	}

	result.file.ScannedLines = scanned
	result.file.Lines = scanned
	// 采样截断时按已读字节比例外推总行数，明确标记 sampled
	if result.file.Sampled && bytesRead > 0 && result.file.Size > bytesRead {
		result.file.Lines = int64(math.Round(float64(scanned) * float64(result.file.Size) / float64(bytesRead)))
	}
	if !start.IsZero() {
		result.timeStart = &start
	}
	if !end.IsZero() {
		result.timeEnd = &end
	}
	return result, nil
}

// stripDevicePrefix 去掉导入时自动追加的 "[设备名]_" 前缀。
func stripDevicePrefix(name string) string {
	return devicePrefixRegex.ReplaceAllString(name, "")
}

// sortDeviceTotals 按行数降序输出设备分布。
func sortDeviceTotals(totals map[string]int64) []ImportPreviewDevice {
	devices := make([]ImportPreviewDevice, 0, len(totals))
	for host, count := range totals {
		devices = append(devices, ImportPreviewDevice{Hostname: host, Lines: count})
	}
	// 小型切片插入排序，避免为预览引入额外依赖
	for i := 1; i < len(devices); i++ {
		for j := i; j > 0 && devices[j].Lines > devices[j-1].Lines; j-- {
			devices[j], devices[j-1] = devices[j-1], devices[j]
		}
	}
	return devices
}
