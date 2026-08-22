package rootcause

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"logauditorgo/internal/model"
	"logauditorgo/pkg/logger"
)

type Engine struct {
	rules []ProtocolFaultRule
}

func NewEngine(customRules []ProtocolFaultRule) *Engine {
	rules := make([]ProtocolFaultRule, len(DefaultRules))
	copy(rules, DefaultRules)
	if len(customRules) > 0 {
		for _, cr := range customRules {
			for e := range cr.DAGEdges {
				cr.DAGEdges[e].compile()
			}
		}
		rules = append(rules, customRules...)
	}
	return &Engine{rules: rules}
}

// Analyze 执行根因推导与衍生事件聚合 (滑动窗口实现)
func (e *Engine) Analyze(logs []*model.NormalizedLog, windowSeconds int) (events []model.RCAEvent) {
	defer func() {
		if r := recover(); r != nil {
			logger.Log.Errorf("[RCA Engine] Panic recovered in Analyze: %v", r)
		}
	}()

	if e == nil || len(logs) == 0 {
		return nil
	}

	validLogs := make([]*model.NormalizedLog, 0, len(logs))
	for idx, log := range logs {
		if log != nil {
			// 如果 LogID 未分配（如内存分析/单测），自动生成一个稳定临时 ID
			if log.ID == 0 {
				log.ID = uint(idx + 1)
			}
			validLogs = append(validLogs, log)
		}
	}
	if len(validLogs) == 0 {
		return nil
	}

	if windowSeconds <= 0 {
		windowSeconds = 300
	}
	windowDuration := time.Duration(windowSeconds) * time.Second

	// 按时间升序排序
	sortedLogs := make([]*model.NormalizedLog, len(validLogs))
	copy(sortedLogs, validLogs)
	sort.Slice(sortedLogs, func(i, j int) bool {
		return sortedLogs[i].Timestamp.Before(sortedLogs[j].Timestamp)
	})

	logger.Log.Debugf("[RCA Engine] Analyzing %d logs with a sliding window of %ds", len(sortedLogs), windowSeconds)

	visited := make(map[uint]bool)

	// 使用滑动窗口的方式向前搜索
	for i, log := range sortedLogs {
		if visited[log.ID] {
			continue
		}

		// 检查当前日志是否可以作为某规则的根因
		var matchedRule *ProtocolFaultRule
		for rIdx := range e.rules {
			rule := &e.rules[rIdx]
			isRoot := false
			for k := range rule.DAGEdges {
				if rule.DAGEdges[k].MatchesNode(log.Module, log.Brief, true) {
					isRoot = true
					break
				}
			}
			if isRoot {
				matchedRule = rule
				break
			}
		}

		if matchedRule == nil {
			continue
		}

		// 执行前向 BFS 寻找受影响日志（仅在 [Timestamp, Timestamp + windowDuration] 范围内且主机匹配）
		endTime := log.Timestamp.Add(windowDuration)
		correlatedIDs := make([]uint, 0)
		var impactEvents []model.ImpactEvent

		queue := []*model.NormalizedLog{log}
		visitedInDAG := make(map[uint]bool)
		visitedInDAG[log.ID] = true

		depthMap := make(map[uint]int)
		depthMap[log.ID] = 0

		maxDepth := 0

		for len(queue) > 0 {
			curr := queue[0]
			queue = queue[1:]

			// 在窗口内向后搜索 otherLog
			for j := i + 1; j < len(sortedLogs); j++ {
				otherLog := sortedLogs[j]
				// 如果超出当前根因的前向时间窗口，停止搜索
				if otherLog.Timestamp.After(endTime) {
					break
				}

				// 设备/主机名隔离：只聚合相同主机或主机为空的日志
				if log.Hostname != "" && otherLog.Hostname != "" && log.Hostname != otherLog.Hostname {
					continue
				}

				if visitedInDAG[otherLog.ID] || otherLog.Timestamp.Before(curr.Timestamp) {
					continue
				}

				// 检查 curr -> otherLog 是否满足当前规则或任意规则的 DAG 边（支持跨规则级联）
				edgeMatched := false
				for rIdx := range e.rules {
					for k := range e.rules[rIdx].DAGEdges {
						edge := &e.rules[rIdx].DAGEdges[k]
						if edge.MatchesNode(curr.Module, curr.Brief, true) && edge.MatchesNode(otherLog.Module, otherLog.Brief, false) {
							edgeMatched = true
							break
						}
					}
					if edgeMatched {
						break
					}
				}

				if edgeMatched {
					visitedInDAG[otherLog.ID] = true
					correlatedIDs = append(correlatedIDs, otherLog.ID)

					delay := otherLog.Timestamp.Sub(log.Timestamp).Milliseconds()
					impactEvents = append(impactEvents, model.ImpactEvent{
						LogID:      otherLog.ID,
						FromLogID:  curr.ID,
						FromModule: curr.Module,
						Module:     otherLog.Module,
						Brief:      otherLog.Brief,
						Timestamp:  otherLog.Timestamp.Format("2006-01-02 15:04:05"),
						DelayMs:    delay,
					})

					queue = append(queue, otherLog)
					depthMap[otherLog.ID] = depthMap[curr.ID] + 1
					if depthMap[otherLog.ID] > maxDepth {
						maxDepth = depthMap[otherLog.ID]
					}
				}
			}
		}

		if len(correlatedIDs) > 0 {
			for id := range visitedInDAG {
				visited[id] = true
			}

			corrJSON, _ := json.Marshal(correlatedIDs)
			impactJSON, _ := json.Marshal(impactEvents)

			summary := fmt.Sprintf("[%s] %s (触发了 %d 条衍生告警)",
				log.Module, matchedRule.SummaryTemplate, len(correlatedIDs))

			impactLevel := "HIGH"
			if len(correlatedIDs) >= 3 || log.Severity <= 2 {
				impactLevel = "CRITICAL"
			}

			// 置信度计算：基础置信度 0.6 + 随深度和关联事件数增加
			confidence := 0.6 + (float64(maxDepth) * 0.1) + (float64(len(correlatedIDs)) * 0.05)
			if confidence > 0.98 {
				confidence = 0.98
			}

			event := model.RCAEvent{
				RootLogID:         log.ID,
				RootModule:        log.Module,
				RootBrief:         log.Brief,
				RootTimestamp:     log.Timestamp.Format("2006-01-02 15:04:05"),
				RootCauseSummary:  summary,
				CorrelatedLogIDs:  string(corrJSON),
				ImpactEventsJSON:  string(impactJSON),
				ImpactLevel:       impactLevel,
				Confidence:        math.Round(confidence*100) / 100, // 保留两位小数
				RecommendedAction: matchedRule.ActionTemplate,
			}

			logger.Log.Debugf("[RCA Engine] Generated RCA Event: Root=%s/%s (#%d), CorrelatedIDs=%v, ImpactLevel=%s, Conf=%.2f",
				event.RootModule, event.RootBrief, event.RootLogID, correlatedIDs, event.ImpactLevel, event.Confidence)

			events = append(events, event)
		}
	}

	return events
}
