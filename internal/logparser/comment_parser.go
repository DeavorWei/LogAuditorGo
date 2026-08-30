package logparser

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"logauditorgo/internal/model"
)

var (
	// # This logfile is generated at slot 1 (CE6865E V200R024C00SPC500B126)
	// # Logfile is generated at slot 1
	headerRegex = regexp.MustCompile(`(?i)^#\s*(?:This\s+)?logfile\s+is\s+generated\s+at\s+slot\s+(\S+)(?:\s*\(([^)]+)\))?`)

	// # Digest(0006756365):3e0f5f595bfa263fff2638e6692bb42ce44af9c01af42a075add1073b287b917
	digestRegex = regexp.MustCompile(`(?i)^#\s*Digest(?:\(([^)]+)\))?:\s*([a-fA-F0-9]+|\S+)`)

	// # This logfile is closed at 2024-03-20 15:30:00
	closedRegex = regexp.MustCompile(`(?i)^#\s*(?:This\s+)?logfile\s+is\s+closed\s+at\s*(.*)`)

	// # Software Version: V200R024C00SPC500B126
	versionRegex = regexp.MustCompile(`(?i)^#\s*(?:Software\s+)?Version:\s*(.*)`)
)

// CommentParser 专门用于解析以 # 开头的设备注释与元数据行（如文件头、Digest校验、文件归档记录等）
type CommentParser struct{}

func (p *CommentParser) Name() string {
	return "Log-Comment-Parser"
}

func (p *CommentParser) Support(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "#")
}

func (p *CommentParser) Parse(line string) (*model.NormalizedLog, error) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return nil, fmt.Errorf("line does not start with #: %s", line)
	}

	norm := &model.NormalizedLog{
		RawLog:      line,
		MessageBody: trimmed,
		Module:      "COMMENT",
		DeviceType:  "Huawei-VRP",
		Severity:    6, // RFC 5424 Informational (信息性/注释性)
		Brief:       "COMMENT_LINE",
		Parameters:  make(map[string]string),
	}

	// 1. 匹配文件头生成信息
	if m := headerRegex.FindStringSubmatch(trimmed); m != nil {
		norm.Brief = "LOGFILE_HEADER"
		slot := m[1]
		norm.SlotInfo = slot
		norm.Parameters["Slot"] = slot
		norm.Parameters["FileType"] = "日志文件头"

		if len(m) > 2 && m[2] != "" {
			sub := strings.TrimSpace(m[2])
			parts := strings.Fields(sub)
			if len(parts) >= 2 {
				norm.Parameters["DeviceModel"] = parts[0]
				norm.Parameters["Version"] = parts[1]
			} else if len(parts) == 1 {
				norm.Parameters["DeviceModel"] = parts[0]
			}
		}
		return norm, nil
	}

	// 2. 匹配 Digest 防篡改哈希校验
	if m := digestRegex.FindStringSubmatch(trimmed); m != nil {
		norm.Brief = "LOGFILE_DIGEST"
		if len(m) > 1 && m[1] != "" {
			seqStr := strings.TrimSpace(m[1])
			norm.Parameters["DigestSeq"] = seqStr
			norm.SlotInfo = "Seq: " + seqStr
			if seq, err := strconv.ParseUint(seqStr, 10, 64); err == nil {
				norm.Sequence = seq
			}
		}
		if len(m) > 2 && m[2] != "" {
			norm.Parameters["Digest"] = strings.TrimSpace(m[2])
		}
		norm.Parameters["FileType"] = "防篡改校验码"
		return norm, nil
	}

	// 3. 匹配文件关闭/归档信息
	if m := closedRegex.FindStringSubmatch(trimmed); m != nil {
		norm.Brief = "LOGFILE_CLOSED"
		info := strings.TrimSpace(m[1])
		norm.Parameters["CloseInfo"] = info
		norm.Parameters["FileType"] = "日志文件尾/归档"
		// 尝试解析时间戳
		if t, err := ParseHuaweiTimestamp(info); err == nil {
			norm.Timestamp = t
		} else if t, err := time.Parse("2006-01-02 15:04:05", info); err == nil {
			norm.Timestamp = t
		}
		return norm, nil
	}

	// 4. 匹配软件版本说明
	if m := versionRegex.FindStringSubmatch(trimmed); m != nil {
		norm.Brief = "SOFTWARE_VERSION"
		norm.Parameters["Version"] = strings.TrimSpace(m[1])
		return norm, nil
	}

	// 5. 通用注释行
	commentContent := strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
	if commentContent != "" {
		norm.Parameters["Comment"] = commentContent
	}
	return norm, nil
}
