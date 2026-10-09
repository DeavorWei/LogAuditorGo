package task

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"logauditorgo/internal/model"
)

var (
	// paramKeyRegex 校验 JSON path 提取的 key，防止恶意注入
	paramKeyRegex = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_\-.]{0,63}$`)

	// 字段到物理列名映射
	fieldColumnMap = map[string]string{
		"raw_log":          "raw_log",
		"message_body":     "message_body",
		"brief":            "brief",
		"module":           "module",
		"hostname":         "hostname",
		"slot_info":        "slot_info",
		"source_file":      "source_file",
		"parameters":       "parameters_json",
		"severity":         "severity",
		"match_tier":       "match_tier",
		"match_confidence": "match_confidence",
		"knowledge_id":     "knowledge_id",
	}

	// 字段支持的操作符白名单
	fieldSupportedOps = map[string]map[string]bool{
		"raw_log": {
			"contains": true, "not_contains": true, "regex": true, "not_regex": true,
			"eq": true, "prefix": true, "suffix": true,
		},
		"message_body": {
			"contains": true, "not_contains": true, "regex": true, "not_regex": true,
			"eq": true, "prefix": true, "suffix": true,
		},
		"hostname": {
			"contains": true, "not_contains": true, "regex": true, "not_regex": true,
			"eq": true, "prefix": true, "suffix": true,
		},
		"slot_info": {
			"contains": true, "not_contains": true, "regex": true, "not_regex": true,
			"eq": true, "prefix": true, "suffix": true,
		},
		"brief": {
			"contains": true, "not_contains": true, "regex": true, "not_regex": true,
			"eq": true, "prefix": true, "suffix": true, "in": true, "not_in": true,
		},
		"module": {
			"contains": true, "not_contains": true, "regex": true, "not_regex": true,
			"eq": true, "prefix": true, "suffix": true, "in": true, "not_in": true,
		},
		"source_file": {
			"eq": true, "contains": true, "prefix": true,
		},
		"parameters": {
			"kv_contains": true, "kv_not_contains": true, "kv_eq": true,
			"kv_regex": true, "kv_not_regex": true,
		},
		"severity": {
			"lte": true, "gte": true, "eq": true, "in": true, "lt": true, "gt": true,
		},
		"match_tier": {
			"eq": true, "in": true, "not_in": true,
		},
		"match_confidence": {
			"gte": true, "lte": true, "gt": true, "lt": true, "eq": true,
		},
		"knowledge_id": {
			"gt": true, "gte": true, "eq": true, "lte": true, "lt": true,
		},
	}
)

const kvExpr = "CASE WHEN json_valid(parameters_json) THEN json_extract(parameters_json, '$.' || ?) ELSE NULL END"

// conditionSQL 片段及其对应绑定参数
type conditionSQL struct {
	SQL  string
	Args []any
}

// compileSingleCondition 编译单条高级筛选条件为 SQL 片段和参数
func compileSingleCondition(cond model.AdvancedCondition, idx int) (*conditionSQL, error) {
	field := strings.TrimSpace(cond.Field)
	op := strings.TrimSpace(cond.Op)
	val := strings.TrimSpace(cond.Value)

	colName, ok := fieldColumnMap[field]
	if !ok {
		return nil, fmt.Errorf("condition #%d: unsupported field '%s'", idx+1, field)
	}

	validOps, ok := fieldSupportedOps[field]
	if !ok || !validOps[op] {
		return nil, fmt.Errorf("condition #%d: operator '%s' is not supported for field '%s'", idx+1, op, field)
	}

	if len(val) > 256 {
		return nil, fmt.Errorf("condition #%d: value exceeds 256 bytes limit", idx+1)
	}

	// 1. 处理 KV 参数类操作符 (parameters)
	if field == "parameters" {
		parts := strings.SplitN(val, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("condition #%d: parameters value must be in 'key=value' format, got '%s'", idx+1, val)
		}
		paramKey := strings.TrimSpace(parts[0])
		paramVal := strings.TrimSpace(parts[1])
		if !paramKeyRegex.MatchString(paramKey) {
			return nil, fmt.Errorf("condition #%d: invalid parameter key '%s', must match ^[A-Za-z0-9_][A-Za-z0-9_\\-.]{0,63}$", idx+1, paramKey)
		}

		switch op {
		case "kv_contains":
			return &conditionSQL{
				SQL:  kvExpr + " LIKE ? ESCAPE '\\'",
				Args: []any{paramKey, "%" + escapeLikePattern(paramVal) + "%"},
			}, nil
		case "kv_not_contains":
			// 三值逻辑防御：NULL 或不匹配均保留
			return &conditionSQL{
				SQL:  "COALESCE(" + kvExpr + ", '') NOT LIKE ? ESCAPE '\\'",
				Args: []any{paramKey, "%" + escapeLikePattern(paramVal) + "%"},
			}, nil
		case "kv_eq":
			return &conditionSQL{
				SQL:  kvExpr + " = ?",
				Args: []any{paramKey, paramVal},
			}, nil
		case "kv_regex":
			if _, err := regexp.Compile(paramVal); err != nil {
				return nil, fmt.Errorf("condition #%d: invalid regex pattern '%s': %w", idx+1, paramVal, err)
			}
			return &conditionSQL{
				SQL:  kvExpr + " REGEXP ?",
				Args: []any{paramKey, paramVal},
			}, nil
		case "kv_not_regex":
			if _, err := regexp.Compile(paramVal); err != nil {
				return nil, fmt.Errorf("condition #%d: invalid regex pattern '%s': %w", idx+1, paramVal, err)
			}
			return &conditionSQL{
				SQL:  "COALESCE(" + kvExpr + ", '') NOT REGEXP ?",
				Args: []any{paramKey, paramVal},
			}, nil
		}
	}

	// 2. 正则类操作符前置校验与编译
	if op == "regex" || op == "not_regex" {
		if _, err := regexp.Compile(val); err != nil {
			return nil, fmt.Errorf("condition #%d: invalid regex pattern '%s': %w", idx+1, val, err)
		}
		if op == "regex" {
			return &conditionSQL{
				SQL:  colName + " REGEXP ?",
				Args: []any{val},
			}, nil
		}
		// not_regex: 三值逻辑防御
		return &conditionSQL{
			SQL:  "COALESCE(" + colName + ", '') NOT REGEXP ?",
			Args: []any{val},
		}, nil
	}

	// 3. 通用文本与数值操作符
	switch op {
	case "contains":
		return &conditionSQL{
			SQL:  colName + " LIKE ? ESCAPE '\\'",
			Args: []any{"%" + escapeLikePattern(val) + "%"},
		}, nil
	case "not_contains":
		return &conditionSQL{
			SQL:  "COALESCE(" + colName + ", '') NOT LIKE ? ESCAPE '\\'",
			Args: []any{"%" + escapeLikePattern(val) + "%"},
		}, nil
	case "prefix":
		return &conditionSQL{
			SQL:  colName + " LIKE ? ESCAPE '\\'",
			Args: []any{escapeLikePattern(val) + "%"},
		}, nil
	case "suffix":
		return &conditionSQL{
			SQL:  colName + " LIKE ? ESCAPE '\\'",
			Args: []any{"%" + escapeLikePattern(val)},
		}, nil
	case "eq":
		if colName == "module" {
			return &conditionSQL{
				SQL:  colName + " = ? COLLATE NOCASE",
				Args: []any{val},
			}, nil
		}
		return &conditionSQL{
			SQL:  colName + " = ?",
			Args: []any{val},
		}, nil
	case "in":
		items := parseCSVList(val)
		if len(items) == 0 {
			// 空集等价恒假
			return &conditionSQL{SQL: "1 = 0", Args: nil}, nil
		}
		if colName == "module" {
			upperItems := make([]string, len(items))
			for i, it := range items {
				upperItems[i] = strings.ToUpper(it)
			}
			return &conditionSQL{
				SQL:  "UPPER(" + colName + ") IN (?)",
				Args: []any{upperItems},
			}, nil
		}
		return &conditionSQL{
			SQL:  colName + " IN (?)",
			Args: []any{items},
		}, nil
	case "not_in":
		items := parseCSVList(val)
		if len(items) == 0 {
			// 空集 not_in 等价恒真
			return &conditionSQL{SQL: "1 = 1", Args: nil}, nil
		}
		if colName == "module" {
			upperItems := make([]string, len(items))
			for i, it := range items {
				upperItems[i] = strings.ToUpper(it)
			}
			return &conditionSQL{
				SQL:  "COALESCE(UPPER(" + colName + "), '') NOT IN (?)",
				Args: []any{upperItems},
			}, nil
		}
		return &conditionSQL{
			SQL:  "COALESCE(" + colName + ", '') NOT IN (?)",
			Args: []any{items},
		}, nil
	case "lte":
		num, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, fmt.Errorf("condition #%d: expected numeric value for lte, got '%s'", idx+1, val)
		}
		return &conditionSQL{SQL: colName + " <= ?", Args: []any{num}}, nil
	case "gte":
		num, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, fmt.Errorf("condition #%d: expected numeric value for gte, got '%s'", idx+1, val)
		}
		return &conditionSQL{SQL: colName + " >= ?", Args: []any{num}}, nil
	case "lt":
		num, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, fmt.Errorf("condition #%d: expected numeric value for lt, got '%s'", idx+1, val)
		}
		return &conditionSQL{SQL: colName + " < ?", Args: []any{num}}, nil
	case "gt":
		num, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, fmt.Errorf("condition #%d: expected numeric value for gt, got '%s'", idx+1, val)
		}
		return &conditionSQL{SQL: colName + " > ?", Args: []any{num}}, nil
	}

	return nil, fmt.Errorf("condition #%d: unhandled op '%s'", idx+1, op)
}

func parseCSVList(s string) []string {
	parts := strings.Split(s, ",")
	res := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			res = append(res, trimmed)
		}
	}
	return res
}

// BuildLogFilterScope 编译统一查询请求体为 GORM Scope（未分页与排序），
// 供日志列表查询、命中数统计、批量打标、批量摘标及报表导出共用单一逻辑源。
func BuildLogFilterScope(query *gorm.DB, req model.LogQueryRequestBody) (*gorm.DB, error) {
	// 1. 基础条件应用
	if req.DeviceID != nil {
		query = query.Where("device_id = ?", *req.DeviceID)
	}
	if req.Module != "" {
		query = query.Where("UPPER(module) = ?", strings.ToUpper(req.Module))
	}
	if req.Severity != nil {
		query = query.Where("severity <= ?", *req.Severity)
	}
	if req.Brief != "" {
		query = query.Where("brief LIKE ? ESCAPE '\\'", "%"+escapeLikePattern(req.Brief)+"%")
	}
	if req.Hostname != "" {
		query = query.Where("hostname LIKE ? ESCAPE '\\'", "%"+escapeLikePattern(req.Hostname)+"%")
	}
	if req.SourceFile != "" {
		escaped := escapeLikePattern(req.SourceFile)
		query = query.Where("(source_file = ? OR source_file LIKE ? ESCAPE '\\')", req.SourceFile, "%]\\_"+escaped)
	}
	if req.Keyword != "" {
		query = applyKeywordFilter(query, req.Keyword)
	}
	if req.Matched != nil {
		if *req.Matched {
			query = query.Where("knowledge_id > 0")
		} else {
			query = query.Where("knowledge_id = 0 OR knowledge_id IS NULL")
		}
	}
	if req.TimeStart != nil && !req.TimeStart.IsZero() {
		query = query.Where("timestamp >= ?", req.TimeStart.Time)
	}
	if req.TimeEnd != nil && !req.TimeEnd.IsZero() {
		query = query.Where("timestamp <= ?", req.TimeEnd.Time)
	}

	// 2. 标签条件过滤（严格采用子查询，禁止 JOIN 防止行膨胀破坏分页与计数）
	if len(req.TagIDs) > 0 {
		// 对 tag_ids 规范化去重，防止重复 ID 破坏 HAVING COUNT(DISTINCT tag_id) 断言
		uniqueTagIDs := make([]uint, 0, len(req.TagIDs))
		seen := make(map[uint]bool, len(req.TagIDs))
		for _, id := range req.TagIDs {
			if id > 0 && !seen[id] {
				seen[id] = true
				uniqueTagIDs = append(uniqueTagIDs, id)
			}
		}
		if len(uniqueTagIDs) > 0 {
			if strings.EqualFold(req.TagLogic, "all") {
				// 全部满足：GROUP BY log_id HAVING COUNT(DISTINCT tag_id) = len(uniqueTagIDs)
				query = query.Where(
					"id IN (SELECT log_id FROM log_tag_relations WHERE tag_id IN (?) GROUP BY log_id HAVING COUNT(DISTINCT tag_id) = ?)",
					uniqueTagIDs, len(uniqueTagIDs),
				)
			} else {
				// 任意满足 (any)
				query = query.Where(
					"id IN (SELECT log_id FROM log_tag_relations WHERE tag_id IN (?))",
					uniqueTagIDs,
				)
			}
		}
	}

	// 3. 高级筛选条件 (AdvancedFilter)
	if req.Advanced != nil && len(req.Advanced.Conditions) > 0 {
		if len(req.Advanced.Conditions) > 10 {
			return nil, fmt.Errorf("advanced conditions count %d exceeds maximum limit of 10", len(req.Advanced.Conditions))
		}

		condSQLs := make([]*conditionSQL, 0, len(req.Advanced.Conditions))
		for i, cond := range req.Advanced.Conditions {
			cSQL, err := compileSingleCondition(cond, i)
			if err != nil {
				return nil, err
			}
			condSQLs = append(condSQLs, cSQL)
		}

		if strings.EqualFold(req.Advanced.Logic, "OR") {
			// 顶层 OR 逻辑：用括号将所有高级条件包裹 (c1 OR c2 OR ...)
			fragments := make([]string, 0, len(condSQLs))
			allArgs := make([]any, 0)
			for _, c := range condSQLs {
				fragments = append(fragments, c.SQL)
				allArgs = append(allArgs, c.Args...)
			}
			orClause := "(" + strings.Join(fragments, " OR ") + ")"
			query = query.Where(orClause, allArgs...)
		} else {
			// 默认 AND 逻辑：逐条追加
			for _, c := range condSQLs {
				query = query.Where(c.SQL, c.Args...)
			}
		}
	}

	return query, nil
}
