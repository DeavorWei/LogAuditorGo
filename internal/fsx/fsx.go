// Package fsx 提供服务端本地文件系统的只读浏览与文件收集能力。
//
// 设计目标：让前端只传递"路径字符串"，由服务端进程直接读取本地磁盘，
// 避免超大目录（数十万文件、数 GB）经由浏览器上传导致的页面卡死。
//
// 安全约束：
//   - 本包只提供只读能力，不包含任何写入/删除/移动操作；
//   - 所有对外暴露的路径均为 filepath.Clean 后的绝对路径；
//   - 仅在需要执行 IO 时通过 longPathSafe 处理 Windows 长路径。
package fsx

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// DefaultLimit 单次目录浏览默认返回条数
	DefaultLimit = 500
	// MaxLimit 单次目录浏览硬上限，防止超大目录一次性返回拖垮前端
	MaxLimit = 2000
	// DefaultMaxFiles 目录展开为文件列表时的默认上限
	DefaultMaxFiles = 50000
	// MaxCollectFiles 目录展开为文件列表时的硬上限
	MaxCollectFiles = 200000
)

// Entry 文件系统的一个条目（目录或文件）
type Entry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	IsDir     bool   `json:"is_dir"`
	IsSymlink bool   `json:"is_symlink,omitempty"`
	Size      int64  `json:"size"`
	ModTime   string `json:"mod_time,omitempty"`
	Readable  bool   `json:"readable"`
	Exists    bool   `json:"exists"`
}

// Root 一个可浏览的根目录入口
type Root struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// RootsResult 根目录与常用快捷入口
type RootsResult struct {
	Roots     []Root `json:"roots"`
	Shortcuts []Root `json:"shortcuts"`
}

// BrowseOptions 目录浏览参数
type BrowseOptions struct {
	Path     string
	Exts     []string
	Keyword  string
	DirsOnly bool
	Offset   int
	Limit    int
}

// BrowseResult 目录浏览结果（分页返回）
type BrowseResult struct {
	Path      string  `json:"path"`
	Parent    string  `json:"parent"`
	Entries   []Entry `json:"entries"`
	Total     int     `json:"total"`
	Offset    int     `json:"offset"`
	Limit     int     `json:"limit"`
	Truncated bool    `json:"truncated"`
}

// winsLongPathThreshold Windows 传统 MAX_PATH 限制
const winsLongPathThreshold = 248

