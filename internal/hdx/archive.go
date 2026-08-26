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

	// 2. 自适应计算 Worker 协程数量
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

// extractSingleZipFile 解压单个文件到目标路径
func extractSingleZipFile(f *zip.File, targetPath string) error {
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

	if _, err := io.Copy(outFile, rc); err != nil {
		return err
	}

	return nil
}

// ExtractAllArchivesWithTracker 递归查找并多协程并发解压目录下所有的 .hdx / .zip 压缩包
func ExtractAllArchivesWithTracker(dir string, tracker *progress.JobTracker) error {
	var archiveFiles []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext == ".hdx" || ext == ".zip" {
			archiveFiles = append(archiveFiles, p)
		}
		return nil
	})

	if len(archiveFiles) == 0 {
		return nil
	}

	if tracker != nil {
		tracker.AddLog("info", "发现 %d 个 HDX 官方压缩包，准备多协程并发解压...", len(archiveFiles))
	}

	for idx, archivePath := range archiveFiles {
		baseName := filepath.Base(archivePath)
		destDir := filepath.Join(filepath.Dir(archivePath), "extracted_"+strings.TrimSuffix(baseName, filepath.Ext(baseName)))

		if tracker != nil {
			tracker.SetStage("UPLOAD", fmt.Sprintf("正在并发解压第 %d/%d 个知识包: %s", idx+1, len(archiveFiles), baseName))
			tracker.AddLog("info", "开始并发解压: %s", baseName)
		}

		if err := UnzipConcurrent(archivePath, destDir, tracker); err != nil {
			logger.Log.Warnf("[HDX Extractor] Failed to unzip archive %s: %v", archivePath, err)
			if tracker != nil {
				tracker.AddLog("warning", "解压 %s 失败: %v", baseName, err)
			}
			continue
		}

		// 解压成功后清理压缩包以释放临时空间
		_ = os.Remove(archivePath)
	}

	if tracker != nil {
		tracker.AddLog("info", "所有 HDX 压缩包并发解压完成")
	}

	return nil
}
