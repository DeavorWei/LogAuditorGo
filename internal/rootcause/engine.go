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

// 根因分析的窗口与规模参数。
//
// 这些常量此前硬编码在 Analyze 里（12.5 节"魔法值"问题），集中定义便于统一调优。
const (
	// DefaultWindowSeconds 因果传播的默认观察窗口（秒）
	DefaultWindowSeconds = 300
	// DefaultOverlapSeconds 重叠窗口的边缘重叠时长（秒）。
	//
	// RCA-01: 若采用硬边界划分，跨越 5 分钟边界的长故障链
	// （例如第 4m50s 端口故障、第 5m10s BGP 断开）会被切成两个簇，
	// 第二个簇随即发生因果倒置或孤儿误判。重叠窗口让边界附近的日志同时落在两个簇里，
	// 再配合全局排他认领保证每条日志只归属一个根因。
	DefaultOverlapSeconds = 60

	// maxRCAEventsPerAnalyze 单次 Analyze 产出的根因事件上限 (RCA-13)。
	// 异常输入（例如全表同一时刻的海量日志）下若无上限，
	// 事件切片与随后的批量 INSERT 会把内存和 SQL 体积一起打爆。
	maxRCAEventsPerAnalyze = 2000
	// maxCorrelatedPerEvent 单个根因事件允许挂载的衍生事件上限 (RCA-13)
	maxCorrelatedPerEvent = 500
)

// 置信度分项权重 (RCA-09)。
//
// 原公式 `0.6 + depth*0.1 + n*0.05` 只要凑够 8 条衍生事件就恒定 0.98，
// 与时间间隔（299s 与 1ms 等价）、主体是否同一、链路质量完全无关，
// 是高置信度误报的直接来源。这里改为可解释的多因子加权。
const (
	confidenceBase          = 0.50 // 基础分
	confidencePerDepth      = 0.09 // 每级传播深度贡献（封顶 0.27）
	confidenceMaxDepthBonus = 0.27
	confidencePerEvidence   = 0.03 // 每条衍生证据贡献（封顶 0.18）
	confidenceMaxEvidence   = 0.18
	confidenceTimeBonus     = 0.15 // 时间衰减项上限（半衰期 150s）
	confidenceEntityBonus   = 0.08 // 实例维度一致时的加分
	confidenceEntityPenalty = 0.10 // 实例维度信息单侧缺失时的惩罚
	confidenceHalfLifeSec   = 150.0
	confidenceCap           = 0.98
	confidenceFloor         = 0.05
)

// recoveryBriefHints 恢复类/正常类助记符特征 (RCA-14)。
//
// 形如 BFD_SESS_DOWN_CLEAR、IF_UP 的事件是"故障已恢复"的信号，
// 把它们当作下游衍生故障计入传播链，会虚增 correlatedIDs 与置信度。
var recoveryBriefHints = []string{"_CLEAR", "_RECOVER", "_UP", "_NORMAL", "_OK", "_RESUME"}

// entityParamKeys 实例维度的参数别名表 (RCA-04)。
//
// 背景：旧实现的关联判据只有"模块 + 助记符 + 时间 + 主机"，
// 于是 SW-01 上两个互不相关的端口（GE1/0/1 与 GE1/0/2）
// 只要在窗口内先后出现 IF_DOWN 与 BFD_SESS_DOWN 就会被强行串成一条根因链。
// 这里把端口 / 对端 IP / 会话 ID 三类"实例身份"纳入关联键。
var entityParamKeys = map[string][]string{
	"interface": {"InterfaceName", "Interface", "IfName", "PortName", "IfIndex", "LocalIfname", "LocalIfName", "IfIndex"},
	"peer_ip":   {"PeerIP", "PeerAddr", "PeerAddress", "NbrAddr", "RemoteAddr", "RemoteIP", "NbrIpAddr"},
	"session_id": {"SessionID", "SessionId", "SessID", "SessId", "Session"},
}

