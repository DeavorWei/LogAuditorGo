package hdx_test

import (
	"os"
	"path/filepath"
	"testing"

	"logauditorgo/internal/hdx"
)

func TestScanHDXDirectory(t *testing.T) {
	parentDir := t.TempDir()

	// 1. 创建一个子解压目录（含 profile.xml）
	docDir1 := filepath.Join(parentDir, "unzipped_ce16800")
	if err := os.MkdirAll(filepath.Join(docDir1, "resources"), 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(docDir1, "profile.xml"), []byte(fixtureProfileXML), 0644); err != nil {
		t.Fatalf("write profile.xml failed: %v", err)
	}

	// 2. 创建一个 .hdx 压缩包
	hdxBytes := buildZipBytes(t, fixtureDoc())
	hdxFile := filepath.Join(parentDir, "Switch_S5700.hdx")
	if err := os.WriteFile(hdxFile, hdxBytes, 0644); err != nil {
		t.Fatalf("write hdx file failed: %v", err)
	}

	// 3. 创建一个无关的普通 .txt 和非 HDX 的普通 .zip（应该被安全忽略）
	if err := os.WriteFile(filepath.Join(parentDir, "readme.txt"), []byte("some notes"), 0644); err != nil {
		t.Fatalf("write readme.txt failed: %v", err)
	}
	dummyZip := buildZipBytes(t, map[string]string{"dummy.txt": "hello"})
	if err := os.WriteFile(filepath.Join(parentDir, "random.zip"), dummyZip, 0644); err != nil {
		t.Fatalf("write random.zip failed: %v", err)
	}

	// 执行智能扫描
	res, err := hdx.ScanHDXDirectory(parentDir)
	if err != nil {
		t.Fatalf("ScanHDXDirectory failed: %v", err)
	}

	if res.TotalCount != 2 {
		t.Fatalf("expected 2 HDX items, got %d (items: %+v)", res.TotalCount, res.Items)
	}
	if res.ArchiveCount != 1 {
		t.Errorf("expected 1 archive, got %d", res.ArchiveCount)
	}
	if res.DirectoryCount != 1 {
		t.Errorf("expected 1 directory, got %d", res.DirectoryCount)
	}

	var foundArchive, foundDir bool
	for _, item := range res.Items {
		if item.Type == "archive" {
			foundArchive = true
			if item.LibID != "AZN1024P" {
				t.Errorf("archive LibID expected AZN1024P, got %s", item.LibID)
			}
			if item.ProductType != "CloudEngine 16800" {
				t.Errorf("archive ProductType expected CloudEngine 16800, got %s", item.ProductType)
			}
		}
		if item.Type == "directory" {
			foundDir = true
			if item.LibID != "AZN1024P" {
				t.Errorf("directory LibID expected AZN1024P, got %s", item.LibID)
			}
			if item.Size <= 0 {
				t.Errorf("directory Size expected > 0, got %d", item.Size)
			}
		}
	}

	if !foundArchive || !foundDir {
		t.Fatalf("expected both archive and directory to be detected, got archive=%v, dir=%v", foundArchive, foundDir)
	}
}

func TestScanHDXDirectDocRoot(t *testing.T) {
	// 测试直接扫描一个本身就是 HDX 解压根目录（包含 profile.xml）的路径
	docDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(docDir, "profile.xml"), []byte(fixtureProfileXML), 0644); err != nil {
		t.Fatalf("write profile.xml failed: %v", err)
	}

	res, err := hdx.ScanHDXDirectory(docDir)
	if err != nil {
		t.Fatalf("ScanHDXDirectory failed: %v", err)
	}

	if res.TotalCount != 1 || res.DirectoryCount != 1 {
		t.Fatalf("expected 1 directory item, got %d", res.TotalCount)
	}
	if res.Items[0].LibID != "AZN1024P" {
		t.Errorf("expected LibID AZN1024P, got %s", res.Items[0].LibID)
	}
}
