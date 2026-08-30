package summary

import (
	"fmt"
	"strings"
)

// BuildCommentSummary 为以 # 开头的注释性日志生成友好易懂的中文事件语义摘要
func BuildCommentSummary(brief string, params map[string]string, rawMsg string) string {
	switch strings.ToUpper(strings.TrimSpace(brief)) {
	case "LOGFILE_HEADER":
		slot := params["Slot"]
		model := params["DeviceModel"]
		ver := params["Version"]
		var details []string
		if slot != "" {
			details = append(details, fmt.Sprintf("槽位 %s", slot))
		}
		if model != "" {
			details = append(details, fmt.Sprintf("型号 %s", model))
		}
		if ver != "" {
			details = append(details, fmt.Sprintf("版本 %s", ver))
		}
		if len(details) > 0 {
			return fmt.Sprintf("【文件头注释】设备日志文件起始头（由 %s 生成）", strings.Join(details, ", "))
		}
		return "【文件头注释】设备日志文件起始标识元数据"

	case "LOGFILE_DIGEST":
		seq := params["DigestSeq"]
		digest := params["Digest"]
		displayDigest := digest
		if len(displayDigest) > 24 {
			displayDigest = displayDigest[:12] + "..." + displayDigest[len(displayDigest)-6:]
		}
		if seq != "" && displayDigest != "" {
			return fmt.Sprintf("【防篡改校验】日志文件完整性校验 Digest（序号: %s，哈希: %s）", seq, displayDigest)
		} else if displayDigest != "" {
			return fmt.Sprintf("【防篡改校验】日志文件完整性校验 Digest（哈希: %s）", displayDigest)
		}
		return "【防篡改校验】日志文件完整性校验摘要记录"

	case "LOGFILE_CLOSED":
		info := params["CloseInfo"]
		if info != "" {
			return fmt.Sprintf("【文件尾注释】设备日志文件归档关闭记录（%s）", info)
		}
		return "【文件尾注释】设备日志文件归档关闭记录"

	case "SOFTWARE_VERSION":
		ver := params["Version"]
		if ver != "" {
			return fmt.Sprintf("【系统版本注释】设备操作系统固件版本（%s）", ver)
		}
		return "【系统版本注释】设备操作系统软件版本说明"

	default:
		// 通用注释行
		comment := params["Comment"]
		if comment == "" {
			clean := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rawMsg), "#"))
			if clean != "" {
				comment = clean
			}
		}
		if comment != "" {
			runes := []rune(comment)
			if len(runes) > 60 {
				comment = string(runes[:60]) + "..."
			}
			return fmt.Sprintf("【系统注释】文件附加注释说明: %s", comment)
		}
		return "【系统注释】设备日志文件中的注释或元数据说明"
	}
}
