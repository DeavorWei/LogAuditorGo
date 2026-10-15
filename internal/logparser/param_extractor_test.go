package logparser_test

import (
	"strings"
	"testing"

	"logauditorgo/internal/logparser"
)

// TestExtractParametersEdgeCases 覆盖 PARSE-08 / PARSE-18 中列出的全部参数抽取缺陷。
// 每一个用例都对应一条"修复前必然失败"的行为，作为防回归守卫。
func TestExtractParametersEdgeCases(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  map[string]string
	}{
		{
			// PARSE-08: `Key=` 后紧跟空格或下一组键时，旧实现会把下一组键吞成自己的值，
			// 且空值被整条丢弃。现实现不吞下一组键，且保留括号结构化块中的空值表达字段为空的审计事实。
			name:  "empty value must not swallow next pair",
			input: "Session terminated. (Reason= Code=5)",
			want:  map[string]string{"Reason": "", "Code": "5"},
		},
		{
			// PARSE-08: 括号外的值遇空格即被截断（旧实现只剩 "Port"）。
			name:  "unquoted value with spaces outside brackets",
			input: "Link flapping. Description=Port to core, Reason=Code=5",
			want:  map[string]string{"Description": "Port to core", "Reason": "Code=5"},
		},
		{
			// PARSE-18: 括号包裹的值在全局分支被逗号截成 "(1"。
			name:  "parenthesized value keeps commas",
			input: "Location=(1,2), Port=GE1/0/1",
			want:  map[string]string{"Location": "1,2", "Port": "GE1/0/1"},
		},
		{
			// PARSE-18: 键名字符类过窄，中文键名（端口名）匹配不到。
			name:  "chinese key names",
			input: "接口状态变化 (端口名=GE1/0/1, 原因=光模块故障)",
			want:  map[string]string{"端口名": "GE1/0/1", "原因": "光模块故障"},
		},
		{
			name:  "quoted and bracketed values",
			input: `Failed. (PeerID=192.168.1.2, ReturnCode=[3], Iface="GE1/0/1")`,
			want:  map[string]string{"PeerID": "192.168.1.2", "ReturnCode": "3", "Iface": "GE1/0/1"},
		},
		{
			// PARSE-18: 块内同名键不同取值应合并为 JSON 数组，而不是静默覆盖。
			name:  "duplicate keys merge into json array",
			input: "(Code=1) and (Code=2)",
			want:  map[string]string{"Code": `["1","2"]`},
		},
		{
			name:  "no parameters yields empty map",
			input: "BGP session authentication failed with no parameters.",
			want:  map[string]string{},
		},
		{
			// P1 D1: 复合键吸收正例
			name:  "composite keys absorption in parenthesis",
			input: "The CPU usage exceeds the threshold value. (forwarding type=1, slot id=0, CPU id=0, current CPU usage=97, threshold=90)",
			want: map[string]string{
				"forwarding type":   "1",
				"slot id":           "0",
				"CPU id":            "0",
				"current CPU usage": "97",
				"threshold":         "90",
			},
		},
		{
			// P1 D2: 连字符切分正例
			name:  "hyphen separated key value pairs",
			input: "CID=0x0-alarmID=0x00f103b4-clearType=service_resume;",
			want: map[string]string{
				"CID":       "0x0",
				"alarmID":   "0x00f103b4",
				"clearType": "service_resume",
			},
		},
		{
			// P1 反例守卫：英文叙述句防误吞为复合键
			name:  "narrative english sentences must not be swallowed as keys",
			input: "The CPU usage on SPU 0 CPU 0 is suddenly changed from 98% to 1%, and the change value is 97%, exceeding threshold value 40%.",
			want:  map[string]string{},
		},
		{
			// P1 反例守卫：正常含连字符的值不误切
			name:  "values containing hyphens like UUID, date, trunk must not be cut",
			input: "uuid=123e4567-e89b-12d3-a456-426614174000, date=2026-10-09, port=Eth-Trunk1, lacp=LACP 1-2",
			want: map[string]string{
				"uuid": "123e4567-e89b-12d3-a456-426614174000",
				"date": "2026-10-09",
				"port": "Eth-Trunk1",
				"lacp": "LACP 1-2",
			},
		},
		{
			// Audit B3 守卫 1: Trunk1-2 紧随空格与合法键，不能在连字符处误切
			name:  "trunk with dash numbers must not be cut at hyphen",
			input: "ifname=Eth-Trunk1-2 slot=3",
			want: map[string]string{
				"ifname": "Eth-Trunk1-2",
				"slot":   "3",
			},
		},
		{
			// Audit B3 守卫 2: 负数 -3dBm 紧随空格与合法键，负号不能被吃成空值
			name:  "negative values like dBm must not swallow negative sign",
			input: "rate=-3dBm level=ok",
			want: map[string]string{
				"rate":  "-3dBm",
				"level": "ok",
			},
		},
		{
			// Audit B3 守卫 3: 路径中的连字符后跟普通词汇，不误切
			name:  "path containing hyphens followed by space and key",
			input: "path=/var/log/app-error code=1",
			want: map[string]string{
				"path": "/var/log/app-error",
				"code": "1",
			},
		},
		{
			// Audit B3 守卫 4: 带有普通复合短语的值不误切
			name:  "message with dash and normal space words",
			input: "msg=error-see log=details",
			want: map[string]string{
				"msg": "error-see",
				"log": "details",
			},
		},
		{
			// Audit N4: 数字开头的单键（如 1st=, 2nd=）在单键快速路径下正常保留，空格正常收口不吞后续键
			name:  "numeric prefix identifier is kept as single key without swallowing",
			input: "a=1 1st=abc b=2",
			want: map[string]string{
				"a":   "1",
				"1st": "abc",
				"b":   "2",
			},
		},
		{
			// Audit N4: 槽位与数字前缀单键
			name:  "slot and numeric prefix second key",
			input: "slot=0 2nd=up",
			want: map[string]string{
				"slot": "0",
				"2nd":  "up",
			},
		},
		{
			// Audit N4: 下划线前缀单键正常保留
			name:  "underscore prefix single key is preserved",
			input: "_id=5 slot=1",
			want: map[string]string{
				"_id":  "5",
				"slot": "1",
			},
		},
		{
			// Audit B8: 连续空值键过滤，不生成空字符串键值对污染卡片
			name:  "empty value pairs are filtered",
			input: "a= b=1 x= y=2",
			want: map[string]string{
				"b": "1",
				"y": "2",
			},
		},
		{
			// Audit B14: 分号分隔符覆盖
			name:  "semicolon separated key value pairs",
			input: "CID=0x0;alarmID=0x1;state=up",
			want: map[string]string{
				"CID":     "0x0",
				"alarmID": "0x1",
				"state":   "up",
			},
		},
		{
			// Audit B14: 混合逗号、分号与空格复合键
			name:  "mixed delimiters with commas and semicolons",
			input: "Slot=0, CPU=0; Drop count=100; Extra=none",
			want: map[string]string{
				"Slot":       "0",
				"CPU":        "0",
				"Drop count": "100",
				"Extra":      "none",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := logparser.ExtractParameters(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("param count mismatch: got %v, want %v", got, tc.want)
			}
			for k, wantV := range tc.want {
				gotV, ok := got[k]
				if !ok {
					t.Fatalf("missing key %q: got %v", k, got)
				}
				if gotV != wantV {
					t.Fatalf("key %q: got %q, want %q", k, gotV, wantV)
				}
			}
		})
	}
}