type rootRuleCandidate struct {
	rule  *ProtocolFaultRule
	edges []*DAGEdge
	// isChainHead[i] 标识 edges[i] 是否为该规则的"链首边"。
	//
	// RCA-03: 原实现只要某条边的 from 侧命中就把该日志当作根因，
	// 但规则中间节点的 from 侧同样会命中（例如规则 1 中 BFD_SESS_DOWN 是边 2 的 from，
	// 它真实身份是 IF_DOWN 的下游）。当真正的上游因设备隔离/窗口边界未被关联时，
	// 中间节点就会被误判为根，导致处置建议方向性错误。
	// 这里预先标记出"不被同规则任何 to 侧覆盖"的边，匹配时优先作为根因候选。
	isChainHead []bool
}

type Engine struct {
	rules                  []ProtocolFaultRule
	rootCandidatesByModule map[string][]rootRuleCandidate
	fromEdgesByModule      map[string][]*DAGEdge
	// toEdgesByModuleBrief 倒排索引：UPPER(模块) + "|" + UPPER(助记符) → 以该组合为 to 侧的边。
	//
	// RCA-02: 旧实现每次 BFS 都线性扫描整个时间窗口（单根 O(q·W)，最坏 O(W²)），
	// 密集故障场景下百万行日志完全不可用。有了倒排索引后，
	// 只需按目标节点的 Module+Brief 取出候选边，扫描量降到 O(k·m)。
	toEdgesByModuleBrief map[string][]*DAGEdge

	indexOnce sync.Once
	// cyclicRules 在加载期拓扑排序检环时发现的成环规则 ID (RCA-16)
	cyclicRules []string
}

// isCoveredByTo 判断某条边的 from 侧是否被另一条边的 to 侧覆盖
// （即该 from 节点在同规则内是其他边的下游产物）
func isCoveredByTo(fromMod, fromBrf, toMod, toBrf *Pattern) bool {
	if fromMod == nil || toMod == nil {
		return false
	}
	modHit := false
	for _, m := range fromMod.keywords {
		if toMod.Match(m) {
			modHit = true
			break
		}
	}
	if !modHit {
		return false
	}
	if fromBrf == nil || toBrf == nil {
		return false
	}
	for _, b := range fromBrf.keywords {
		if toBrf.Match(b) {
			return true
		}
	}
	return false
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
	e.toEdgesByModuleBrief = make(map[string][]*DAGEdge)

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

			// RCA-02: 建 to 侧倒排索引，供 BFS 直接取候选边
			for _, mod := range edge.toMod.keywords {
				modUpper := strings.ToUpper(strings.TrimSpace(mod))
				if modUpper == "" {
					continue
				}
				for _, brf := range edge.toBrf.keywords {
					brfUpper := strings.ToUpper(strings.TrimSpace(brf))
					if brfUpper == "" {
						continue
					}
					key := modUpper + "|" + brfUpper
					e.toEdgesByModuleBrief[key] = append(e.toEdgesByModuleBrief[key], edge)
				}
			}
		}

		// 标记每条边的 from 侧是否被同规则内其他边的 to 侧覆盖（RCA-03）
		edgeCount := len(rule.DAGEdges)
		chainHead := make([]bool, edgeCount)
		for i := 0; i < edgeCount; i++ {
			head := true
			for j := 0; j < edgeCount && head; j++ {
				if i == j {
					continue
				}
				if isCoveredByTo(&rule.DAGEdges[i].fromMod, &rule.DAGEdges[i].fromBrf,
					&rule.DAGEdges[j].toMod, &rule.DAGEdges[j].toBrf) {
					head = false
				}
			}
			chainHead[i] = head
		}

		for modUpper, edges := range ruleModuleEdges {
			heads := make([]bool, 0, len(edges))
			for _, edge := range edges {
				idx := edgeIndexIn(rule, edge)
				heads = append(heads, idx >= 0 && chainHead[idx])
			}
			e.rootCandidatesByModule[modUpper] = append(e.rootCandidatesByModule[modUpper], rootRuleCandidate{
				rule:        rule,
				edges:       edges,
				isChainHead: heads,
			})
		}

		// RCA-16: 加载期拓扑排序检环。
		// 自定义规则若配置成环，旧实现靠 visitedInDAG 隐式防重入（不会死循环），
		// 但永远没有告警，配置错误只能靠线上现象反推。
		if hasCycle(rule) {
			e.cyclicRules = append(e.cyclicRules, rule.ID)
			logger.Log.Warnf("[RCA Engine] rule %q contains a cyclic DAG; propagation order may be unstable", rule.ID)
		}
	}
}

