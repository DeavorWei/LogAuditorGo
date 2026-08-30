package hdx

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"logauditorgo/internal/model"
	"logauditorgo/pkg/logger"
)

type ProfileXML struct {
	XMLName        xml.Name `xml:"profile"`
	LibID          string   `xml:"libId"`
	LibVersion     string   `xml:"libVersion"`
	LibName        string   `xml:"libName"`
	ProductType    string   `xml:"productType"`
	ProductVersion string   `xml:"productVersion"`
	IssueDate      string   `xml:"issueDate"`
	Language       string   `xml:"language"`
	TopicNumber    int      `xml:"topicNumber"`
	Navi           string   `xml:"navi"`
}

// maxProfileFileBytes profile.xml 的体积上限 (8MB)。
// 正常 HDX 的 profile.xml 只有几 KB，设限只为防御畸形包导致的一次性大内存分配。
const maxProfileFileBytes = 8 << 20

// ParseProfileXML 从指定根目录解析 profile.xml（目录形态，兼容传统导入链路）
func ParseProfileXML(docRootDir string) (*model.Document, string, error) {
	return ParseProfileXMLFrom(NewDirSource(docRootDir))
}

// ParseProfileXMLFrom 从任意 DocSource 解析 profile.xml。
//
// doc.FilePath 记录来源（已解压目录为目录绝对路径，压缩包为原始 .hdx 绝对路径），
// 不再指向随导入结束即失效的临时解压目录。
func ParseProfileXMLFrom(src DocSource) (*model.Document, string, error) {
	data, err := readSourceFile(src, "profile.xml", maxProfileFileBytes)
	if err != nil {
		return nil, "", fmt.Errorf("read profile.xml failed: %w", err)
	}

	var p ProfileXML
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.CharsetReader = XMLCharsetReader
	if err := decoder.Decode(&p); err != nil {
		return nil, "", fmt.Errorf("unmarshal profile.xml failed: %w", err)
	}

	naviRelPath := strings.TrimSpace(p.Navi)
	if naviRelPath == "" {
		naviRelPath = "resources/navi.xml"
	}

	doc := &model.Document{
		LibID:          strings.TrimSpace(p.LibID),
		LibVersion:     strings.TrimSpace(p.LibVersion),
		LibName:        strings.TrimSpace(p.LibName),
		ProductType:    strings.TrimSpace(p.ProductType),
		ProductVersion: strings.TrimSpace(p.ProductVersion),
		IssueDate:      strings.TrimSpace(p.IssueDate),
		Language:       strings.TrimSpace(p.Language),
		TopicNumber:    p.TopicNumber,
		FilePath:       src.Origin(),
		ImportedAt:     time.Now(),
	}

	logger.Log.Debugf("[HDX Extractor] Successfully parsed profile.xml [%s]: LibID=%s, Version=%s, Product=%s %s, Topics=%d, Navi=%s",
		src.Label(), doc.LibID, doc.LibVersion, doc.ProductType, doc.ProductVersion, doc.TopicNumber, naviRelPath)

	return doc, naviRelPath, nil
}

// FindHDXDocDirs 递归查找指定目录下所有包含 profile.xml 的 HDX 文档目录
func FindHDXDocDirs(rootDir string) ([]string, error) {
	cleanRoot := filepath.Clean(rootDir)
	stat, err := os.Stat(cleanRoot)
	if err != nil {
		return nil, fmt.Errorf("stat directory failed: %w", err)
	}
	if !stat.IsDir() {
		return nil, fmt.Errorf("path is not a directory: %s", cleanRoot)
	}

	// 1. 如果当前目录本身直接包含 profile.xml，直接返回该目录
	if hasProfileXML(cleanRoot) {
		return []string{cleanRoot}, nil
	}

	// 2. 遍历子目录寻找包含 profile.xml 的文档目录
	var docDirs []string
	err = filepath.WalkDir(cleanRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			logger.Log.Warnf("walk dir error at %s: %v", path, err)
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path == cleanRoot {
			return nil
		}

		if hasProfileXML(path) {
			docDirs = append(docDirs, path)
			// 命中 HDX 根目录后无需继续深入遍历其子目录（如 resources/）
			return filepath.SkipDir
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walk directory failed: %w", err)
	}

	if len(docDirs) == 0 {
		return nil, fmt.Errorf("no valid HDX document directory (containing profile.xml) found in: %s", rootDir)
	}

	return docDirs, nil
}

// hasProfileXML 检测目录下是否存在 profile.xml 文件（大小写兼容）
func hasProfileXML(dir string) bool {
	profilePath := filepath.Join(dir, "profile.xml")
	if fi, err := os.Stat(profilePath); err == nil && !fi.IsDir() {
		return true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(entry.Name(), "profile.xml") {
			return true
		}
	}
	return false
}

