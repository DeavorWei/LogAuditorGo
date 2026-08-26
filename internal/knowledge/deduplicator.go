package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"logauditorgo/internal/model"
)

// CalculateContentHash 计算知识条目的唯一内容哈希指纹
func CalculateContentHash(k *model.Knowledge) string {
	if k == nil {
		return ""
	}
	h := sha256.New()
	sep := []byte("|")
	h.Write([]byte(strings.TrimSpace(string(k.EntryType))))
	h.Write(sep)
	h.Write([]byte(strings.TrimSpace(k.Module)))
	h.Write(sep)
	h.Write([]byte(strings.TrimSpace(k.Brief)))
	h.Write(sep)
	h.Write([]byte(strings.TrimSpace(k.Message)))
	h.Write(sep)
	h.Write([]byte(strings.TrimSpace(k.Cause)))
	h.Write(sep)
	h.Write([]byte(strings.TrimSpace(k.Action)))

	return hex.EncodeToString(h.Sum(nil))
}
