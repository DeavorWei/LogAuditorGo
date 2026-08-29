//go:build windows

package fsx

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// listVolumes 枚举 Windows 逻辑驱动器。
// 优先使用 GetLogicalDrives 位掩码避免探测不存在的盘符（访问未就绪的软驱/光驱
// 可能显著变慢甚至触发系统错误弹窗），再对每个命中的盘符做一次 Stat 校验。
func listVolumes() []Root {
	mask, err := windows.GetLogicalDrives()
	if err != nil || mask == 0 {
		return probeVolumes()
	}

	var roots []Root
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		letter := string(rune('A'+i)) + `:\`
		if !volumeReady(letter) {
			continue
		}
		roots = append(roots, Root{Name: strings.TrimSuffix(letter, `\`), Path: letter})
	}
	if len(roots) == 0 {
		return probeVolumes()
	}
	return roots
}

// probeVolumes GetLogicalDrives 不可用时的兜底探测
func probeVolumes() []Root {
	var roots []Root
	for i := 0; i < 26; i++ {
		letter := string(rune('A'+i)) + `:\`
		if volumeReady(letter) {
			roots = append(roots, Root{Name: strings.TrimSuffix(letter, `\`), Path: letter})
		}
	}
	return roots
}

func volumeReady(letter string) bool {
	info, err := os.Stat(letter)
	return err == nil && info.IsDir()
}
