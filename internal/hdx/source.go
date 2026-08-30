package hdx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"logauditorgo/internal/fsx"
	"logauditorgo/pkg/logger"
	"logauditorgo/pkg/progress"
)

// DocSource 抽象一个 HDX 文档包的读取来源：已解压目录，或 ZIP 压缩包内的虚拟目录。
//
// 设计要点（KB-16）：
//
//  1. 实例与文档根一一绑定。Open(rel) 的入参 rel 严格是"相对该文档根"的正斜杠路径
//     （如 "profile.xml"、"resources/dc_bgp_auth.html"），由实现负责拼接内部前缀。
//     下游解析器因此完全无感：无论文档来自磁盘目录，还是来自压缩包二级子目录。
//
//  2. 返回的 size 仅用于预检，可能是 -1（元数据不可信）。真正的防线是
//     readSourceFile 里的 io.LimitReader，压缩包头声明的大小不可信时也不会撑爆内存。
//
//  3. Close 必须被调用。目录源是 no-op；压缩包源持有真实文件句柄，
//     Windows 下不释放会导致原 .hdx 无法被移动或重命名。
type DocSource interface {
	// ID 全局唯一标识，用于并发锁 key
	ID() string
	// Label 日志与进度展示用的短名
	Label() string
	// Origin 写入 model.Document.FilePath 的来源路径
	Origin() string
	// Root 文档根在来源内部的相对路径（目录源恒为 "."）
	Root() string
	// Kind 来源类型："dir" 或 "zip"
	Kind() string
	// Open 打开文档根内相对路径对应的文件
	Open(rel string) (io.ReadCloser, int64, error)
	// Close 释放底层句柄（目录源为 no-op，可安全重复调用）
	Close() error
}

// 来源类型常量
const (
	SourceKindDir = "dir"
	SourceKindZip = "zip"
)

// 嵌套压缩包的展开约束。
//
// 嵌套包会在扫描阶段被整包读入内存，因此必须像防御解压炸弹一样防御"包中包"：
// 一个 200MB 的外层包里塞 100 个 200MB 的子包，就能在不落盘的情况下耗尽内存。
const (
	// maxNestedArchiveDepth 嵌套压缩包的展开深度上限（外层为 1 层）
	maxNestedArchiveDepth = 2
	// maxNestedArchiveCount 单个导入批次允许展开的嵌套压缩包总数
	maxNestedArchiveCount = 8
	// maxNestedArchiveBytes 单个嵌套压缩包载入内存的体积上限 (256MB)
	maxNestedArchiveBytes = int64(256 << 20)
	// maxNestedTotalBytes 单个导入批次嵌套压缩包占用的内存总预算 (512MB)
	maxNestedTotalBytes = int64(512 << 20)
)

