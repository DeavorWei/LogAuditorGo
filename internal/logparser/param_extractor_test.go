package logparser_test

import (
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
			// 且空值被整条丢弃，无法表达"该字段为空"这一审计事实。
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