// hasCycle 对单条规则的 DAG 边做拓扑排序检环 (RCA-16)。
// 节点按 "Module|Brief" 归一化，若存在环则无法完成拓扑排序。
func hasCycle(rule *ProtocolFaultRule) bool {
	n := len(rule.DAGEdges)
	if n < 2 {
		return false
	}

	nodeIndex := make(map[string]int)
	nodeName := func(p *Pattern) string {
		if p == nil || len(p.keywords) == 0 {
			return ""
		}
		return strings.ToUpper(strings.TrimSpace(p.keywords[0]))
	}
	idOf := func(name string) int {
		if name == "" {
			return -1
		}
		if idx, ok := nodeIndex[name]; ok {
			return idx
		}
		idx := len(nodeIndex)
		nodeIndex[name] = idx
		return idx
	}

	adj := make([][]int, 0, n)
	indegree := make([]int, 0, n)
	ensure := func(size int) {
		for len(adj) < size {
			adj = append(adj, nil)
			indegree = append(indegree, 0)
		}
	}

	for i := range rule.DAGEdges {
		from := idOf(nodeName(&rule.DAGEdges[i].fromMod))
		to := idOf(nodeName(&rule.DAGEdges[i].toMod))
		if from < 0 || to < 0 || from == to {
			continue
		}
		ensure(len(nodeIndex))
		adj[from] = append(adj[from], to)
		indegree[to]++
	}
	ensure(len(nodeIndex))

	queue := make([]int, 0, len(adj))
	for i := range adj {
		if indegree[i] == 0 {
			queue = append(queue, i)
		}
	}
	visited := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		visited++
		for _, next := range adj[cur] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	return visited != len(adj)
}

// edgeIndexIn 返回 edge 在 rule.DAGEdges 中的下标（按元素地址比较），未找到返回 -1
func edgeIndexIn(rule *ProtocolFaultRule, edge *DAGEdge) int {
	for i := range rule.DAGEdges {
		if &rule.DAGEdges[i] == edge {
			return i
		}
	}
	return -1
}

