package rootcause

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"logauditorgo/internal/model"
	"logauditorgo/pkg/logger"
)

type rootRuleCandidate struct {
	rule  *ProtocolFaultRule
	edges []*DAGEdge
}

type Engine struct {
	rules                  []ProtocolFaultRule
	rootCandidatesByModule map[string][]rootRuleCandidate
	fromEdgesByModule      map[string][]*DAGEdge
	indexOnce              sync.Once
}

// NewEngine 创建并初始化根因分析引擎。支持无参调用 rootcause.NewEngine()，也支持传入自定义规则切片。
func NewEngine(customRules ...[]ProtocolFaultRule) *Engine {
	rules := make([]ProtocolFaultRule, len(DefaultRules))
	copy(rules, DefaultRules)
	for _, crList := range customRules {
		if len(crList) > 0 {
			for _, cr := range crList {
				for e := range cr.DAGEdges {
					cr.DAGEdges[e].compile()
				}
			}
			rules = append(rules, crList...)
		}
	}
	eng := &Engine{rules: rules}
	eng.ensureIndexes()
	return eng
}

func (e *Engine) ensureIndexes() {
	e.indexOnce.Do(func() {
		e.buildIndexes()
	})
}

func (e *Engine) buildIndexes() {
	e.rootCandidatesByModule = make(map[string][]rootRuleCandidate)
	e.fromEdgesByModule = make(map[string][]*DAGEdge)

	for rIdx := range e.rules {
		rule := &e.rules[rIdx]
		ruleModuleEdges := make(map[string][]*DAGEdge)
		for eIdx := range rule.DAGEdges {
			edge := &rule.DAGEdges[eIdx]
			edge.compile()

			for _, mod := range edge.fromMod.keywords {
				modUpper := strings.ToUpper(strings.TrimSpace(mod))
				if modUpper != "" {
					e.fromEdgesByModule[modUpper] = append(e.fromEdgesByModule[modUpper], edge)
					ruleModuleEdges[modUpper] = append(ruleModuleEdges[modUpper], edge)
				}
			}
		}

		for modUpper, edges := range ruleModuleEdges {
			e.rootCandidatesByModule[modUpper] = append(e.rootCandidatesByModule[modUpper], rootRuleCandidate{
				rule:  rule,
				edges: edges,
			})
		}
	}
}

// matchRootRule returns the first rule whose DAG root edge matches the given module and brief
func (e *Engine) matchRootRule(module, brief string) *ProtocolFaultRule {
	modUpper := strings.ToUpper(strings.TrimSpace(module))
	candidates, ok := e.rootCandidatesByModule[modUpper]
	if !ok || len(candidates) == 0 {
		return nil
	}

	for _, cand := range candidates {
		for _, edge := range cand.edges {
			if edge.fromBrf.Match(brief) {
				return cand.rule
			}
		}
	}
	return nil
}

// getActiveOutgoingEdges returns all outgoing DAG edges across all rules matching curr module and brief
func (e *Engine) getActiveOutgoingEdges(module, brief string) []*DAGEdge {
	modUpper := strings.ToUpper(strings.TrimSpace(module))
	candidates, ok := e.fromEdgesByModule[modUpper]
	if !ok || len(candidates) == 0 {
		return nil
	}

	var active []*DAGEdge
	for _, edge := range candidates {
		if edge.fromBrf.Match(brief) {
			active = append(active, edge)
		}
	}
	return active
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

	e.ensureIndexes()

	validLogs := make([]*model.NormalizedLog, 0, len(logs))
	for idx, log := range logs {
		if log != nil {
			// (CQ-003) 如果 LogID 未分配，克隆副本并分配稳定临时 ID，避免修改调用方入参
			if log.ID == 0 {
				logCopy := *log
				logCopy.ID = uint(idx + 1)
				validLogs = append(validLogs, &logCopy)
			} else {
				validLogs = append(validLogs, log)
			}
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
	for _, log := range sortedLogs {
		if visited[log.ID] {
			continue
		}

		// (PERF-004) 通过倒排索引 O(1) 检查当前日志是否可以作为某规则的根因
		matchedRule := e.matchRootRule(log.Module, log.Brief)
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

		// (PERF-004) 二分查找窗口右边界 windowEnd：所有 log.Timestamp <= endTime 的日志
		windowEnd := sort.Search(len(sortedLogs), func(k int) bool {
			return sortedLogs[k].Timestamp.After(endTime)
		})

		for len(queue) > 0 {
			curr := queue[0]
			queue = queue[1:]

			// (PERF-004) 获取当前节点的所有活跃出边，若无出边则直接跳过
			activeEdges := e.getActiveOutgoingEdges(curr.Module, curr.Brief)
			if len(activeEdges) == 0 {
				continue
			}

			// (PERF-004) 二分查找起始位置 windowStart：所有 timestamp >= curr.Timestamp 的日志
			windowStart := sort.Search(windowEnd, func(k int) bool {
				return !sortedLogs[k].Timestamp.Before(curr.Timestamp)
			})

			// 在窗口 [windowStart, windowEnd) 内向前搜索 otherLog
			for j := windowStart; j < windowEnd; j++ {
				otherLog := sortedLogs[j]

				if otherLog.ID == curr.ID || visitedInDAG[otherLog.ID] {
					continue
				}

				// 设备/主机名隔离：只聚合相同主机或主机为空的日志
				if log.Hostname != "" && otherLog.Hostname != "" && log.Hostname != otherLog.Hostname {
					continue
				}

				// 检查 curr -> otherLog 是否满足 activeEdges 中任意一条 DAG 边
				edgeMatched := false
				for _, edge := range activeEdges {
					if edge.MatchesNode(otherLog.Module, otherLog.Brief, false) {
						edgeMatched = true
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
