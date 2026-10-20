package task

import (
	"encoding/json"
	"strings"

	"gorm.io/gorm"

	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
	"logauditorgo/internal/summary"
	"logauditorgo/pkg/logger"
)

// deviceContext 批内设备缓存：同一 Hostname 在一个 Committer 生命周期内只查/建一次。
type deviceContext struct {
	id      uint
	product string
	version string
}

// assignDevicesPerHostname 修复"单个日志文件包含多台设备"时的归属错误。
//
// 背景（规划 F14）：导入时只按文件采样出一个 Hostname 建设备，整个文件都写同一个
// device_id，导致另一台设备的日志全部错挂（实测 10 条日志两设备 → 10/0）。
//
// 约束：
//   - 仅对"自动归属"模式生效（bundle.deviceID == 0）；用户显式选择设备的导入
//     （ImportLogsToDevice）必须保持整文件归属，不能违背显式意图；
//   - 主 Hostname 与空 Hostname 记录保持 worker 已算出的结果，避免无谓重匹配；
//   - 设备创建失败只降级为未归属并留痕，绝不阻断导入。
func (s *Service) assignDevicesPerHostname(
	taskDB *gorm.DB,
	bundle *fileBundle,
	records []model.LogRecord,
	cache map[string]deviceContext,
) {
	if s == nil || bundle == nil || len(records) == 0 || bundle.explicitDevice {
		return
	}

	for i := range records {
		host := strings.TrimSpace(records[i].Hostname)
		if host == "" || strings.EqualFold(host, bundle.primaryHost) {
			continue
		}

		ctx, ok := cache[host]
		if !ok {
			inferred := matcher.ResolveSpecificModelOrFamily(host)
			dev, err := getOrCreateDevice(taskDB, bundle.taskID, host, host, inferred)
			if err != nil {
				logger.Log.Errorf("[Task Service] per-host device resolve failed for %q: %v", host, err)
				// 负缓存：同一坏 Hostname 不重复打库
				cache[host] = deviceContext{id: bundle.deviceID}
				continue
			}
			product := normalizeDeviceProduct(dev.DeviceType)
			if product == "" {
				product = inferred
			}
			if product == "" {
				product = matcher.NeutralProductName
			}
			ctx = deviceContext{
				id:      dev.ID,
				product: product,
				version: strings.TrimSpace(dev.DeviceVersion),
			}
			cache[host] = ctx
		}
		if ctx.id == 0 {
			continue
		}

		records[i].DeviceID = ctx.id
		s.rematchRecordWithContext(&records[i], ctx.product, ctx.version)
	}
}

// rematchRecordWithContext 用指定设备上下文重跑知识匹配。
//
// 与 parseLogLine 的匹配段保持同一语义：匹配引擎选优 → 文档 ID 同源复算 →
// 模板参数捕获并合并进 ParametersJSON。仅对跨 Hostname 的少部分记录执行。
func (s *Service) rematchRecordWithContext(rec *model.LogRecord, product, version string) {
	if s == nil || s.matchEngine == nil || rec == nil {
		return
	}

	params := map[string]string{}
	if strings.TrimSpace(rec.ParametersJSON) != "" {
		_ = json.Unmarshal([]byte(rec.ParametersJSON), &params)
	}

	norm := &model.NormalizedLog{
		RawLog:      rec.RawLog,
		Timestamp:   rec.Timestamp,
		Hostname:    rec.Hostname,
		Module:      rec.Module,
		Severity:    rec.Severity,
		Brief:       rec.Brief,
		SourceFile:  rec.SourceFile,
		MessageBody: rec.MessageBody,
		Parameters:  params,
	}

	k, tier, conf := s.matchEngine.Match(norm, product, version)
	if k == nil || k.ID == 0 {
		rec.KnowledgeID = 0
		rec.MatchTier = matcher.TierUnmatch
		rec.MatchConfidence = 0
		rec.MatchedDocumentID = 0
		return
	}

	rec.KnowledgeID = k.ID
	rec.MatchTier = tier
	rec.MatchConfidence = conf
	rec.MatchedDocumentID = matcher.BestDocumentIDFor(k, product, version)

	if k.Message != "" {
		captured := summary.CaptureTemplateParams(k.Message, rec.MessageBody)
		if len(captured) > 0 {
			merged := summary.MergeCapturedParameters(params, captured, matcher.IsWirelessLog(rec.MessageBody))
			if b, err := json.Marshal(merged); err == nil {
				rec.ParametersJSON = string(b)
			}
		}
	}
}
