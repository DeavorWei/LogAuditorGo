//go:build !windows

package fsx

// listVolumes 类 Unix 系统以根目录为唯一起点
func listVolumes() []Root {
	return []Root{{Name: "根目录", Path: "/"}}
}
