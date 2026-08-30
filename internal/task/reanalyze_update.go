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
// 这里改为分批构造 CASE WHEN 批量写回；每批 500 行是"SQL 长度"与"往返次数"的平衡点——
// 再大就会逼近 SQLITE_MAX_SQL_LENGTH，再小则往返次数降不下来。
const reanalyzeUpdateChunk = 500

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

// batchUpdateChunk 构造并执行单条 CASE WHEN 批量更新语句
func batchUpdateChunk(tx *gorm.DB, chunk []model.LogRecord) error {
	if len(chunk) == 0 {
		return nil
	}

	idList := make([]uint, 0, len(chunk))
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
		caseMessageBody strings.Builder
		caseParams      strings.Builder
	)

	caseKnowledge.WriteString("CASE id ")
	caseTier.WriteString("CASE id ")
	caseConfidence.WriteString("CASE id ")
	caseSeverity.WriteString("CASE id ")
	caseTimestamp.WriteString("CASE id ")
	caseHostname.WriteString("CASE id ")
	caseModule.WriteString("CASE id ")
	caseBrief.WriteString("CASE id ")
	caseSlotInfo.WriteString("CASE id ")
	caseMessageBody.WriteString("CASE id ")
	caseParams.WriteString("CASE id ")

	for _, r := range chunk {
		idList = append(idList, r.ID)
		caseKnowledge.WriteString(fmt.Sprintf("WHEN %d THEN %d ", r.ID, r.KnowledgeID))
		caseTier.WriteString(fmt.Sprintf("WHEN %d THEN %s ", r.ID, sqlQuote(r.MatchTier)))
		caseConfidence.WriteString(fmt.Sprintf("WHEN %d THEN %f ", r.ID, r.MatchConfidence))
		caseSeverity.WriteString(fmt.Sprintf("WHEN %d THEN %d ", r.ID, r.Severity))
		caseTimestamp.WriteString(fmt.Sprintf("WHEN %d THEN %s ", r.ID, sqlQuote(r.Timestamp.Format("2006-01-02 15:04:05.000"))))
		caseHostname.WriteString(fmt.Sprintf("WHEN %d THEN %s ", r.ID, sqlQuote(r.Hostname)))
		caseModule.WriteString(fmt.Sprintf("WHEN %d THEN %s ", r.ID, sqlQuote(r.Module)))
		caseBrief.WriteString(fmt.Sprintf("WHEN %d THEN %s ", r.ID, sqlQuote(r.Brief)))
		caseSlotInfo.WriteString(fmt.Sprintf("WHEN %d THEN %s ", r.ID, sqlQuote(r.SlotInfo)))
		caseMessageBody.WriteString(fmt.Sprintf("WHEN %d THEN %s ", r.ID, sqlQuote(r.MessageBody)))
		caseParams.WriteString(fmt.Sprintf("WHEN %d THEN %s ", r.ID, sqlQuote(r.ParametersJSON)))
	}

	caseKnowledge.WriteString("END")
	caseTier.WriteString("END")
	caseConfidence.WriteString("END")
	caseSeverity.WriteString("END")
	caseTimestamp.WriteString("END")
	caseHostname.WriteString("END")
	caseModule.WriteString("END")
	caseBrief.WriteString("END")
	caseSlotInfo.WriteString("END")
	caseMessageBody.WriteString("END")
	caseParams.WriteString("END")

	err := tx.Model(&model.LogRecord{}).
		Where("id IN ?", idList).
		Updates(map[string]interface{}{
			"knowledge_id":     gormclause.Expr{SQL: caseKnowledge.String()},
			"match_tier":       gormclause.Expr{SQL: caseTier.String()},
			"match_confidence": gormclause.Expr{SQL: caseConfidence.String()},
			"severity":         gormclause.Expr{SQL: caseSeverity.String()},
			"timestamp":        gormclause.Expr{SQL: caseTimestamp.String()},
			"hostname":         gormclause.Expr{SQL: caseHostname.String()},
			"module":           gormclause.Expr{SQL: caseModule.String()},
			"brief":            gormclause.Expr{SQL: caseBrief.String()},
			"slot_info":        gormclause.Expr{SQL: caseSlotInfo.String()},
			"message_body":     gormclause.Expr{SQL: caseMessageBody.String()},
			"parameters_json":  gormclause.Expr{SQL: caseParams.String()},
		}).Error
	if err != nil {
		logger.Log.Errorf("[Task Service] Batch CASE-WHEN update failed for %d records: %v", len(chunk), err)
		return fmt.Errorf("batch update log records failed: %w", err)
	}
	return nil
}

// sqlQuote 把字符串值安全地包成 SQL 字面量。
// 这些值全部来自本进程对日志行的解析结果，但仍统一转义单引号以防注入。
func sqlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
