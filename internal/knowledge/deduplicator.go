package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"

	"logauditorgo/internal/model"
)

// 内容哈希分隔符。
//
// KB-08: 原实现用单个 `|` 作分隔符且无任何转义，
// 当字段值本身包含 `|` 时（华为文档里并不罕见），
// 不同的字段组合可以算出同一个哈希——存在理论碰撞风险。
// 这里改用**长度前缀编码** `len:value`，任何字符都不会产生歧义。
const hashFieldSeparator = ":"

// CalculateContentHash 计算知识条目的唯一内容哈希指纹。
//
// 全字段哈希用于判断"内容是否发生过变化"，是知识条目的完整快照。
func CalculateContentHash(k *model.Knowledge) string {
	if k == nil {
		return ""
	}
	h := sha256.New()
	writeField(h, string(k.EntryType))
	writeField(h, k.Module)
	writeField(h, fmt.Sprintf("%d", k.Severity))
	writeField(h, k.Brief)
	writeField(h, k.TrapOID)
	writeField(h, k.AlarmID)
	writeField(h, k.Message)
	writeField(h, k.Description)
	writeField(h, k.Impact)
	writeField(h, k.Cause)
	writeField(h, k.Action)
	writeField(h, k.Parameters)
	writeField(h, k.MIBName)
	writeField(h, k.AlarmType)
	return hex.EncodeToString(h.Sum(nil))
}

// CalculateStableKey 计算知识条目的**稳定键**（判重依据）。
//
// KB-08: 原实现直接用"全字段哈希"做判重，存在两个方向的问题：
//  1. 过松：Action / Description 差一个字就视为新知识，
//     同一条日志在不同版本文档里会被反复插入，knowledges 表随版本线性膨胀；
//  2. 过紧：Parameters / MIBName / AlarmType 完全没纳入指纹，
//     参数不同、告警类型不同的两条会被误判为同一条而丢弃。
//
// 现在拆成两级：
//   - 稳定键（本函数）：只由"标识性字段"构成，用于判重；
//   - 全字段哈希（CalculateContentHash）：用于判断内容是否变更。
//
// 分隔符统一采用长度前缀编码，杜绝跨字段碰撞。
func CalculateStableKey(k *model.Knowledge) string {
	if k == nil {
		return ""
	}
	h := sha256.New()
	writeField(h, string(k.EntryType))
	writeField(h, k.Module)
	writeField(h, k.Brief)
	writeField(h, k.TrapOID)
	writeField(h, k.AlarmID)
	// 不纳入 Severity：同一条日志在不同版本文档里的级别可能不同，
	// 用级别判重会让"同一条知识"被拆成多条。
	return hex.EncodeToString(h.Sum(nil))
}

// writeField 以 `len:value` 形式写入一个字段，避免分隔符歧义 (KB-08)
func writeField(h hash.Hash, value string) {
	trimmed := strings.TrimSpace(value)
	_, _ = h.Write([]byte(fmt.Sprintf("%d", len(trimmed))))
	_, _ = h.Write([]byte(hashFieldSeparator))
	_, _ = h.Write([]byte(trimmed))
}
