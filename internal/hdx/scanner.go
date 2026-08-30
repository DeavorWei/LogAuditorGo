package hdx

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"logauditorgo/internal/fsx"
	"logauditorgo/pkg/logger"
)

// ScannedHDXItem 表示扫描到的一个 HDX 文档目标（压缩包或解压目录）
type ScannedHDXItem struct {
	Type           string `json:"type"` // "archive" 或 "directory"
	Path           string `json:"path"` // 绝对路径
	Name           string `json:"name"` // 显示文件名或目录名
	Size           int64  `json:"size"` // 文件大小（字节）
	LibID          string `json:"lib_id,omitempty"`
	LibVersion     string `json:"lib_version,omitempty"`
	LibName        string `json:"lib_name,omitempty"`
	ProductType    string `json:"product_type,omitempty"`
	ProductVersion string `json:"product_version,omitempty"`
	IssueDate      string `json:"issue_date,omitempty"`
	TopicNumber    int    `json:"topic_number,omitempty"`
	ExistsInKB     bool   `json:"exists_in_kb"`
	Error          string `json:"error,omitempty"`
}

// ScanResult 扫描得到的聚合结果
type ScanResult struct {
	ScannedPath    string           `json:"scanned_path"`
	TotalCount     int              `json:"total_count"`
	ArchiveCount   int              `json:"archive_count"`
	DirectoryCount int              `json:"directory_count"`
	Items          []ScannedHDXItem `json:"items"`
}

// ScanHDXDirectory 递归扫描指定目录下的所有 HDX 官方压缩包与解压文档目录（包含 profile.xml）
func ScanHDXDirectory(rootDir string) (*ScanResult, error) {
	return ScanHDXPaths([]string{rootDir})
}

// ScanHDXPaths 扫描一个或多个路径（目录或压缩包）
func ScanHDXPaths(paths []string) (*ScanResult, error) {
	res := &ScanResult{
		Items: make([]ScannedHDXItem, 0),
	}
	if len(paths) == 0 {
		return res, nil
	}

	seenPaths := make(map[string]bool)

	for _, p := range paths {
		clean, err := fsx.Normalize(strings.TrimSpace(p))
		if err != nil {
			continue
		}
		if res.ScannedPath == "" {
			res.ScannedPath = clean
		}

		stat, err := os.Stat(fsx.LongPathSafe(clean))
		if err != nil {
			logger.Log.Warnf("[HDX Scanner] Path not accessible: %s: %v", clean, err)
			continue
		}

		if !stat.IsDir() {
			// 单个文件（如直接传入 .hdx / .zip）
			if isArchiveExtension(clean) {
				if !seenPaths[clean] {
					seenPaths[clean] = true
					item := scanArchiveFile(clean, stat.Size())
					res.Items = append(res.Items, item)
				}
			}
			continue
		}

		// 目录情况
		// 1. 如果该目录本身直接包含 profile.xml，视为主文档目录
		if hasProfileXML(clean) {
			if !seenPaths[clean] {
				seenPaths[clean] = true
				item := scanDocDirectory(clean)
				res.Items = append(res.Items, item)
			}
			continue
		}

		// 2. 遍历目录，寻找子解压文档目录与压缩包
		err = filepath.WalkDir(clean, func(entryPath string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				logger.Log.Warnf("[HDX Scanner] Error walking at %s: %v", entryPath, walkErr)
				return nil
			}
			if entryPath == clean {
				return nil
			}

			if d.IsDir() {
				// 命中 HDX 根目录（包含 profile.xml）
				if hasProfileXML(entryPath) {
					if !seenPaths[entryPath] {
						seenPaths[entryPath] = true
						item := scanDocDirectory(entryPath)
						res.Items = append(res.Items, item)
					}
					// 命中文档根后无需继续深入内部资源目录（如 resources/）
					return filepath.SkipDir
				}
				return nil
			}

			// 普通文件
			if isArchiveExtension(entryPath) {
				if !seenPaths[entryPath] {
					seenPaths[entryPath] = true
					info, err := d.Info()
					var size int64 = -1
					if err == nil {
						size = info.Size()
					}
					item := scanArchiveFile(entryPath, size)
					// 如果是 .zip 文件但并未包含 profile.xml，则忽略该普通 zip
					if strings.ToLower(filepath.Ext(entryPath)) == ".zip" && item.LibID == "" && item.Error != "" {
						return nil
					}
					res.Items = append(res.Items, item)
				}
			}
			return nil
		})

		if err != nil {
			logger.Log.Warnf("[HDX Scanner] Walk dir %s encountered error: %v", clean, err)
		}
	}

	for _, item := range res.Items {
		res.TotalCount++
		if item.Type == "archive" {
			res.ArchiveCount++
		} else {
			res.DirectoryCount++
		}
	}

	return res, nil
}

// isArchiveExtension 检查扩展名是否为 .hdx 或 .zip
func isArchiveExtension(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	return ext == ".hdx" || ext == ".zip"
}

// scanDocDirectory 解析已解压的文档目录
func scanDocDirectory(dirPath string) ScannedHDXItem {
	item := ScannedHDXItem{
		Type: "directory",
		Path: dirPath,
		Name: filepath.Base(dirPath),
		Size: calculateDirSize(dirPath),
	}

	doc, _, err := ParseProfileXML(dirPath)
	if err != nil {
		item.Error = err.Error()
		return item
	}

	item.LibID = doc.LibID
	item.LibVersion = doc.LibVersion
	item.LibName = doc.LibName
	item.ProductType = doc.ProductType
	item.ProductVersion = doc.ProductVersion
	item.IssueDate = doc.IssueDate
	item.TopicNumber = doc.TopicNumber
	return item
}

// scanArchiveFile 流式探测压缩包并提取 profile.xml 元信息
func scanArchiveFile(filePath string, size int64) ScannedHDXItem {
	item := ScannedHDXItem{
		Type: "archive",
		Path: filePath,
		Name: filepath.Base(filePath),
		Size: size,
	}

	arc, err := OpenArchive(filePath)
	if err != nil {
		item.Error = fmt.Sprintf("open archive failed: %v", err)
		return item
	}
	defer arc.Close()

	roots := arc.DocRoots(nil)
	if len(roots) == 0 {
		item.Error = "no profile.xml found in archive"
		return item
	}

	// 从第一个文档根读取 profile.xml
	doc, _, err := ParseProfileXMLFrom(roots[0])
	if err != nil {
		item.Error = fmt.Sprintf("parse profile.xml failed: %v", err)
		return item
	}

	item.LibID = doc.LibID
	item.LibVersion = doc.LibVersion
	item.LibName = doc.LibName
	item.ProductType = doc.ProductType
	item.ProductVersion = doc.ProductVersion
	item.IssueDate = doc.IssueDate
	item.TopicNumber = doc.TopicNumber
	return item
}

// calculateDirSize 快速计算目录体积
func calculateDirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}