// normalizeRelPath 校验并清洗"文档根内部"的相对路径，返回正斜杠形式。
//
// 拒绝任何跳出文档根的请求（等价于解压链路里的 Zip Slip 防护），
// 同时统一 Windows 反斜杠与多余的 "./" 段。
func normalizeRelPath(rel string) (string, error) {
	u := strings.TrimSpace(rel)
	if u == "" {
		return "", fmt.Errorf("empty relative path")
	}
	u = strings.ReplaceAll(u, `\`, "/")
	if strings.HasPrefix(u, "/") {
		return "", fmt.Errorf("absolute path is not allowed: %s", rel)
	}
	// 盘符（C:/…）与协议前缀（http://…）都不可能出现在文档包内部
	if strings.Contains(u, ":") {
		return "", fmt.Errorf("illegal character ':' in relative path: %s", rel)
	}

	var segs []string
	for _, seg := range strings.Split(u, "/") {
		switch seg {
		case "", ".":
			continue
		case "..":
			if len(segs) == 0 {
				return "", fmt.Errorf("path escapes the document root: %s", rel)
			}
			segs = segs[:len(segs)-1]
		default:
			segs = append(segs, seg)
		}
	}
	if len(segs) == 0 {
		return "", fmt.Errorf("empty relative path after normalization: %s", rel)
	}
	return strings.Join(segs, "/"), nil
}

// readSourceFile 从 DocSource 读取文件内容，并强制施加字节上限。
//
// size 只用于快速预检（可能为 -1），真正的防线是 LimitReader：
// 即使压缩包中央目录声明的大小是伪造的，也绝不会多读一个字节上限之外的数据。
func readSourceFile(src DocSource, rel string, limit int64) ([]byte, error) {
	rc, size, err := src.Open(rel)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	if size > limit {
		return nil, fmt.Errorf("file %q is too large: %d bytes (limit %d)", rel, size, limit)
	}

	var buf bytes.Buffer
	written, err := io.Copy(&buf, io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %q failed: %w", rel, err)
	}
	if written > limit {
		return nil, fmt.Errorf("file %q exceeds limit %d bytes", rel, limit)
	}
	return buf.Bytes(), nil
}

// ---------------------------------------------------------------------------
// 目录源
// ---------------------------------------------------------------------------

// dirSource 已解压目录形态的文档来源（保持与传统导入链路完全一致的读盘行为）
type dirSource struct {
	root   string
	origin string
}

// NewDirSource 基于已解压的 HDX 文档目录构造来源
func NewDirSource(dir string) DocSource {
	clean := filepath.Clean(dir)
	return &dirSource{root: clean, origin: clean}
}

func (d *dirSource) ID() string { return SourceKindDir + ":" + d.root }

func (d *dirSource) Label() string { return filepath.Base(d.root) }

func (d *dirSource) Origin() string { return d.origin }

func (d *dirSource) Root() string { return "." }

func (d *dirSource) Kind() string { return SourceKindDir }

func (d *dirSource) Open(rel string) (io.ReadCloser, int64, error) {
	clean, err := normalizeRelPath(rel)
	if err != nil {
		return nil, 0, err
	}
	full := filepath.Join(d.root, filepath.FromSlash(clean))

	// 目录穿越二次校验：normalizeRelPath 已挡住 ".."，此处兜底绝对路径与符号链接逃逸
	root := filepath.Clean(d.root)
	relCheck, relErr := filepath.Rel(root, full)
	if relErr != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) || filepath.IsAbs(relCheck) {
		return nil, 0, fmt.Errorf("path %q escapes the document root %q", rel, root)
	}

	f, err := os.Open(fsx.LongPathSafe(full))
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	if info.IsDir() {
		_ = f.Close()
		return nil, 0, fmt.Errorf("path %q is a directory", rel)
	}
	return f, info.Size(), nil
}

func (d *dirSource) Close() error { return nil }

// ---------------------------------------------------------------------------
// 压缩包源
// ---------------------------------------------------------------------------

// archiveIndex 压缩包条目的内存索引。
//
// 只保存元数据指针，不读取任何条目内容，因此构建成本与"读取中央目录"同量级（毫秒级）。
type archiveIndex struct {
	// files 归一化小写相对路径 -> 条目
	files map[string]*zip.File
	// basename 小写文件名 -> 归一化相对路径列表，用于大小写/目录层级不一致时的兜底
	basename map[string][]string
}

// buildArchiveIndex 基于中央目录条目构建索引，跳过目录与非法（Zip Slip）条目
func buildArchiveIndex(entries []*zip.File, origin string) *archiveIndex {
	idx := &archiveIndex{
		files:    make(map[string]*zip.File, len(entries)),
		basename: make(map[string][]string),
	}
	for _, f := range entries {
		if f == nil || f.FileInfo().IsDir() {
			continue
		}
		rel, err := normalizeRelPath(f.Name)
		if err != nil {
			logger.Log.Warnf("[HDX Source] skip illegal entry %q in %s: %v", f.Name, origin, err)
			continue
		}
		key := strings.ToLower(rel)
		if _, dup := idx.files[key]; dup {
			// 同路径重复条目：保留首个，避免异常包覆盖正常内容
			continue
		}
		idx.files[key] = f

		base := strings.ToLower(path.Base(key))
		idx.basename[base] = append(idx.basename[base], key)
	}
	return idx
}

// lookup 按优先级查找条目：精确（小写）命中 -> basename 唯一候选兜底。
//
// 实测华为 HDX 的 item.URL 与实体文件绝大多数只在大小写上有差异，
// 全小写匹配即可覆盖；basename 兜底用于消化目录层级不一致的少数包。
// 多候选时放弃匹配，避免命中语义完全不同的同名文件。
func (idx *archiveIndex) lookup(rel string) (*zip.File, bool) {
	key := strings.ToLower(strings.TrimPrefix(rel, "./"))
	if f, ok := idx.files[key]; ok {
		return f, true
	}
	cands := idx.basename[strings.ToLower(path.Base(key))]
	if len(cands) == 1 {
		if f, ok := idx.files[cands[0]]; ok {
			logger.Log.Debugf("[HDX Source] entry %q resolved by basename fallback -> %s", rel, cands[0])
			return f, true
		}
	}
	return nil, false
}

// docRoots 返回包内所有包含 profile.xml 的虚拟目录（归一化相对路径）。
//
// 语义对齐 FindHDXDocDirs：命中文档根后不再深入其子目录。
func (idx *archiveIndex) docRoots() []string {
	var dirs []string
	for key := range idx.files {
		if strings.ToLower(path.Base(key)) != "profile.xml" {
			continue
		}
		d := path.Dir(key)
		if d == "" || d == "/" {
			d = "."
		}
		dirs = append(dirs, d)
	}
	if len(dirs) == 0 {
		return nil
	}
	sort.Strings(dirs)

	// 排序后祖先必然先于后代出现，只需与最近一个已确认的根比较即可剔除后代
	roots := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if len(roots) > 0 && isDescendantPath(d, roots[len(roots)-1]) {
			continue
		}
		roots = append(roots, d)
	}
	return roots
}

// nestedArchives 返回包内所有嵌套压缩包条目的归一化路径（稳定排序）
func (idx *archiveIndex) nestedArchives() []string {
	var keys []string
	for key := range idx.files {
		ext := strings.ToLower(path.Ext(key))
		if ext == ".zip" || ext == ".hdx" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// isDescendantPath 判断 child 是否位于 parent 之下（parent 为 "." 时除自身外均为真）
func isDescendantPath(child, parent string) bool {
	if child == parent {
		return false
	}
	if parent == "." {
		return true
	}
	return strings.HasPrefix(child, parent+"/")
}

// zipSource 压缩包内某个文档根形态的来源。
// 多个 zipSource 共享同一份 archiveIndex，但各自绑定不同的 docRoot。
type zipSource struct {
	idx     *archiveIndex
	docRoot string
	origin  string
	label   string
	// owner 持有真实文件句柄的顶层归档；纯内存子包为 nil
	owner *ArchiveSource
}

func (z *zipSource) ID() string { return SourceKindZip + ":" + z.origin + "::" + z.docRoot }

func (z *zipSource) Label() string { return z.label }

func (z *zipSource) Origin() string { return z.origin }

func (z *zipSource) Root() string { return z.docRoot }

func (z *zipSource) Kind() string { return SourceKindZip }

func (z *zipSource) Open(rel string) (io.ReadCloser, int64, error) {
	clean, err := normalizeRelPath(rel)
	if err != nil {
		return nil, 0, err
	}
	key := clean
	if z.docRoot != "" && z.docRoot != "." {
		key = z.docRoot + "/" + clean
	}

	f, ok := z.idx.lookup(key)
	if !ok {
		return nil, 0, fmt.Errorf("entry %q not found in archive %s", key, z.origin)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, 0, fmt.Errorf("open entry %q in %s failed: %w", key, z.origin, err)
	}

	// 元数据不可信或明显越界时返回 -1，交由调用方的 LimitReader 兜底
	size := int64(f.UncompressedSize64)
	if size < 0 || size > MaxUncompressedFileSize {
		size = -1
	}
	return rc, size, nil
}

func (z *zipSource) Close() error {
	if z.owner != nil {
		return z.owner.Close()
	}
	return nil
}

// ArchiveSource 持有一个磁盘上压缩包的句柄，以及包内条目的内存索引。
//
// 生命周期严格与一个导入批次绑定：必须由 prepareImportTargets 返回的 cleanup
// 关闭，且 cleanup 需要通过 defer 兜底，保证正常完成、报错、panic 三种路径
// 都能第一时间释放句柄（Windows 下否则会锁住原 .hdx 文件）。
type ArchiveSource struct {
	path string
	rc   *zip.ReadCloser
	idx  *archiveIndex

	mu     sync.Mutex
	closed bool
}

// OpenArchive 打开磁盘上的 HDX/ZIP 压缩包并构建内存索引，全程不向磁盘写入任何文件。
//
// 打开时会基于中央目录元数据执行解压炸弹预算校验，异常包在此处即被拒绝。
func OpenArchive(filePath string) (*ArchiveSource, error) {
	rc, err := zip.OpenReader(fsx.LongPathSafe(filePath))
	if err != nil {
		return nil, fmt.Errorf("open archive %s failed: %w", filePath, err)
	}

	files := make([]*zip.File, 0, len(rc.File))
	for _, f := range rc.File {
		if f == nil || f.FileInfo().IsDir() {
			continue
		}
		files = append(files, f)
	}
	// 解压炸弹防护：与全量解压链路共用同一套阈值，行为保持一致
	if err := checkArchiveBudget(files); err != nil {
		_ = rc.Close()
		return nil, fmt.Errorf("archive rejected: %w", err)
	}

	return &ArchiveSource{
		path: filePath,
		rc:   rc,
		idx:  buildArchiveIndex(rc.File, filePath),
	}, nil
}

// Path 返回压缩包的绝对路径
func (a *ArchiveSource) Path() string { return a.path }

// EntryCount 返回索引内的有效条目数
func (a *ArchiveSource) EntryCount() int { return len(a.idx.files) }

// Close 释放底层文件句柄，可安全重复调用
func (a *ArchiveSource) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	if a.rc != nil {
		return a.rc.Close()
	}
	return nil
}

// DocRoots 发现压缩包内所有 HDX 文档根，并递归展开包内嵌套的压缩包。
//
// 嵌套包在这里（扫描阶段、主协程）被整包读入内存并构建索引，
// Worker 协程只负责并发读取已就绪的 zip.File，避免每个 Worker 重复解包。
//
// 返回的 DocSource 全部共享本 ArchiveSource 的句柄，
// 调用方必须在导入结束后调用一次 ArchiveSource.Close()（或任一 DocSource.Close()）。
func (a *ArchiveSource) DocRoots(tr *progress.JobTracker) []DocSource {
	budget := &nestedBudget{}
	out := make([]DocSource, 0, 1)
	a.collectDocRoots(a, a.path, 1, budget, &out, tr)
	return out
}

// nestedBudget 单次扫描的嵌套展开配额
type nestedBudget struct {
	count int
	bytes int64
}

// collectDocRoots 递归收集文档根：先收集本层，再展开嵌套压缩包
func (a *ArchiveSource) collectDocRoots(owner *ArchiveSource, originPath string, depth int,
	budget *nestedBudget, out *[]DocSource, tr *progress.JobTracker) {

	for _, root := range a.idx.docRoots() {
		origin := originPath
		label := filepath.Base(a.path)
		if root != "." {
			origin = originPath + "::" + root
			label = label + "/" + root
		}
		*out = append(*out, &zipSource{
			idx:     a.idx,
			docRoot: root,
			origin:  origin,
			label:   label,
			owner:   owner,
		})
	}

	if depth >= maxNestedArchiveDepth {
		return
	}

	for _, key := range a.idx.nestedArchives() {
		if budget.count >= maxNestedArchiveCount {
			if tr != nil {
				tr.AddLog("warning", "嵌套压缩包数量已达上限 %d，剩余子包不再展开", maxNestedArchiveCount)
			}
			logger.Log.Warnf("[HDX Source] nested archive limit %d reached in %s, skipping remaining", maxNestedArchiveCount, a.path)
			return
		}

		f := a.idx.files[key]
		size := int64(f.UncompressedSize64)
		if size > maxNestedArchiveBytes {
			logger.Log.Warnf("[HDX Source] nested archive %s is too large (%d bytes), skipped", key, size)
			if tr != nil {
				tr.AddLog("warning", "嵌套压缩包 %s 体积超限，已跳过", path.Base(key))
			}
			continue
		}
		if budget.bytes+size > maxNestedTotalBytes {
			logger.Log.Warnf("[HDX Source] nested archive memory budget exhausted, skipping %s", key)
			if tr != nil {
				tr.AddLog("warning", "嵌套压缩包内存预算已耗尽，剩余子包不再展开")
			}
			continue
		}

		data, err := readEntryBytes(f, maxNestedArchiveBytes)
		if err != nil {
			logger.Log.Warnf("[HDX Source] read nested archive %s failed: %v", key, err)
			if tr != nil {
				tr.AddLog("warning", "读取嵌套压缩包 %s 失败: %v", path.Base(key), err)
			}
			continue
		}

		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			logger.Log.Warnf("[HDX Source] open nested archive %s failed: %v", key, err)
			if tr != nil {
				tr.AddLog("warning", "解析嵌套压缩包 %s 失败: %v", path.Base(key), err)
			}
			continue
		}

		budget.count++
		budget.bytes += int64(len(data))
		if tr != nil {
			tr.AddLog("info", "已展开嵌套压缩包 %s (%.1f MB), 采用内存流式读取，不落盘",
				path.Base(key), float64(len(data))/(1024*1024))
		}

		child := &ArchiveSource{
			path: a.path + "::" + key,
			idx:  buildArchiveIndex(zr.File, key),
		}
		child.collectDocRoots(owner, child.path, depth+1, budget, out, tr)
	}
}

// readEntryBytes 把单个压缩包条目完整读入内存（带硬上限）
func readEntryBytes(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	var buf bytes.Buffer
	written, err := io.Copy(&buf, io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if written > limit {
		return nil, fmt.Errorf("entry %s exceeds limit %d bytes", f.Name, limit)
	}
	return buf.Bytes(), nil
}
