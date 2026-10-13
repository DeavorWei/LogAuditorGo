package task

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
	gormclause "gorm.io/gorm/clause"

	"logauditorgo/internal/model"
	"logauditorgo/pkg/logger"
)

// reanalyzeBatchSize 重分析时每批读取并回写的日志行数
const reanalyzeBatchSize = 1000

// reanalyzeUpdateChunk CASE WHEN 批量更新的分片大小。
//
// REANA-09: 原实现在 1000 行的批次循环内逐条执行 `UPDATE log_records SET ... WHERE id = ?`，
// 百万级重分析会产生百万次单行 UPDATE，耗时远大于导入路径的 CreateInBatches。
// 这里改为分批构造 CASE WHEN 批量写回；
// 设定为 200 行：涉及 12 个更新字段 (含 raw_log 与 message_body) + WHERE IN 条件，
// 单条 SQL 绑定参数量约为 200 * 12 + 200 = 2600 个。
// 既能最大化批量执行效率、减少往返开销，又能稳妥规避 SQLite 变量数量上限（现代 SQLite 上限 32766）。
const reanalyzeUpdateChunk = 200

// batchUpdateLogRecords 批量回写重分析后的日志字段。
//
// 采用 `UPDATE ... SET col = CASE id WHEN ... END WHERE id IN (...)` 形式，
// 一次语句更新整批记录，把 SQL 往返次数从 N 降到 N/chunk。
func batchUpdateLogRecords(tx *gorm.DB, records []model.LogRecord) error {
	if len(records) == 0 {
		return nil
	}

	for start := 0; start < len(records); start += reanalyzeUpdateChunk {
		end := start + reanalyzeUpdateChunk
		if end > len(records) {
			end = len(records)
		}
		if err := batchUpdateChunk(tx, records[start:end]); err != nil {
			return err
		}
	}
	return nil
}