// TestExtractParameters_HuaweiAC_AP_Samples 验证华为 AC/AP 离线报文 27 字段全量提取与纯冒号无等号报文解析 (§5.1)
func TestExtractParameters_HuaweiAC_AP_Samples(t *testing.T) {
	input := `[WLAN_STA_INFO_OFFLINE]ACMAC:f4-e5-f2-70-f4-bc;ACNAME:AC6508;APMAC:8c-e5-ef-80-b3-20;APNAME:26F-AP2-cae;SSID:btet_lan;RADIOID:1;USER:c858c0b8b299;MAC:c8-58-c0-b8-b2-99;IPADDRESS:10.101.34.48;IPV6ADDRESS:-;TIME:1784858824;ZONE:UTC+0800;DAYLIGHT:false;SESSIONTIME:1118;ERRCODE:216;RESULT:STA disassociation;USERGROUP:NULL;AUTHENPLACE:None;EXTENDINFO:The signal strength of the STA is -51 dbm.;CIB ID:11533;INTERFACE:Wlan-Dbss17648;ACCESS TYPE:None;RDSIP:-;Portal TYPE:-;AUTHID=3480934416;AUTHPROTOCOL:-;`
	params := logparser.ExtractParameters(input)

	// 1. 验证字段总数精确为 27
	if len(params) != 27 {
		t.Fatalf("expected 27 parameters, got %d: %v", len(params), params)
	}

	// 2. 验证空格复合键与标签成功提取
	if params["CIB ID"] != "11533" || params["ACCESS TYPE"] != "None" {
		t.Errorf("composite key failed: %v", params)
	}
	if _, ok := params["WLAN_STA_INFO_OFFLINE"]; !ok {
		t.Errorf("leading tag parameter missing")
	}

	// 3. 严格禁止冒号脏键
	for k := range params {
		if strings.Contains(k, ":") {
			t.Errorf("illegal dirty key containing colon: %q", k)
		}
	}

	// 4. 纯冒号无等号报文验证
	pureColon := `[WLAN_STA_INFO_AUTHENTICATION]ACMAC:f4-e5-f2-70-f4-bc;USER:sta01;RESULT:success;`
	pureParams := logparser.ExtractParameters(pureColon)
	if pureParams["USER"] != "sta01" || pureParams["RESULT"] != "success" {
		t.Fatalf("pure colon log failed: %v", pureParams)
	}
}

// TestExtractParameters_RegressionGuards 验证存量回归守卫：段内逗号二次切分、引号保护与散文括号补漏 (§5.2)
func TestExtractParameters_RegressionGuards(t *testing.T) {
	// Audit B14 守卫：段内逗号二次切分，Slot=0 与 CPU=0 必须同时提取
	b14Input := "Slot=0, CPU=0; Drop count=100; Extra=none"
	b14Got := logparser.ExtractParameters(b14Input)
	if b14Got["Slot"] != "0" || b14Got["CPU"] != "0" || b14Got["Drop count"] != "100" {
		t.Fatalf("Audit B14 regression: got %v", b14Got)
	}

	// 引号保护守卫：引号内部的分号不被硬切断
	quoteInput := `NAME:test;EXTENDINFO:"signal=-51;noise=-90";STATUS:ok;`
	qGot := logparser.ExtractParameters(quoteInput)
	if qGot["EXTENDINFO"] != "signal=-51;noise=-90" || qGot["STATUS"] != "ok" {
		t.Fatalf("quote protection failed: got %v", qGot)
	}

	// 散文冒号日志补漏：括号参数绝不丢失
	prose := "State: Down; Reason: Timeout (PeerIP=10.0.0.1, AS=65000)"
	pGot := logparser.ExtractParameters(prose)
	if pGot["PeerIP"] != "10.0.0.1" || pGot["AS"] != "65000" {
		t.Fatalf("prose bracket parameters dropped: got %v", pGot)
	}
}
