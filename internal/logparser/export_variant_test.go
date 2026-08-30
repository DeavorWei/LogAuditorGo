package logparser

// NewVRPParserForTest 暴露 VRP 解析器实例，供外部测试包验证 Support 判定语义
func NewVRPParserForTest() *VRPParser {
	return &VRPParser{}
}
