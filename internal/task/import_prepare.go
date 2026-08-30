package task

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"logauditorgo/internal/model"
	"logauditorgo/pkg/logger"
	"logauditorgo/pkg/progress"
)

// failTask 统一的任务失败出口 (TASK-07 / 12.1)。
//
// 旧实现里中途出错的路径有十几种写法，其中相当一部分是
// `logger.Log.Errorf(...); return nil, err` —— 既不置 TaskStatusFailed，
// 也不通知进度追踪器，任务永久停留在 PROCESSING，前端进度条挂死且无法重试。
//
// 现在所有错误出口都必须走这里：
// 置 FAILED + 回写 ErrorMessage（双库） + 进度追踪器 Fail + 返回可上抛的 error。
func (s *Service) failTask(taskDB *gorm.DB, taskInfo *model.TaskInfo, tr *progress.JobTracker, cause error, message string) (*model.TaskInfo, error) {
	if taskInfo != nil {
		taskInfo.Status = model.TaskStatusFailed
		taskInfo.ErrorMessage = cause.Error()
		s.persistTaskInfo(taskDB, taskInfo)
	}
	if tr != nil {
		if message != "" {
			tr.AddLog("error", "%s: %v", message, cause)
		}
		tr.Fail(cause, message)
	}
	return nil, cause
}

// persistTaskInfo 把任务元信息双写回全局库与任务库。
// 12.1 / REANA-07: 旧实现大量 `_ = s.globalDB.Save(...)`，
// 双写失败时两库字段漂移，用户看到的状态与实际数据不一致。
func (s *Service) persistTaskInfo(taskDB *gorm.DB, taskInfo *model.TaskInfo) {
	if taskInfo == nil {
		return
	}
	if err := s.globalDB.Save(taskInfo).Error; err != nil {
		logger.Log.Errorf("[Task Service] save task info to global db failed: %v", err)
	}
	if taskDB != nil {
		if err := taskDB.Save(taskInfo).Error; err != nil {
			logger.Log.Errorf("[Task Service] save task info to task db failed: %v", err)
		}
	}
}