// matchRootRule returns the first rule whose DAG root edge matches the given module and brief.
//
// RCA-03: 优先匹配"链首边"，链首边都命中不了时才退化为匹配任意 from 边。
// 这样可避免把规则中间节点（其真实身份是上游的下游）误判为根因。
func (e *Engine) matchRootRule(module, brief string) *ProtocolFaultRule {
	modUpper := strings.ToUpper(strings.TrimSpace(module))
	candidates, ok := e.rootCandidatesByModule[modUpper]
	if !ok || len(candidates) == 0 {
		return nil
	}

	// 第一轮：只认链首边
	for _, cand := range candidates {
		for i, edge := range cand.edges {
			if i < len(cand.isChainHead) && cand.isChainHead[i] && edge.fromBrf.Match(brief) {
				return cand.rule
			}
		}
	}
	// 第二轮：退化匹配任意 from 边，保持原有召回能力
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

// isRecoveryBrief 判断助记符是否为"恢复/正常"类事件 (RCA-14)。
// 这类事件不应被当作下游衍生故障计入传播链。
func isRecoveryBrief(brief string) bool {
	upper := strings.ToUpper(strings.TrimSpace(brief))
	if upper == "" {
		return false
	}
	for _, hint := range recoveryBriefHints {
		if strings.HasSuffix(upper, hint) {
			return true
		}
	}
	// 例如 BFD_SESS_DOWN_CLEAR：恢复标记出现在中间
	return strings.Contains(upper, "_CLEAR") || strings.Contains(upper, "_RECOVER")
}

// entityValues 抽取日志的实例维度实体（接口 / 对端 IP / 会话 ID），用于 RCA-04 的同实例校验
func entityValues(log *model.NormalizedLog) map[string]string {
	if log == nil || len(log.Parameters) == 0 {
		return nil
	}
	out := make(map[string]string, len(entityParamKeys))
	for role, keys := range entityParamKeys {
		for _, k := range keys {
			if v, ok := log.Parameters[k]; ok && strings.TrimSpace(v) != "" {
				out[role] = strings.TrimSpace(v)
				break
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// entityConsistency 判定两条日志的实例维度是否一致。
//
// 返回值：
//   - consistent=true  ：双方实体均存在且全部匹配，可放心关联；
//   - consistent=false, hasSignal=true ：有一方缺失实体信息，按产品决策宽松放行但打标降级置信度；
//   - reject=true      ：双方实体都存在且不匹配（不同端口/不同会话），必须拒绝关联。
func entityConsistency(a, b *model.NormalizedLog) (consistent bool, hasSignal bool, reject bool) {
	ea, eb := entityValues(a), entityValues(b)
	if len(ea) == 0 && len(eb) == 0 {
		// 双方都没有实例信息（参数未被抽取）：没有信号，不做加减分，也不拒绝
		return false, false, false
	}
	if len(ea) == 0 || len(eb) == 0 {
		return false, true, false
	}

	compared := 0
	for role, va := range ea {
		vb, ok := eb[role]
		if !ok {
			continue
		}
		compared++
		if !strings.EqualFold(va, vb) {
			// 同一角色出现不同取值：明确不属于同端口/同会话的故障
			return false, true, true
		}
	}
	if compared == 0 {
		return false, true, false
	}
	return true, true, false
}

// canCorrelate 判定两条日志是否可能属于同一故障主体 (RCA-06 / RCA-13)。
//
// 旧实现只在"双方字段都非空"时才比较，单侧为空即放行，
// 于是主机名解析失败的日志会与所有主机互相关联（审计报告场景 1）。
// 这里改为保守关联：任何一侧缺失主体标识时，只有在另一侧同样缺失、且来源文件一致时才放行。
func canCorrelate(a, b *model.NormalizedLog) bool {
	if a == nil || b == nil {
		return false
	}
	// 1. 设备维度最可靠
	if a.DeviceID > 0 && b.DeviceID > 0 {
		return a.DeviceID == b.DeviceID
	}
	if a.DeviceID > 0 || b.DeviceID > 0 {
		return false
	}
	// 2. 其次按主机名
	if a.Hostname != "" && b.Hostname != "" {
		return a.Hostname == b.Hostname
	}
	if a.Hostname != "" || b.Hostname != "" {
		return false
	}
	// 3. 都没有主体标识时退化为"同一来源文件"，未知来源不参与跨主体关联
	return a.SourceFile == b.SourceFile
}

// invertedIndex 日志下标倒排索引 (RCA-02)。
// key 为 UPPER(模块) + "|" + UPPER(助记符)，value 为按时间升序排列的 sortedLogs 下标。
type invertedIndex struct {
	logs    []*model.NormalizedLog
	byKey   map[string][]int
	builder strings.Builder
}

func newInvertedIndex(logs []*model.NormalizedLog) *invertedIndex {
	ix := &invertedIndex{logs: logs, byKey: make(map[string][]int, len(logs))}
	for i, l := range logs {
		if l == nil {
			continue
		}
		ix.byKey[ix.keyOf(l.Module, l.Brief)] = append(ix.byKey[ix.keyOf(l.Module, l.Brief)], i)
	}
	return ix
}

// keyOf 构造倒排键。使用 strings.Builder 复用缓冲，避免百万行下产生大量临时字符串。
func (ix *invertedIndex) keyOf(module, brief string) string {
	ix.builder.Reset()
	ix.builder.WriteString(strings.ToUpper(strings.TrimSpace(module)))
	ix.builder.WriteByte('|')
	ix.builder.WriteString(strings.ToUpper(strings.TrimSpace(brief)))
	return ix.builder.String()
}

// rangeOf 返回 key 对应下标数组中，时间戳落在 (after, before] 且下标在 [lo, hi) 内的区间。
// 下标数组本身按时间升序，因此可用两次二分定位，整体复杂度 O(log n + 命中数)。
func (ix *invertedIndex) rangeOf(key string, lo, hi int, after, before time.Time) (int, int) {
	idxList, ok := ix.byKey[key]
	if !ok || lo >= hi {
		return 0, 0
	}
	// 第一个下标 >= lo 的元素
	start := sort.Search(len(idxList), func(m int) bool { return idxList[m] >= lo })
	// 第一个时间戳 > after 的元素（严格大于，杜绝同秒因果倒置，RCA-12）
	start2 := sort.Search(len(idxList), func(m int) bool {
		t := ix.logs[idxList[m]].Timestamp
		if t.Equal(after) {
			return ix.logs[idxList[m]].ID > ix.logs[lo].ID
		}
		return t.After(after)
	})
	if start2 > start {
		start = start2
	}
	end := sort.Search(len(idxList), func(m int) bool {
		return ix.logs[idxList[m]].Timestamp.After(before)
	})
	end2 := sort.Search(len(idxList), func(m int) bool { return idxList[m] >= hi })
	if end2 < end {
		end = end2
	}
	if start >= end {
		return 0, 0
	}
	return start, end
}

// Analyze 执行根因推导与衍生事件聚合。
//
// 流水线：
//  0. 重叠滑动窗口聚类降噪（RCA-01，此前 ClusterByTimeWindow 是死代码，RCA 实际无降噪阶段）；
//  1. 在窗口内以倒排索引做 O(k·m) 的 BFS 传播（RCA-02）；
//  2. 实例维度（接口/对端 IP/会话 ID）一致性校验（RCA-04）；
//  3. 全局排他认领，保证每条日志只归属一个根因（RCA-08）；
//  4. 多因子置信度与四级影响等级（RCA-09 / RCA-16）。
func (e *Engine) Analyze(logs []*model.NormalizedLog, windowSeconds int) (events []model.RCAEvent) {
	// RCA-16: recover 后必须置位错误标志，而不是"静默返回已累积的偏少结果"。
	analyzeErr := error(nil)

	defer func() {
		if r := recover(); r != nil {
			analyzeErr = fmt.Errorf("panic in RCA Analyze: %v", r)
			logger.Log.Errorf("[RCA Engine] Panic recovered in Analyze: %v", r)
			events = nil
		}
		if analyzeErr != nil {
			logger.Log.Errorf("[RCA Engine] Analyze aborted: %v", analyzeErr)
		}
	}()

	if e == nil || len(logs) == 0 {
		return nil
	}

	e.ensureIndexes()

	// RCA-05: 原实现用 `uint(idx + 1)` 给未分配 ID 的日志补临时号，
	// 而下标与真实 ID 的取值范围完全重叠——若 logs[4].ID == 0 而 logs[0].ID == 5，
	// 两者都会得到 5，visited / visitedInDAG / depthMap / correlatedIDs 全部以 ID 为键，
	// 碰撞会静默吞掉一条日志或错连因果链。
	// 这里先扫描出真实 ID 的最大值，临时号从 maxRealID+1 开始分配，彻底避开冲突区间。
	var maxRealID uint
	for _, log := range logs {
		if log != nil && log.ID > maxRealID {
			maxRealID = log.ID
		}
	}

	validLogs := make([]*model.NormalizedLog, 0, len(logs))
	var tempSeq uint
	for _, log := range logs {
		if log == nil || log.Timestamp.IsZero() {
			continue
		}
		// (CQ-003) 如果 LogID 未分配，克隆副本并分配稳定临时 ID，避免修改调用方入参
		if log.ID == 0 {
			tempSeq++
			logCopy := *log
			logCopy.ID = maxRealID + tempSeq
			validLogs = append(validLogs, &logCopy)
		} else {
			validLogs = append(validLogs, log)
		}
	}
	if len(validLogs) == 0 {
		return nil
	}

	if windowSeconds <= 0 {
		windowSeconds = DefaultWindowSeconds
	}

	// 按时间升序排序
	//
	// RCA-07: 原实现用 sort.Slice（不稳定排序）。华为日志多为秒级精度，同秒日志海量，
	// 相对顺序随机会导致"谁当根、事件输出顺序、BFS 遍历序"每次重分析都可能不同，
	// 测试与线上结论无法对齐。改为稳定排序，并追加 ID 次级键兜底。
	sortedLogs := make([]*model.NormalizedLog, len(validLogs))
	copy(sortedLogs, validLogs)
	sort.SliceStable(sortedLogs, func(i, j int) bool {
		if !sortedLogs[i].Timestamp.Equal(sortedLogs[j].Timestamp) {
			return sortedLogs[i].Timestamp.Before(sortedLogs[j].Timestamp)
		}
		return sortedLogs[i].ID < sortedLogs[j].ID
	})

	logger.Log.Debugf("[RCA Engine] Analyzing %d logs with overlapping window %ds (overlap %ds)",
		len(sortedLogs), windowSeconds, DefaultOverlapSeconds)

	// RCA-02: 倒排索引在全量日志上预建，避免每个簇内重复构建
	ix := newInvertedIndex(sortedLogs)

	// RCA-01: 重叠滑动窗口聚类，作为 BFS 前置降噪
	clusters := ClusterByOverlappingWindow(sortedLogs, windowSeconds, DefaultOverlapSeconds)

	// RCA-08: 全局已认领日志表（logID → 根因 logID）。
	// 旧实现的 visited 只在"本次 BFS 找到衍生事件"时回填，且不阻止"当衍生"，
	// 于是同一条日志会同时出现在多个 RCA 事件里，前端展示互相矛盾的多重根因。
	claimed := make(map[uint]uint, len(sortedLogs))

	events = make([]model.RCAEvent, 0, 16)

	for _, cluster := range clusters {
		if len(events) >= maxRCAEventsPerAnalyze {
			logger.Log.Warnf("[RCA Engine] RCA event limit (%d) reached, stopping analysis", maxRCAEventsPerAnalyze)
			break
		}
		lo, hi := cluster.StartIdx, cluster.EndIdx
		if lo < 0 {
			lo = 0
		}
		if hi > len(sortedLogs) || hi < 0 {
			hi = len(sortedLogs)
		}
		if lo >= hi {
			continue
		}

		for i := lo; i < hi; i++ {
			if len(events) >= maxRCAEventsPerAnalyze {
				break
			}
			log := sortedLogs[i]
			if log == nil {
				continue
			}
			// 已归属于某个根因的事件不再参与后续 BFS
			if _, ok := claimed[log.ID]; ok {
				continue
			}

			matchedRule := e.matchRootRule(log.Module, log.Brief)
			if matchedRule == nil {
				continue
			}

			event, ok := e.propagate(sortedLogs, ix, lo, hi, log, matchedRule, windowSeconds, claimed)
			if !ok {
				continue
			}
			events = append(events, event)
		}
	}

	return events
}

// propagate 从根因日志出发，在给定窗口内做倒排索引 BFS 传播，产出一个 RCA 事件。
func (e *Engine) propagate(
	sortedLogs []*model.NormalizedLog,
	ix *invertedIndex,
	lo, hi int,
	root *model.NormalizedLog,
	rule *ProtocolFaultRule,
	windowSeconds int,
	claimed map[uint]uint,
) (model.RCAEvent, bool) {
	windowDuration := time.Duration(windowSeconds) * time.Second
	endTime := root.Timestamp.Add(windowDuration)

	correlatedIDs := make([]uint, 0, 8)
	var impactEvents []model.ImpactEvent

	queue := []*model.NormalizedLog{root}
	visitedInDAG := make(map[uint]bool, 8)
	visitedInDAG[root.ID] = true

	depthMap := map[uint]int{root.ID: 0}
	maxDepth := 0

	entityOK, entitySignal := true, false
	firstDelay := time.Duration(-1)

	for len(queue) > 0 && len(correlatedIDs) < maxCorrelatedPerEvent {
		curr := queue[0]
		queue = queue[1:]

		activeEdges := e.getActiveOutgoingEdges(curr.Module, curr.Brief)
		if len(activeEdges) == 0 {
			continue
		}

		// RCA-02: 只遍历"目标 Module+Brief 有边指向"的候选日志，
		// 不再对整个窗口做线性扫描。
		for _, edge := range activeEdges {
			if len(correlatedIDs) >= maxCorrelatedPerEvent {
				break
			}
			for _, brf := range edge.toBrf.keywords {
				for _, mod := range edge.toMod.keywords {
					key := ix.keyOf(mod, brf)
					start, end := ix.rangeOf(key, lo, hi, curr.Timestamp, endTime)
					for m := start; m < end; m++ {
						idx := ix.byKey[key][m]
						otherLog := sortedLogs[idx]
						if otherLog == nil || otherLog.ID == curr.ID || visitedInDAG[otherLog.ID] {
							continue
						}
						if len(correlatedIDs) >= maxCorrelatedPerEvent {
							break
						}

						// RCA-14: 恢复类事件不作为下游衍生故障入链
						if isRecoveryBrief(otherLog.Brief) {
							continue
						}
						if !edge.MatchesNode(otherLog.Module, otherLog.Brief, false) {
							continue
						}
						// 设备与主机隔离 (RCA-06 / RCA-13)
						if !canCorrelate(root, otherLog) {
							continue
						}
						// RCA-04: 实例维度一致性校验
						consistent, hasSignal, reject := entityConsistency(curr, otherLog)
						if reject {
							continue
						}
						if hasSignal && !consistent {
							entityOK = false
						}
						if hasSignal {
							entitySignal = true
						}
						// RCA-08: 全局排他认领——已被其他根因认领的日志不再重复归属
						if owner, exists := claimed[otherLog.ID]; exists && owner != root.ID {
							continue
						}

						visitedInDAG[otherLog.ID] = true
						claimed[otherLog.ID] = root.ID
						correlatedIDs = append(correlatedIDs, otherLog.ID)

						delay := otherLog.Timestamp.Sub(root.Timestamp)
						if firstDelay < 0 || delay < firstDelay {
							firstDelay = delay
						}
						impactEvents = append(impactEvents, model.ImpactEvent{
							LogID:      otherLog.ID,
							FromLogID:  curr.ID,
							FromModule: curr.Module,
							Module:     otherLog.Module,
							Brief:      otherLog.Brief,
							Timestamp:  otherLog.Timestamp.Format("2006-01-02 15:04:05"),
							DelayMs:    delay.Milliseconds(),
						})

						queue = append(queue, otherLog)
						depthMap[otherLog.ID] = depthMap[curr.ID] + 1
						if depthMap[otherLog.ID] > maxDepth {
							maxDepth = depthMap[otherLog.ID]
						}
					}
				}
			}
		}
	}

	if len(correlatedIDs) == 0 {
		return model.RCAEvent{}, false
	}

	// 根因自身也标记为已认领，避免它被别的根因抢走当"衍生"
	claimed[root.ID] = root.ID

	corrJSON, _ := json.Marshal(correlatedIDs)
	impactJSON, _ := json.Marshal(impactEvents)

	summary := fmt.Sprintf("[%s] %s (触发了 %d 条衍生告警)",
		root.Module, rule.SummaryTemplate, len(correlatedIDs))

	confidence := calculateConfidence(maxDepth, len(correlatedIDs), firstDelay, entityOK, entitySignal)

	logger.Log.Debugf("[RCA Engine] Generated RCA Event: Root=%s/%s (#%d), CorrelatedIDs=%v, ImpactLevel=%s, Conf=%.2f",
		root.Module, root.Brief, root.ID, correlatedIDs, impactLevelOf(len(correlatedIDs), root.Severity), confidence)

	return model.RCAEvent{
		RootLogID:         root.ID,
		RootModule:        root.Module,
		RootBrief:         root.Brief,
		RootTimestamp:     root.Timestamp.Format("2006-01-02 15:04:05"),
		RootCauseSummary:  summary,
		CorrelatedLogIDs:  string(corrJSON),
		ImpactEventsJSON:  string(impactJSON),
		ImpactLevel:       impactLevelOf(len(correlatedIDs), root.Severity),
		Confidence:        confidence,
		RecommendedAction: rule.ActionTemplate,
	}, true
}

// impactLevelOf 计算影响等级。
//
// RCA-16: 旧实现只产出 CRITICAL / HIGH，而 model.RCAEvent 注释声明了 4 档，
// 前端的 MEDIUM / LOW 筛选器永远为空。
func impactLevelOf(correlatedCount, rootSeverity int) string {
	switch {
	case correlatedCount >= 3 || rootSeverity <= 2:
		return "CRITICAL"
	case correlatedCount >= 2:
		return "HIGH"
	case correlatedCount == 1 && rootSeverity <= 4:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

// calculateConfidence 多因子置信度 (RCA-09)。
//
// 分项：
//   - 基础分 0.45；
//   - 传播深度贡献：min(depth,3) * 0.08（封顶 0.24）——链条越长说明传播模型越完整；
//   - 证据条数贡献：min(n,6) * 0.03（封顶 0.18）——对数式饱和，避免"凑条数"刷分；
//   - 时间衰减：0.15 * exp(-t/τ)，半衰期 150s——首条衍生事件拖得越久，因果越可疑；
//   - 实例一致性：一致 +0.08，单侧缺失 -0.10，无信号 0。
//
// 最终封顶 0.98、下限 0.05，并保留两位小数。
func calculateConfidence(maxDepth, correlatedCount int, firstDelay time.Duration, entityOK, entitySignal bool) float64 {
	score := confidenceBase

	depthBonus := float64(maxDepth) * confidencePerDepth
	if depthBonus > confidenceMaxDepthBonus {
		depthBonus = confidenceMaxDepthBonus
	}
	score += depthBonus

	evidenceBonus := float64(correlatedCount) * confidencePerEvidence
	if evidenceBonus > confidenceMaxEvidence {
		evidenceBonus = confidenceMaxEvidence
	}
	score += evidenceBonus

	if firstDelay >= 0 {
		seconds := firstDelay.Seconds()
		if seconds < 0 {
			seconds = 0
		}
		decay := math.Exp(-seconds * math.Ln2 / confidenceHalfLifeSec)
		score += confidenceTimeBonus * decay
	}

	if entitySignal {
		if entityOK {
			score += confidenceEntityBonus
		} else {
			score -= confidenceEntityPenalty
		}
	}

	if score > confidenceCap {
		score = confidenceCap
	}
	if score < confidenceFloor {
		score = confidenceFloor
	}
	return math.Round(score*100) / 100
}
