package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"logauditorgo/internal/model"
)

// CalculateContentHash 计算知识条目的唯一内容哈希指纹
func CalculateContentHash(k *model.Knowledge) string {
	if k == nil {
		return ""
	}
	raw := fmt.Sprintf("%s|%s|%s|%s|%s|%s",
		strings.TrimSpace(string(k.EntryType)),
		strings.TrimSpace(k.Module),
		strings.TrimSpace(k.Brief),
		strings.TrimSpace(k.Message),
		strings.TrimSpace(k.Cause),
		strings.TrimSpace(k.Action),
	)
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:])
}