// prepareBundles 逐个文件完成"计数/采样 → 设备识别 → 命名冲突消解 → 旧数据清理"，
// 产出可直接进入落库阶段的 fileBundle 列表。
//
// DEV-16: 设备创建统一走 getOrCreateDevice，与 AutoAssignDevices 行为一致。
// TASK-06: 扫描出错（含单行超 1MB）的文件被判定为格式非法，整文件跳过并记入 failedFiles，
//          不再像旧实现那样"记一行 warning 后继续用被截断的半截数据"。
// TASK-18: 设备前缀改用正则判断，避免不同设备的 [A]_x.log / [B]_x.log 混档。
func (s *Service) prepareBundles(
	taskID string,
	deviceID uint,
	items []FileUploadItem,
	conflictMode string,
	taskDB *gorm.DB,
	dbFilesMap map[string]model.TaskFile,
	batchAssignedNames map[string]bool,
	tr *progress.JobTracker,
) (bundles []*fileBundle, totalLines int64, failedFiles []string) {
	nameTaken := func(name string) bool {
		return batchAssignedNames[name] || dbFilesMap[name].FileName != ""
	}
	// 进度追踪器在单元测试与部分内部调用中为 nil，统一走这个空安全的转发器
	addLog := func(level, format string, args ...interface{}) {
		if tr != nil {
			tr.AddLog(level, format, args...)
		}
	}

	for _, item := range items {
		cleanName := filepath.Base(strings.TrimSpace(item.FileName))
		if cleanName == "" {
			cleanName = "uploaded_log.txt"
		}

		// 磁盘文件可重复打开，走"先计数采样、后流式导入"的两遍模式，内存与文件大小无关；
		// 内存内容 / 一次性 Reader 无法二次读取，只能一次性载入。
		var sample []string
		var allLines []string
		var lineCount int

		if item.FilePath != "" {
			f, err := os.Open(item.FilePath)
			if err != nil {
				failedFiles = append(failedFiles, fmt.Sprintf("%s: 打开失败 (%v)", item.FileName, err))
				addLog("error", "文件 %s 打开失败，已跳过: %v", item.FileName, err)
				continue
			}
			dr, err := openDecodedReader(f)
			if err != nil {
				_ = f.Close()
				failedFiles = append(failedFiles, fmt.Sprintf("%s: 解码失败 (%v)", item.FileName, err))
				addLog("error", "文件 %s 读取/转码失败，已跳过: %v", item.FileName, err)
				continue
			}
			lineCount, sample, err = scanLineSource(dr, hostnameProbeLines)
			_ = f.Close()
			if err != nil {
				// TASK-06: 单行超 1MB 时 bufio.Scanner 会返回 ErrTooLong，
				// 这里按产品决策把整个文件判为格式非法，绝不使用被截断的半截数据。
				failedFiles = append(failedFiles, fmt.Sprintf("%s: 扫描失败，可能存在超过 %d 字节的超长行 (%v)",
					item.FileName, maxScanLineBytes, err))
				addLog("error", "文件 %s 扫描失败（可能存在超过 %d 字节的超长行），已跳过该文件: %v",
					item.FileName, maxScanLineBytes, err)
				continue
			}
		} else {
			rc, err := item.Open()
			if err != nil {
				failedFiles = append(failedFiles, fmt.Sprintf("%s: 打开失败 (%v)", item.FileName, err))
				addLog("error", "文件 %s 打开失败，已跳过: %v", item.FileName, err)
				continue
			}
			dr, err := openDecodedReader(rc)
			if err != nil {
				_ = rc.Close()
				failedFiles = append(failedFiles, fmt.Sprintf("%s: 解码失败 (%v)", item.FileName, err))
				addLog("error", "文件 %s 读取/转码失败，已跳过: %v", item.FileName, err)
				continue
			}
			allLines, sample, err = loadAllLines(dr, hostnameProbeLines)
			_ = rc.Close()
			if err != nil {
				failedFiles = append(failedFiles, fmt.Sprintf("%s: 扫描失败，可能存在超过 %d 字节的超长行 (%v)",
					item.FileName, maxScanLineBytes, err))
				addLog("error", "文件 %s 扫描失败（可能存在超过 %d 字节的超长行），已跳过该文件: %v",
					item.FileName, maxScanLineBytes, err)
				continue
			}
			lineCount = len(allLines)
		}

		if lineCount == 0 {
			continue
		}

		// 1. 设备识别：优先用日志里嗅探到的 Hostname
		detectedHost := detectHostname(sample)
		targetDevName := detectedHost
		if targetDevName == "" {
			if deviceID > 0 {
				var dev model.Device
				if err := taskDB.First(&dev, deviceID).Error; err == nil && dev.DeviceName != "" {
					targetDevName = dev.DeviceName
				}
			}
			if targetDevName == "" {
				targetDevName = fmt.Sprintf("Device-%s", strings.ToUpper(uuid.New().String()[:4]))
			}
		}

		// 2. 文件名带上设备名前缀，区分不同设备的同名日志文件
		prefix := fmt.Sprintf("[%s]_", sanitizeFileNameComponent(targetDevName))
		if !strings.HasPrefix(cleanName, prefix) && !devicePrefixRegex.MatchString(cleanName) {
			cleanName = prefix + cleanName
		}

		// 3. 同名文件冲突处理 (skip / overwrite / rename)
		skipFile := false
		switch conflictMode {
		case "skip":
			if nameTaken(cleanName) {
				logger.Log.Infof("[Task Service] Skipping existing file %s for task %s", cleanName, taskID)
				addLog("warning", "跳过已存在的同名文件: %s", cleanName)
				skipFile = true
			}
		case "overwrite":
			if _, exists := dbFilesMap[cleanName]; exists && !batchAssignedNames[cleanName] {
				// 清理历史旧数据。注意：这里必须与后续的新数据写入构成"逻辑上的同一操作"，
				// 一旦新数据写入失败，persistFileBundle 会做补偿清理，
				// 虽然旧日志无法自动恢复，但至少不会留下"新旧混杂"的错误数据 (TASK-02)。
				addLog("info", "清理覆盖旧同名文件数据: %s", cleanName)
				delErr := taskDB.Transaction(func(tx *gorm.DB) error {
					if err := tx.Where("source_file = ?", cleanName).Delete(&model.LogRecord{}).Error; err != nil {
						return fmt.Errorf("delete old log records failed: %w", err)
					}
					if err := tx.Where("task_id = ? AND file_name = ?", taskID, cleanName).Delete(&model.TaskFile{}).Error; err != nil {
						return fmt.Errorf("delete old task file failed: %w", err)
					}
					return nil
				})
				if delErr != nil {
					failedFiles = append(failedFiles, fmt.Sprintf("%s: 清理旧数据失败 (%v)", cleanName, delErr))
					addLog("error", "清理覆盖旧同名文件 %s 失败，已跳过该文件: %v", cleanName, delErr)
					skipFile = true
					break
				}
				delete(dbFilesMap, cleanName)
			} else if batchAssignedNames[cleanName] {
				// 同批次内出现同名文件：意图是全部导入，追加序号区分，绝不互相覆盖
				candidate, ok := resolveRenameCandidate(cleanName, nameTaken)
				if !ok {
					failedFiles = append(failedFiles, fmt.Sprintf("%s: 自动重命名失败（已达 %d 次上限）", cleanName, maxRenameSeq))
					addLog("error", "文件 %s 自动重命名失败（已达上限），已跳过", cleanName)
					skipFile = true
					break
				}
				addLog("info", "检测到同批次存在同名文件，已自动区分为: %s", candidate)
				cleanName = candidate
			}
		default:
			// rename 模式：历史文件或当前批次中已存在时，自动追加序号多文件共存
			if nameTaken(cleanName) {
				candidate, ok := resolveRenameCandidate(cleanName, nameTaken)
				if !ok {
					failedFiles = append(failedFiles, fmt.Sprintf("%s: 自动重命名失败（已达 %d 次上限）", cleanName, maxRenameSeq))
					addLog("error", "文件 %s 自动重命名失败（已达上限），已跳过", cleanName)
					skipFile = true
					break
				}
				cleanName = candidate
			}
		}
		if skipFile {
			continue
		}
		batchAssignedNames[cleanName] = true

		// 4. 自动关联或创建设备实体 (DEV-16: 与 AutoAssignDevices 共用同一实现)
		fileDevID := deviceID
		if targetDevName != "" {
			dev, devErr := getOrCreateDevice(taskDB, taskID, targetDevName, detectedHost)
			if devErr != nil {
				// 设备创建失败不阻断导入，退化为"未归属"，但必须留痕
				logger.Log.Errorf("[Task Service] get or create device %s failed: %v", targetDevName, devErr)
				addLog("warning", "设备 %s 自动创建失败，该文件的日志将标记为未归属: %v", targetDevName, devErr)
			} else {
				fileDevID = dev.ID
			}
		}

		totalLines += int64(lineCount)
		bundles = append(bundles, &fileBundle{
			taskID:     taskID,
			item:       item,
			cleanName:  cleanName,
			totalLines: lineCount,
			deviceID:   fileDevID,
			lines:      allLines,
		})
	}

	return bundles, totalLines, failedFiles
}

