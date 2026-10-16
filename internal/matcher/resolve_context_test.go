package matcher_test

import (
	"testing"

	"logauditorgo/internal/matcher"
)

// TestResolveSpecificModelOrFamily 守护"设备上下文决议"的第一步：
// 从主机名 / 设备名中优先提取具体型号（可命中 120/150 版本分档），
// 退化为规范产品族名，无法识别时返回空串。
func TestResolveSpecificModelOrFamily(t *testing.T) {
	cases := []struct {
		name string
		host string
		want string
	}{
		{"具体型号 AC", "AC6508", "AC6508"},
		{"具体型号大小写归一", "ac6508", "AC6508"},
		{"具体型号带主机名后缀", "AC6508-CORE", "AC6508"},
		{"具体型号 CE", "CE16800", "CE16800"},
		{"具体型号 Campus", "S12700", "S12700"},
		{"具体型号 HiSecEngine 带形态后缀", "USG6000F-FW", "USG6000F"},
		{"具体型号 NetEngine", "NE40E", "NE40E"},
		{"规范族名 WLAN", "WLAN", "WLAN"},
		{"规范族名 CloudEngine", "CloudEngine", "CloudEngine"},
		{"规范族名 Campus-Switch", "Campus-Switch", "Campus-Switch"},
		{"规范族名 HiSecEngine-USG", "HiSecEngine-USG", "HiSecEngine-USG"},
		{"规范族名 NetEngine", "NetEngine", "NetEngine"},
		{"无法识别的中性主机名", "CORE-SW-01", ""},
		{"空主机名", "", ""},
		{"纯空白", "   ", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matcher.ResolveSpecificModelOrFamily(tc.host); got != tc.want {
				t.Fatalf("ResolveSpecificModelOrFamily(%q) = %q, want %q", tc.host, got, tc.want)
			}
		})
	}
}

// TestResolveSpecificModelOrFamily_NeverNeutralFallback 契约守护：
// 无法识别时必须返回空串而**不是** Huawei-VRP。
//
// 该函数被 task.prepareBundles 与 task.resolveLineContext 用于区分
// "嗅探无果"（应继续走显式覆盖 / 中性兜底）与"已决议"（应直接采用）。
// 若此处提前兜底为中性值，ReanalyzeOptions 的显式覆盖分支将永远不可达。
func TestResolveSpecificModelOrFamily_NeverNeutralFallback(t *testing.T) {
	got := matcher.ResolveSpecificModelOrFamily("UNKNOWN-HOST-XY")
	if got == matcher.NeutralProductName {
		t.Fatalf("must not pre-emptively fall back to %q", got)
	}
	if got != "" {
		t.Fatalf("unrecognized hostname must resolve to empty string, got %q", got)
	}
}
