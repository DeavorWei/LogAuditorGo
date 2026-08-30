package hdx

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"logauditorgo/pkg/logger"
	"logauditorgo/pkg/progress"
)

// UnzipConcurrent 采用自适应多协程 Worker Pool 并发解压 ZIP/HDX 压缩包
// 包含严格的 Zip Slip 路径穿越安全防护，并支持向 JobTracker 上报细粒度实时进度
func UnzipConcurrent(src string, dest string, tracker *progress.JobTracker) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("open zip file %s failed: %w", src, err)
	}
	defer r.Close()

	cleanDest := filepath.Clean(dest)
	if err := os.MkdirAll(cleanDest, 0755); err != nil {
		return fmt.Errorf("create dest dir %s failed: %w", dest, err)
	}

	cleanDestPrefix := cleanDest + string(os.PathSeparator)

	// 1. 预处理：Zip Slip 路径安全校验、收集文件列表并预建目录
	var filesToExtract []*zip.File
	dirSet := make(map[string]struct{})

	for _, f := range r.File {
		targetPath := filepath.Join(cleanDest, f.Name)
		cleanTarget := filepath.Clean(targetPath)

		// Zip Slip 安全检查：确保目标路径位于目标目录内部
		if !strings.HasPrefix(cleanTarget+string(os.PathSeparator), cleanDestPrefix) && cleanTarget != cleanDest {
			return fmt.Errorf("illegal file path in zip (Zip Slip detected): %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			dirSet[cleanTarget] = struct{}{}
		} else {
			dirSet[filepath.Dir(cleanTarget)] = struct{}{}
			filesToExtract = append(filesToExtract, f)
		}
	}

	// 批量预先创建所有子目录，避免并发写文件时的目录争用与重复系统调用
	for dirPath := range dirSet {
		if err := os.MkdirAll(dirPath, 0755); err != nil {
			return fmt.Errorf("create subdirectory %s failed: %w", dirPath, err)
		}
	}

	totalFiles := int64(len(filesToExtract))
	if totalFiles == 0 {
		return nil
	}

	// 2. 解压前基于元数据做总量校验，拦截解压炸弹 (KB-03)。
	// 必须在真正写盘之前执行，否则恶意/异常压缩包已把磁盘写满才被发现。
	if err := checkArchiveBudget(filesToExtract); err != nil {
		return fmt.Errorf("archive rejected: %w", err)
	}

	// 3. 自适应计算 Worker 协程数量
	workerNum := runtime.NumCPU() * 2
	if workerNum < 4 {
		workerNum = 4
	} else if workerNum > 32 {
		workerNum = 32
	}
	if int64(workerNum) > totalFiles {
		workerNum = int(totalFiles)
	}

	jobs := make(chan *zip.File, totalFiles)
	for _, f := range filesToExtract {
		jobs <- f
	}
	close(jobs)

	var doneCount int64
	var firstErr error
	var errOnce sync.Once
	var hasError atomic.Bool

	srcBaseName := filepath.Base(src)
	if tracker != nil {
		tracker.UpdateProgress(0, totalFiles, fmt.Sprintf("正在并发解压 %s: 0 / %d", srcBaseName, totalFiles))
	}

	var wg sync.WaitGroup
	for i := 0; i < workerNum; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					errOnce.Do(func() {
						firstErr = fmt.Errorf("panic in unzip worker: %v", r)
						hasError.Store(true)
					})
				}
			}()

			for f := range jobs {
				if hasError.Load() {
					return
				}

				targetPath := filepath.Join(cleanDest, f.Name)
				if err := extractSingleZipFile(f, targetPath); err != nil {
					logger.Log.Warnf("[HDX Unzip] Extract %s failed: %v", f.Name, err)
					errOnce.Do(func() {
						firstErr = fmt.Errorf("extract file %s failed: %w", f.Name, err)
						hasError.Store(true)
					})
					return
				}

				cur := atomic.AddInt64(&doneCount, 1)
				if tracker != nil && (cur%50 == 0 || cur == totalFiles) {
					tracker.UpdateProgress(cur, totalFiles, fmt.Sprintf("正在并发解压 %s: %d / %d", srcBaseName, cur, totalFiles))
				}
			}
		}()
	}

	wg.Wait()

	if firstErr != nil {
		return firstErr
	}

	return nil
}

// MaxUncompressedFileSize 单文件解压最大尺寸上限 (2GB)
const MaxUncompressedFileSize = int64(2 * 1024 * 1024 * 1024)

// 解压炸弹（Zip Bomb）防护阈值 (KB-03)。
//
// 此前只校验"单文件 2GB"，对压缩包内的**条目总数、解压后总字节、压缩比**毫无约束：
// 一个含 1 万个 1.9GB 条目的压缩包就能写出约 19TB 数据，瞬间打满磁盘。
// 本工具虽是本地单机、输入来自用户本人，但导入源可能是他人提供的文档包，仍应设防。
const (
	// MaxArchiveTotalUncompressed 单个压缩包解压后的总字节上限 (20GB)
	MaxArchiveTotalUncompressed = int64(20 * 1024 * 1024 * 1024)
	// MaxArchiveFileCount 单个压缩包内的文件条目数上限
	MaxArchiveFileCount = 200000
	// MaxArchiveCompressionRatio 单个条目的最大压缩比，超过即判定为疑似解压炸弹
	MaxArchiveCompressionRatio = 1000
)