// batchUpdateChunk 构造并执行单条参数化的 CASE WHEN 批量更新语句
func batchUpdateChunk(tx *gorm.DB, chunk []model.LogRecord) error {
	if len(chunk) == 0 {
		return nil
	}

	n := len(chunk)
	idList := make([]uint, 0, n)
	var (
		caseKnowledge   strings.Builder
		caseTier        strings.Builder
		caseConfidence  strings.Builder
		caseSeverity    strings.Builder
		caseTimestamp   strings.Builder
		caseHostname    strings.Builder
		caseModule      strings.Builder
		caseBrief       strings.Builder
		caseSlotInfo    strings.Builder
		caseRawLog      strings.Builder
		caseMessageBody strings.Builder
		caseParams      strings.Builder
	)

	varsKnowledge := make([]interface{}, 0, n)
	varsTier := make([]interface{}, 0, n)
	varsConfidence := make([]interface{}, 0, n)
	varsSeverity := make([]interface{}, 0, n)
	varsTimestamp := make([]interface{}, 0, n)
	varsHostname := make([]interface{}, 0, n)
	varsModule := make([]interface{}, 0, n)
	varsBrief := make([]interface{}, 0, n)
	varsSlotInfo := make([]interface{}, 0, n)
	varsRawLog := make([]interface{}, 0, n)
	varsMessageBody := make([]interface{}, 0, n)
	varsParams := make([]interface{}, 0, n)

	initCase := func(sb *strings.Builder) { sb.WriteString("CASE id ") }
	initCase(&caseKnowledge)
	initCase(&caseTier)
	initCase(&caseConfidence)
	initCase(&caseSeverity)
	initCase(&caseTimestamp)
	initCase(&caseHostname)
	initCase(&caseModule)
	initCase(&caseBrief)
	initCase(&caseSlotInfo)
	initCase(&caseRawLog)
	initCase(&caseMessageBody)
	initCase(&caseParams)

	for _, r := range chunk {
		idList = append(idList, r.ID)

		// WHEN %d 保持数字字面量（uint 绝对安全且不额外消耗 SQLite 绑定参数）
		// THEN ? 统一使用占位符，由 GORM 与 SQLite 驱动通过预编译参数安全绑定传值
		caseKnowledge.WriteString(fmt.Sprintf("WHEN %d THEN ? ", r.ID))
		varsKnowledge = append(varsKnowledge, r.KnowledgeID)

		caseTier.WriteString(fmt.Sprintf("WHEN %d THEN ? ", r.ID))
		varsTier = append(varsTier, r.MatchTier)

		caseConfidence.WriteString(fmt.Sprintf("WHEN %d THEN ? ", r.ID))
		varsConfidence = append(varsConfidence, r.MatchConfidence)

		caseSeverity.WriteString(fmt.Sprintf("WHEN %d THEN ? ", r.ID))
		varsSeverity = append(varsSeverity, r.Severity)

		caseTimestamp.WriteString(fmt.Sprintf("WHEN %d THEN ? ", r.ID))
		varsTimestamp = append(varsTimestamp, r.Timestamp.Format("2006-01-02 15:04:05.000"))

		caseHostname.WriteString(fmt.Sprintf("WHEN %d THEN ? ", r.ID))
		varsHostname = append(varsHostname, r.Hostname)

		caseModule.WriteString(fmt.Sprintf("WHEN %d THEN ? ", r.ID))
		varsModule = append(varsModule, r.Module)

		caseBrief.WriteString(fmt.Sprintf("WHEN %d THEN ? ", r.ID))
		varsBrief = append(varsBrief, r.Brief)

		caseSlotInfo.WriteString(fmt.Sprintf("WHEN %d THEN ? ", r.ID))
		varsSlotInfo = append(varsSlotInfo, r.SlotInfo)

		caseRawLog.WriteString(fmt.Sprintf("WHEN %d THEN ? ", r.ID))
		varsRawLog = append(varsRawLog, r.RawLog)

		caseMessageBody.WriteString(fmt.Sprintf("WHEN %d THEN ? ", r.ID))
		varsMessageBody = append(varsMessageBody, r.MessageBody)

		caseParams.WriteString(fmt.Sprintf("WHEN %d THEN ? ", r.ID))
		varsParams = append(varsParams, r.ParametersJSON)
	}

	endCase := func(sb *strings.Builder) { sb.WriteString("END") }
	endCase(&caseKnowledge)
	endCase(&caseTier)
	endCase(&caseConfidence)
	endCase(&caseSeverity)
	endCase(&caseTimestamp)
	endCase(&caseHostname)
	endCase(&caseModule)
	endCase(&caseBrief)
	endCase(&caseSlotInfo)
	endCase(&caseRawLog)
	endCase(&caseMessageBody)
	endCase(&caseParams)

	err := tx.Model(&model.LogRecord{}).
		Where("id IN ?", idList).
		Updates(map[string]interface{}{
			"knowledge_id":     gormclause.Expr{SQL: caseKnowledge.String(), Vars: varsKnowledge},
			"match_tier":       gormclause.Expr{SQL: caseTier.String(), Vars: varsTier},
			"match_confidence": gormclause.Expr{SQL: caseConfidence.String(), Vars: varsConfidence},
			"severity":         gormclause.Expr{SQL: caseSeverity.String(), Vars: varsSeverity},
			"timestamp":        gormclause.Expr{SQL: caseTimestamp.String(), Vars: varsTimestamp},
			"hostname":         gormclause.Expr{SQL: caseHostname.String(), Vars: varsHostname},
			"module":           gormclause.Expr{SQL: caseModule.String(), Vars: varsModule},
			"brief":            gormclause.Expr{SQL: caseBrief.String(), Vars: varsBrief},
			"slot_info":        gormclause.Expr{SQL: caseSlotInfo.String(), Vars: varsSlotInfo},
			"raw_log":          gormclause.Expr{SQL: caseRawLog.String(), Vars: varsRawLog},
			"message_body":     gormclause.Expr{SQL: caseMessageBody.String(), Vars: varsMessageBody},
			"parameters_json":  gormclause.Expr{SQL: caseParams.String(), Vars: varsParams},
		}).Error
	if err != nil {
		logger.Log.Errorf("[Task Service] Batch CASE-WHEN update failed for %d records: %v", len(chunk), err)
		return fmt.Errorf("batch update log records failed: %w", err)
	}
	return nil
}