// longPathSafe 为超长路径添加 \\?\ 前缀，使 Windows 下可访问超过 MAX_PATH 的路径。
// 仅在执行真实 IO 时调用，对外返回的路径始终保持原始形态。
func longPathSafe(p string) string {
	if runtime.GOOS != "windows" {
		return p
	}
	if strings.HasPrefix(p, `\\?\`) {
		return p
	}
	if strings.HasPrefix(p, `\\`) {
		if len(p) > winsLongPathThreshold {
			return `\\?\UNC\` + strings.TrimPrefix(p, `\\`)
		}
		return p
	}
	if len(p) >= winsLongPathThreshold {
		return `\\?\` + p
	}
	return p
}

// Normalize 将任意输入路径规范化为干净的绝对路径
func Normalize(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path failed: %w", err)
	}
	return filepath.Clean(abs), nil
}

// hiddenNames 默认隐藏的系统/噪声目录（小写比较）
var hiddenNames = map[string]bool{
	"$recycle.bin":              true,
	"system volume information": true,
	"recovery":                  true,
	"$windows.~ws":              true,
	"$windows.~bt":              true,
	"windowsapps":               true,
	"perflogs":                  true,
	"node_modules":              true,
	".git":                      true,
	".svn":                      true,
	".hg":                       true,
}

// isHiddenName 判断目录名是否属于需要隐藏的系统/噪声目录
func isHiddenName(name string) bool {
	return hiddenNames[strings.ToLower(name)]
}

// matchExt 判断文件名是否命中扩展名白名单（为空表示不过滤）
func matchExt(name string, exts []string) bool {
	if len(exts) == 0 {
		return true
	}
	lower := strings.ToLower(name)
	for _, raw := range exts {
		ext := strings.ToLower(strings.TrimSpace(raw))
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// naturalLess 自然排序比较（数字按数值大小比较，例如 Log2 < Log10）
func naturalLess(a, b string) bool {
	ai, bi := 0, 0
	for ai < len(a) && bi < len(b) {
		ac, as := utf8.DecodeRuneInString(a[ai:])
		bc, bs := utf8.DecodeRuneInString(b[bi:])

		if isDigitByte(a[ai]) && isDigitByte(b[bi]) {
			aj, bj := ai, bi
			for aj < len(a) && isDigitByte(a[aj]) {
				aj++
			}
			for bj < len(b) && isDigitByte(b[bj]) {
				bj++
			}
			an, _ := strconv.ParseInt(a[ai:aj], 10, 64)
			bn, _ := strconv.ParseInt(b[bi:bj], 10, 64)
			if an != bn {
				return an < bn
			}
			if (aj - ai) != (bj - bi) {
				return (aj - ai) < (bj - bi)
			}
			ai, bi = aj, bj
			continue
		}

		la, lb := unicode.ToLower(ac), unicode.ToLower(bc)
		if la != lb {
			return la < lb
		}
		ai += as
		bi += bs
	}
	return len(a) < len(b)
}

// LongPathSafe 对外暴露的长路径安全化：为超长路径添加 \\?\ 前缀（仅 Windows 生效），
// 供其他包在执行 IO 前处理可能超过 MAX_PATH 的路径。
func LongPathSafe(p string) string {
	return longPathSafe(p)
}

func isDigitByte(b byte) bool {
	return b >= '0' && b <= '9'
}

// dirReadable 检测目录是否可读（可打开即视为可读）
func dirReadable(path string) bool {
	f, err := os.Open(longPathSafe(path))
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// Roots 返回可浏览的根目录列表，extraShortcuts 为调用方附加的快捷入口
func Roots(extraShortcuts ...Root) RootsResult {
	roots := listVolumes()

	var shortcuts []Root
	seen := make(map[string]bool)
	for _, r := range roots {
		seen[strings.ToLower(r.Path)] = true
	}

	appendShortcut := func(name, path string) {
		if path == "" {
			return
		}
		clean, err := Normalize(path)
		if err != nil {
			return
		}
		key := strings.ToLower(clean)
		if seen[key] {
			return
		}
		if _, err := os.Stat(longPathSafe(clean)); err != nil {
			return
		}
		seen[key] = true
		shortcuts = append(shortcuts, Root{Name: name, Path: clean})
	}

	if home, err := os.UserHomeDir(); err == nil {
		appendShortcut("用户主目录", home)
	}
	if wd, err := os.Getwd(); err == nil {
		appendShortcut("程序运行目录", wd)
	}
	for _, s := range extraShortcuts {
		appendShortcut(s.Name, s.Path)
	}

	return RootsResult{Roots: roots, Shortcuts: shortcuts}
}

// newDirEntry 构造目录条目
func newDirEntry(path string, d os.DirEntry) Entry {
	name := d.Name()
	info, err := d.Info()
	e := Entry{
		Name:     name,
		Path:     path,
		IsDir:    true,
		Readable: true,
		Exists:   true,
	}
	if err == nil {
		e.Size = info.Size()
		e.ModTime = info.ModTime().Format("2006-01-02 15:04:05")
		if info.Mode()&os.ModeSymlink != 0 {
			e.IsSymlink = true
		}
	}
	return e
}

// newFileEntry 构造文件条目
func newFileEntry(path string, info os.FileInfo) Entry {
	e := Entry{
		Name:     info.Name(),
		Path:     path,
		IsDir:    false,
		Size:     info.Size(),
		ModTime:  info.ModTime().Format("2006-01-02 15:04:05"),
		Readable: true,
		Exists:   true,
	}
	if info.Mode()&os.ModeSymlink != 0 {
		e.IsSymlink = true
	}
	return e
}

// Browse 分页浏览单个目录
func Browse(opts BrowseOptions) (*BrowseResult, error) {
	root, err := Normalize(opts.Path)
	if err != nil {
		return nil, err
	}
	if !dirReadable(root) {
		return nil, fmt.Errorf("directory is not accessible: %s", root)
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}

	des, err := os.ReadDir(longPathSafe(root))
	if err != nil {
		return nil, fmt.Errorf("read directory failed: %w", err)
	}

	var dirs, files []Entry
	keyword := strings.ToLower(strings.TrimSpace(opts.Keyword))

	for _, de := range des {
		name := de.Name()
		if de.IsDir() && isHiddenName(name) {
			continue
		}
		if keyword != "" && !strings.Contains(strings.ToLower(name), keyword) {
			continue
		}

		full := filepath.Join(root, name)
		if de.IsDir() {
			dirs = append(dirs, newDirEntry(full, de))
			continue
		}
		if opts.DirsOnly {
			continue
		}
		if !matchExt(name, opts.Exts) {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		files = append(files, newFileEntry(full, info))
	}

	sort.SliceStable(dirs, func(i, j int) bool { return naturalLess(dirs[i].Name, dirs[j].Name) })
	sort.SliceStable(files, func(i, j int) bool { return naturalLess(files[i].Name, files[j].Name) })

	all := append(dirs, files...)
	total := len(all)

	end := offset + limit
	if end > total {
		end = total
	}
	page := []Entry{}
	if offset < total {
		page = all[offset:end]
		// 仅对当前页的目录做可读性探测，避免超大目录全量探测带来的开销
		for i := range page {
			if page[i].IsDir {
				page[i].Readable = dirReadable(page[i].Path)
			}
		}
	}

	parent := filepath.Dir(root)
	if parent == root {
		parent = ""
	}

	return &BrowseResult{
		Path:      root,
		Parent:    parent,
		Entries:   page,
		Total:     total,
		Offset:    offset,
		Limit:     limit,
		Truncated: end < total,
	}, nil
}

// Stat 批量校验路径是否存在及其类型、大小
func Stat(paths []string) []Entry {
	result := make([]Entry, 0, len(paths))
	for _, raw := range paths {
		clean, err := Normalize(raw)
		if err != nil {
			result = append(result, Entry{Name: filepath.Base(raw), Path: raw, Readable: false, Exists: false})
			continue
		}
		info, err := os.Stat(longPathSafe(clean))
		if err != nil {
			result = append(result, Entry{Name: filepath.Base(clean), Path: clean, Readable: false, Exists: false})
			continue
		}
		result = append(result, Entry{
			Name:     info.Name(),
			Path:     clean,
			IsDir:    info.IsDir(),
			Size:     info.Size(),
			ModTime:  info.ModTime().Format("2006-01-02 15:04:05"),
			Readable: true,
			Exists:   true,
		})
	}
	return result
}

// CollectFiles 收集路径列表下的所有匹配文件（供导入管线复用）
// 返回值为文件条目、扫描过的目录数、以及是否因达到上限而被截断。
func CollectFiles(paths []string, recursive bool, exts []string, maxFiles int) ([]Entry, int, bool) {
	if maxFiles <= 0 {
		maxFiles = DefaultMaxFiles
	}
	if maxFiles > MaxCollectFiles {
		maxFiles = MaxCollectFiles
	}

	var files []Entry
	dirsScanned := 0
	truncated := false

	appendFile := func(path string, info os.FileInfo) bool {
		if !matchExt(info.Name(), exts) {
			return false
		}
		files = append(files, newFileEntry(path, info))
		return len(files) >= maxFiles
	}

	for _, raw := range paths {
		if truncated {
			break
		}
		clean, err := Normalize(raw)
		if err != nil {
			continue
		}
		info, err := os.Stat(longPathSafe(clean))
		if err != nil {
			continue
		}

		if !info.IsDir() {
			if appendFile(clean, info) {
				truncated = true
			}
			continue
		}

		dirsScanned++
		if !recursive {
			des, err := os.ReadDir(longPathSafe(clean))
			if err != nil {
				continue
			}
			for _, de := range des {
				if de.IsDir() {
					continue
				}
				fi, err := de.Info()
				if err != nil {
					continue
				}
				if appendFile(filepath.Join(clean, de.Name()), fi) {
					truncated = true
					break
				}
			}
			continue
		}

		// 递归遍历，命中上限后通过 SkipAll 提前终止
		stop := false
		err = filepath.WalkDir(longPathSafe(clean), func(p string, d os.DirEntry, err error) error {
			if stop {
				return filepath.SkipAll
			}
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if isHiddenName(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			// WalkDir 传入的是 longPathSafe 后的根路径，需还原为干净路径
			cleanPath := cleanPathFrom(p, clean)
			if appendFile(cleanPath, fi) {
				stop = true
				return filepath.SkipAll
			}
			return nil
		})
		if stop {
			truncated = true
		}
		_ = err
	}

	return files, dirsScanned, truncated
}

// cleanPathFrom 将 WalkDir 回调中的路径还原为不含 \\?\ 前缀的干净路径
func cleanPathFrom(walked, rootPrefix string) string {
	p := walked
	if strings.HasPrefix(p, `\\?\UNC\`) {
		p = `\\` + strings.TrimPrefix(p, `\\?\UNC\`)
	} else if strings.HasPrefix(p, `\\?\`) {
		p = strings.TrimPrefix(p, `\\?\`)
	}
	return p
}