// runRCAPipeline 统一的 RCA 重建流水线 (9.1)。
//
// ImportLogsWithDevice 与 ReanalyzeTask 原本各有一份"采样回灌 → Analyze → 先算后换事务落库"，
// 约 40 行重复代码；抽取后两条链路共用同一实现，行为不可能再漂移。
func (s *Service) runRCAPipeline(taskDB *gorm.DB) (events []model.RCAEvent, totalLogCount, matchedCount int64, err error) {
	if err = taskDB.Model(&model.LogRecord{}).Count(&totalLogCount).Error; err != nil {
		return nil, 0, 0, fmt.Errorf("count log records failed: %w", err)
	}
	if err = taskDB.Model(&model.LogRecord{}).Where("knowledge_id > 0").Count(&matchedCount).Error; err != nil {
		return nil, 0, 0, fmt.Errorf("count matched log records failed: %w", err)
	}

	var normLogs []*model.NormalizedLog
	truncated := false

	rows, rowsErr := taskDB.Model(&model.LogRecord{}).
		Where("knowledge_id > 0 OR severity <= ?", rcaSeverityThreshold).
		Order("timestamp asc, id asc").Rows()
	if rowsErr != nil {
		return nil, totalLogCount, matchedCount, fmt.Errorf("query log records for RCA failed: %w", rowsErr)
	}
	defer rows.Close()

	skippedScan := 0
	for rows.Next() {
		var rec model.LogRecord
		if scanErr := taskDB.ScanRows(rows, &rec); scanErr != nil {
			// REANA-08: 旧实现 `continue` 后静默丢弃该行，且不计数，
			// 报告数字与明细对不上却没有任何线索。这里累计并在返回后上报。
			skippedScan++
			logger.Log.Warnf("[Task Service] Scan row for RCA failed: %v", scanErr)
			continue
		}
		if len(normLogs) >= maxRCALogs {
			truncated = true
			break
		}
		var params map[string]string
		if rec.ParametersJSON != "" {
			_ = json.Unmarshal([]byte(rec.ParametersJSON), &params)
		}
		normLogs = append(normLogs, &model.NormalizedLog{
			ID:              rec.ID,
			RawLog:          rec.RawLog,
			Timestamp:       rec.Timestamp,
			Hostname:        rec.Hostname,
			Module:          rec.Module,
			Severity:        rec.Severity,
			Brief:           rec.Brief,
			SlotInfo:        rec.SlotInfo,
			SourceFile:      rec.SourceFile,
			MessageBody:     rec.MessageBody,
			Parameters:      params,
			DeviceID:        rec.DeviceID,
			KnowledgeID:     rec.KnowledgeID,
			MatchTier:       rec.MatchTier,
			MatchConfidence: rec.MatchConfidence,
		})
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		logger.Log.Errorf("[Task Service] Iterate log rows for RCA failed: %v", rowsErr)
	}
	if skippedScan > 0 {
		logger.Log.Warnf("[Task Service] RCA skipped %d unscannable log rows", skippedScan)
	}

	if len(normLogs) > 0 && s.rcaEngine != nil {
		events = s.rcaEngine.Analyze(normLogs, 300)
	}

	// REANA-03 / TASK-12: 先算后换，纳入同一事务，失败时旧根因完好无损
	if err = taskDB.Transaction(func(tx *gorm.DB) error {
		if delErr := tx.Where("id > 0").Delete(&model.RCAEvent{}).Error; delErr != nil {
			return fmt.Errorf("delete old RCA events failed: %w", delErr)
		}
		if len(events) > 0 {
			if createErr := tx.Create(&events).Error; createErr != nil {
				return fmt.Errorf("create RCA events failed: %w", createErr)
			}
		}
		return nil
	}); err != nil {
		return nil, totalLogCount, matchedCount, err
	}

	_ = truncated
	return events, totalLogCount, matchedCount, nil
}

