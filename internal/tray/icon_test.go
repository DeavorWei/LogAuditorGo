package tray

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateIcons(t *testing.T) {
	themes := []IconTheme{
		ThemeBlueShield,
		ThemeEmeraldTerminal,
		ThemeIndigoRadar,
	}

	scratchDir := `C:\Users\deavorwei\.gemini\antigravity\brain\7ac3ef55-2696-4661-abfa-096aafe850f0\scratch`
	_ = os.MkdirAll(scratchDir, 0755)

	for _, theme := range themes {
		pngBytes := GenerateIconPNG(theme, 32)
		if len(pngBytes) == 0 {
			t.Fatalf("Failed to generate PNG for %s", theme)
		}

		pngPath := filepath.Join(scratchDir, string("icon_"+theme+".png"))
		if err := os.WriteFile(pngPath, pngBytes, 0644); err != nil {
			t.Fatalf("Failed to write PNG: %v", err)
		}

		icoBytes := EncodeICO(pngBytes, 32, 32)
		if len(icoBytes) <= 22 {
			t.Fatalf("Invalid ICO bytes length for %s", theme)
		}
		icoPath := filepath.Join(scratchDir, string("icon_"+theme+".ico"))
		_ = os.WriteFile(icoPath, icoBytes, 0644)

		// 导出到项目 assets 目录
		assetsDir := `d:\Document\GO\LogAuditorGo\assets`
		_ = os.MkdirAll(assetsDir, 0755)
		_ = os.WriteFile(filepath.Join(assetsDir, string(theme)+".png"), pngBytes, 0644)
		_ = os.WriteFile(filepath.Join(assetsDir, string(theme)+".ico"), icoBytes, 0644)
		if theme == ThemeBlueShield {
			_ = os.WriteFile(filepath.Join(assetsDir, "app.ico"), icoBytes, 0644)
			_ = os.WriteFile(filepath.Join(assetsDir, "app.png"), pngBytes, 0644)
		}
	}
}
