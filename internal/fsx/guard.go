package fsx

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SecurePathGuard 导入类接口的路径安全守卫 (ARCH-02)。
//
// 背景：documents/import-dir、tasks、tasks/:id/import 三个接口都接受
// 任意服务端绝对路径。由于全局 CORS 允许 localhost 来源，任意网页都可以在
// 用户不知情的情况下让服务端读取本地任意文件并入库，再经知识库检索接口读出，
// 构成完整的本地文件泄露链。
//
// 设计：三个入口统一收敛到这一个守卫，避免"三处各写一遍、漏一处即失效"。
// 白名单为空时保持既有行为（不限制），以便本地单机工具开箱可用；
// 一旦配置了 roots，任何跳出这些根目录的路径都会被拒绝。
type SecurePathGuard struct {
	roots []string // 已规范化为绝对路径的根目录白名单
}

// NewSecurePathGuard 依据白名单根目录创建守卫。
// roots 为空表示不启用限制（保留向后兼容行为）。
func NewSecurePathGuard(roots []string) *SecurePathGuard {
	cleaned := make([]string, 0, len(roots))
	for _, r := range roots {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		abs, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		cleaned = append(cleaned, filepath.Clean(abs))
	}
	return &SecurePathGuard{roots: cleaned}
}

// Enabled 返回是否启用了白名单限制
func (g *SecurePathGuard) Enabled() bool {
	return g != nil && len(g.roots) > 0
}

// Roots 返回白名单根目录副本
func (g *SecurePathGuard) Roots() []string {
	if g == nil {
		return nil
	}
	out := make([]string, len(g.roots))
	copy(out, g.roots)
	return out
}

// Validate 校验单个路径是否落在白名单内。
// 白名单未启用时永远返回 nil（保持既有行为）。
func (g *SecurePathGuard) Validate(path string) error {
	if !g.Enabled() {
		return nil
	}
	target, err := Normalize(path)
	if err != nil {
		return fmt.Errorf("invalid path %q: %w", path, err)
	}
	for _, root := range g.roots {
		if isWithin(root, target) {
			return nil
		}
	}
	return fmt.Errorf("path %q is outside the allowed roots %v; check 'storage.allowed_roots' in your config", target, g.roots)
}

// ValidateAll 批量校验路径，返回全部非法路径的错误描述。
// 与 Validate 不同，它会一次性收集所有问题，便于前端一次性展示。
func (g *SecurePathGuard) ValidateAll(paths []string) error {
	if !g.Enabled() {
		return nil
	}
	var rejected []string
	for _, p := range paths {
		if err := g.Validate(p); err != nil {
			rejected = append(rejected, p)
		}
	}
	if len(rejected) == 0 {
		return nil
	}
	shown := rejected
	if len(shown) > 5 {
		shown = shown[:5]
	}
	return fmt.Errorf("以下 %d 个路径不在允许的根目录白名单内: %s (共 %d 个被拒绝)",
		len(rejected), strings.Join(shown, ", "), len(rejected))
}

// isWithin 判定 target 是否位于 root 内部（或就是 root 本身）。
// 使用 filepath.Rel 而不是字符串前缀比较，避免 /data 误判 /database 这类同前缀目录。
func isWithin(root, target string) bool {
	if root == "" || target == "" {
		return false
	}
	// Windows 上盘符大小写不敏感，统一按小写比较以保证行为一致
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	// 跳出根目录的 Rel 结果一定以 ".." 开头或是绝对路径
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	if filepath.IsAbs(rel) {
		return false
	}
	return true
}