// recountTaskLogs 重新统计任务的日志总量与匹配量（RCA 流水线失败后的兜底路径）
func recountTaskLogs(taskDB *gorm.DB) (total, matched int64) {
	if err := taskDB.Model(&model.LogRecord{}).Count(&total).Error; err != nil {
		logger.Log.Errorf("[Task Service] Count log records failed: %v", err)
	}
	if err := taskDB.Model(&model.LogRecord{}).Where("knowledge_id > 0").Count(&matched).Error; err != nil {
		logger.Log.Errorf("[Task Service] Count matched log records failed: %v", err)
	}
	return total, matched
}

// refreshAllDeviceStats 批量刷新全部设备的日志量与匹配量 (DEV-05 / DEV-07)
func (s *Service) refreshAllDeviceStats(taskDB *gorm.DB) error {
	var devices []model.Device
	if err := taskDB.Order("id asc").Find(&devices).Error; err != nil {
		return fmt.Errorf("load devices failed: %w", err)
	}
	if len(devices) == 0 {
		return nil
	}
	stats, err := loadDeviceStatsBatch(taskDB)
	if err != nil {
		return fmt.Errorf("aggregate device stats failed: %w", err)
	}
	for _, dev := range devices {
		st := stats[dev.ID]
		if err := taskDB.Model(&model.Device{}).Where("id = ?", dev.ID).Updates(map[string]interface{}{
			"log_count":     int(st.LogCount),
			"matched_count": int(st.Matched),
			"updated_at":    time.Now(),
		}).Error; err != nil {
			logger.Log.Warnf("[Task Service] refresh stats of device %d failed: %v", dev.ID, err)
		}
	}
	return nil
}

// finalizeTaskInfo 统一的任务收尾：刷新计数、置终态、双写落库 (9.1 / REANA-07)
func (s *Service) finalizeTaskInfo(taskDB *gorm.DB, taskInfo *model.TaskInfo, logCount, matchedCount int, rcaCount int, status model.TaskStatus, errMsg string) {
	var fileCount, deviceCount int64
	if err := taskDB.Model(&model.TaskFile{}).Count(&fileCount).Error; err != nil {
		logger.Log.Errorf("[Task Service] count task files failed: %v", err)
	}
	if err := taskDB.Model(&model.Device{}).Count(&deviceCount).Error; err != nil {
		logger.Log.Errorf("[Task Service] count task devices failed: %v", err)
	}

	now := time.Now()
	taskInfo.FileCount = int(fileCount)
	taskInfo.DeviceCount = int(deviceCount)
	taskInfo.LogCount = logCount
	taskInfo.MatchedCount = matchedCount
	taskInfo.RcaCount = rcaCount
	taskInfo.Status = status
	taskInfo.ErrorMessage = errMsg
	if status == model.TaskStatusCompleted {
		taskInfo.FinishTime = &now
	}
	s.persistTaskInfo(taskDB, taskInfo)
}