// checkArchiveBudget 在解压前基于压缩包元数据做总量校验，拦截解压炸弹
func checkArchiveBudget(files []*zip.File) error {
	if len(files) > MaxArchiveFileCount {
		return fmt.Errorf("archive contains %d entries, exceeds limit of %d", len(files), MaxArchiveFileCount)
	}

	var total uint64
	for _, f := range files {
		if f.UncompressedSize64 > uint64(MaxUncompressedFileSize) {
			return fmt.Errorf("entry '%s' uncompressed size %d bytes exceeds single-file limit", f.Name, f.UncompressedSize64)
		}
		// 压缩比 = 解压后大小 / 压缩后大小；压缩后为 0 时跳过（目录/空文件）
		if f.CompressedSize64 > 0 {
			ratio := f.UncompressedSize64 / f.CompressedSize64
			if ratio > MaxArchiveCompressionRatio {
				return fmt.Errorf("entry '%s' compression ratio %d:1 exceeds limit %d:1, suspected zip bomb",
					f.Name, ratio, MaxArchiveCompressionRatio)
			}
		}
		total += f.UncompressedSize64
		if total > uint64(MaxArchiveTotalUncompressed) {
			return fmt.Errorf("archive total uncompressed size exceeds limit (max %d bytes)", MaxArchiveTotalUncompressed)
		}
	}
	return nil
}

// extractSingleZipFile 解压单个文件到目标路径
func extractSingleZipFile(f *zip.File, targetPath string) error {
	// 校验未压缩尺寸元数据 (防御 Zip Bomb)
	if f.UncompressedSize64 > uint64(MaxUncompressedFileSize) {
		return fmt.Errorf("file '%s' uncompressed size (%d bytes) exceeds 2GB limit", f.Name, f.UncompressedSize64)
	}

	// 确保父目录存在（容错）
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return err
	}

	mode := f.Mode()
	if mode == 0 {
		mode = 0644
	}

	outFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer outFile.Close()

	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	// 使用 LimitReader 严格防止实际解压流超过 2GB
	limitedReader := io.LimitReader(rc, MaxUncompressedFileSize+1)
	written, err := io.Copy(outFile, limitedReader)
	if err != nil {
		_ = os.Remove(targetPath)
		return err
	}
	if written > MaxUncompressedFileSize {
		_ = os.Remove(targetPath)
		return fmt.Errorf("file '%s' decompressed data exceeds 2GB limit", f.Name)
	}

	return nil
}

// ExtractSingleZipFileForTest 供单元测试安全校验单文件 2GB 解压阈值拦截
func ExtractSingleZipFileForTest(f *zip.File, targetPath string) error {
	return extractSingleZipFile(f, targetPath)
}

// ExtractAllArchivesWithTracker 递归查找并多协程并发解压目录下所有的 .hdx / .zip 压缩包
func ExtractAllArchivesWithTracker(dir string, tracker *progress.JobTracker) error {
	var archiveFiles []string
	walkErr := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d == nil || d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext == ".hdx" || ext == ".zip" {
			archiveFiles = append(archiveFiles, p)
		}
		return nil
	})
	if walkErr != nil {
		return fmt.Errorf("walk directory %s failed: %w", dir, walkErr)
	}

	if len(archiveFiles) == 0 {
		return nil
	}

	if tracker != nil {
		tracker.AddLog("info", "发现 %d 个 HDX 官方压缩包，准备多协程并发解压...", len(archiveFiles))
	}

	var lastErr error
	successCount := 0

	for idx, archivePath := range archiveFiles {
		baseName := filepath.Base(archivePath)
		destDir := filepath.Join(filepath.Dir(archivePath), "extracted_"+strings.TrimSuffix(baseName, filepath.Ext(baseName)))

		if tracker != nil {
			tracker.SetStage("UPLOAD", fmt.Sprintf("正在并发解压第 %d/%d 个知识包: %s", idx+1, len(archiveFiles), baseName))
			tracker.AddLog("info", "开始并发解压: %s", baseName)
		}

		if err := UnzipConcurrent(archivePath, destDir, tracker); err != nil {
			lastErr = err
			logger.Log.Warnf("[HDX Extractor] Failed to unzip archive %s: %v", archivePath, err)
			if tracker != nil {
				tracker.AddLog("warning", "解压 %s 失败: %v", baseName, err)
			}
			continue
		}

		successCount++
		// 解压成功后清理压缩包以释放临时空间
		_ = os.Remove(archivePath)
	}

	if successCount == 0 && lastErr != nil {
		return lastErr
	}

	if tracker != nil {
		tracker.AddLog("info", "所有 HDX 压缩包并发解压完成")
	}

	return nil
}
