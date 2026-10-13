package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

// IconTheme 定义图标的主题风格
type IconTheme string

const (
	// ThemeBlueShield 蔚蓝护盾·极客守护者 (默认推荐：融合日志条目与在线指示灯)
	ThemeBlueShield IconTheme = "blue_shield"
	// ThemeEmeraldTerminal 曜石方舱·日志脉冲 (极客终端与日志流)
	ThemeEmeraldTerminal IconTheme = "emerald_terminal"
	// ThemeIndigoRadar 星穹雷达·根因透镜 (科技雷达与精准聚焦)
	ThemeIndigoRadar IconTheme = "indigo_radar"
)

func clamp(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v + 0.5)
}

// GenerateIconPNG 渲染指定主题的高清平滑 PNG 图标 (默认尺寸 32x32)
func GenerateIconPNG(theme IconTheme, outSize int) []byte {
	if outSize <= 0 {
		outSize = 32
	}
	const ssaa = 4 // 4x SSAA 超采样
	w := outSize * ssaa
	h := outSize * ssaa

	highRes := image.NewRGBA(image.Rect(0, 0, w, h))
	cx := float64(w) / 2.0
	cy := float64(h) / 2.0
	unit := float64(ssaa)

	switch theme {
	case ThemeEmeraldTerminal:
		renderTerminalIcon(highRes, w, h, cx, cy, unit)
	case ThemeIndigoRadar:
		renderRadarIcon(highRes, w, h, cx, cy, unit)
	default:
		renderShieldIcon(highRes, w, h, cx, cy, unit)
	}

	// 4x 下采样为目标尺寸 (Box Filter)
	finalImg := image.NewRGBA(image.Rect(0, 0, outSize, outSize))
	for y := 0; y < outSize; y++ {
		for x := 0; x < outSize; x++ {
			var rSum, gSum, bSum, aSum float64
			for sy := 0; sy < ssaa; sy++ {
				for sx := 0; sx < ssaa; sx++ {
					c := highRes.RGBAAt(x*ssaa+sx, y*ssaa+sy)
					alpha := float64(c.A) / 255.0
					rSum += float64(c.R) * alpha
					gSum += float64(c.G) * alpha
					bSum += float64(c.B) * alpha
					aSum += alpha
				}
			}
			totalSamples := float64(ssaa * ssaa)
			finalA := aSum / totalSamples
			if finalA > 0.001 {
				finalImg.Set(x, y, color.RGBA{
					R: clamp(rSum / aSum),
					G: clamp(gSum / aSum),
					B: clamp(bSum / aSum),
					A: clamp(finalA * 255.0),
				})
			} else {
				finalImg.Set(x, y, color.RGBA{0, 0, 0, 0})
			}
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, finalImg)
	return buf.Bytes()
}

// renderShieldIcon 渲染【蔚蓝护盾·极客守护者】
func renderShieldIcon(img *image.RGBA, w, h int, cx, cy, unit float64) {
	cyOffset := cy * 0.92

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			nx := (float64(x) - cx) / unit
			ny := (float64(y) - cyOffset) / unit

			// 盾牌对称造型：
			// 顶部 ny 从 -12 到 -1.5，横向半宽 ~ 12.0
			// 底部 ny 从 -1.5 到 13.0，横向半宽从 12.0 平滑收缩至 0
			var halfW float64
			var inYRange bool

			if ny >= -12.0 && ny <= 13.0 {
				inYRange = true
				if ny < -1.5 {
					// 顶部两角平滑圆角
					t := (ny - (-1.5)) / -10.5
					halfW = 12.0 - 2.5*t*t
				} else {
					// 底部圆弧向下聚拢
					t := (ny - (-1.5)) / 14.5
					halfW = 12.0 * math.Cos(t*math.Pi*0.48)
				}
			}

			ax := math.Abs(nx)
			if inYRange && ax <= halfW {
				distFromEdge := halfW - ax

				// 垂直主渐变：顶部亮宝蓝 (#2563eb) -> 底部深邃藏青 (#1e3a8a)
				tY := (ny + 12.0) / 25.0
				r := 37.0*(1.0-tY) + 30.0*tY
				g := 99.0*(1.0-tY) + 58.0*tY
				b := 235.0*(1.0-tY) + 138.0*tY

				// 外轮廓高光边框 (Sky Blue #38bdf8)
				if distFromEdge < 1.3 {
					glow := (1.3 - distFromEdge) / 1.3
					r = r*(1-glow) + 56.0*glow
					g = g*(1-glow) + 189.0*glow
					b = b*(1-glow) + 248.0*glow
				}

				img.SetRGBA(x, y, color.RGBA{clamp(r), clamp(g), clamp(b), 255})
			}
		}
	}

	// 绘制内嵌平滑日志胶囊条 (白、冰蓝、天蓝)
	drawCapsule(img, cx-6.5*unit, cyOffset-6.5*unit, cx+6.5*unit, cyOffset-3.8*unit, color.RGBA{255, 255, 255, 255})
	drawCapsule(img, cx-6.5*unit, cyOffset-2.0*unit, cx+4.0*unit, cyOffset+0.7*unit, color.RGBA{224, 242, 254, 255})
	drawCapsule(img, cx-6.5*unit, cyOffset+2.5*unit, cx+1.0*unit, cyOffset+5.2*unit, color.RGBA{186, 230, 253, 255})

	// 右下角在线运行状态指示灯
	beaconX := cx + 8.2*unit
	beaconY := cyOffset + 8.2*unit
	beaconR := 4.0 * unit

	// 浅色背底圆环隔断，增强与背景/盾牌的对比度
	for py := int(beaconY - beaconR - 1.2*unit); py <= int(beaconY+beaconR+1.2*unit); py++ {
		for px := int(beaconX - beaconR - 1.2*unit); px <= int(beaconX+beaconR+1.2*unit); px++ {
			d := math.Hypot(float64(px)-beaconX, float64(py)-beaconY)
			if d <= beaconR+1.2*unit && d > beaconR {
				img.SetRGBA(px, py, color.RGBA{15, 23, 42, 255}) // 深色轮廓裁切圈
			}
		}
	}

	for py := int(beaconY - beaconR); py <= int(beaconY+beaconR); py++ {
		for px := int(beaconX - beaconR); px <= int(beaconX+beaconR); px++ {
			d := math.Hypot(float64(px)-beaconX, float64(py)-beaconY)
			if d <= beaconR {
				// 绿灯光泽球体渐变：极光翡翠绿 (#10b981 ~ #059669)
				t := d / beaconR
				r := 16.0*(1.0-t) + 5.0*t
				g := 185.0*(1.0-t) + 150.0*t
				b := 129.0*(1.0-t) + 105.0*t

				// 球体顶部小白高光点
				if math.Hypot(float64(px)-(beaconX-unit*0.9), float64(py)-(beaconY-unit*0.9)) <= unit*1.0 {
					img.SetRGBA(px, py, color.RGBA{255, 255, 255, 255})
				} else {
					img.SetRGBA(px, py, color.RGBA{clamp(r), clamp(g), clamp(b), 255})
				}
			}
		}
	}
}

// renderTerminalIcon 渲染【曜石方舱·日志脉冲】
func renderTerminalIcon(img *image.RGBA, w, h int, cx, cy, unit float64) {
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			nx := math.Abs((float64(x) - cx) / unit)
			ny := math.Abs((float64(y) - cy) / unit)
			cornerR := 3.5
			inside := false
			if nx <= 13.0 && ny <= 13.0 {
				if nx > 13.0-cornerR && ny > 13.0-cornerR {
					dx := nx - (13.0 - cornerR)
					dy := ny - (13.0 - cornerR)
					if math.Hypot(dx, dy) <= cornerR {
						inside = true
					}
				} else {
					inside = true
				}
			}

			if inside {
				tY := float64(y) / float64(h)
				r := 15.0*(1.0-tY) + 30.0*tY
				g := 23.0*(1.0-tY) + 41.0*tY
				b := 42.0*(1.0-tY) + 59.0*tY
				img.SetRGBA(x, y, color.RGBA{clamp(r), clamp(g), clamp(b), 255})
			}
		}
	}

	promptGreen := color.RGBA{52, 211, 153, 255}
	drawCapsule(img, cx-8.5*unit, cy-7.0*unit, cx-4.5*unit, cy-3.0*unit, promptGreen)
	drawCapsule(img, cx-4.5*unit, cy-3.0*unit, cx-8.5*unit, cy+1.0*unit, promptGreen)

	drawCapsule(img, cx-1.0*unit, cy-1.0*unit, cx+8.0*unit, cy+1.0*unit, color.RGBA{16, 185, 129, 255})
	drawCapsule(img, cx-8.5*unit, cy+5.0*unit, cx+5.0*unit, cy+7.0*unit, color.RGBA{110, 231, 183, 255})
}

// renderRadarIcon 渲染【星穹雷达·根因透镜】
func renderRadarIcon(img *image.RGBA, w, h int, cx, cy, unit float64) {
	maxR := 13.5 * unit
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			d := math.Hypot(float64(x)-cx, float64(y)-cy)
			if d <= maxR {
				t := d / maxR
				r := 49.0*(1.0-t) + 67.0*t
				g := 46.0*(1.0-t) + 56.0*t
				b := 129.0*(1.0-t) + 202.0*t
				img.SetRGBA(x, y, color.RGBA{clamp(r), clamp(g), clamp(b), 255})
			}
		}
	}

	cyanRing := color.RGBA{56, 189, 248, 240}
	drawRing(img, cx, cy, 9.5*unit, 1.4*unit, cyanRing)
	drawRing(img, cx, cy, 5.0*unit, 1.4*unit, cyanRing)

	coreWhite := color.RGBA{255, 255, 255, 255}
	for py := int(cy - 2.5*unit); py <= int(cy+2.5*unit); py++ {
		for px := int(cx - 2.5*unit); px <= int(cx+2.5*unit); px++ {
			if math.Hypot(float64(px)-cx, float64(py)-cy) <= 2.2*unit {
				img.SetRGBA(px, py, coreWhite)
			}
		}
	}
}

// drawCapsule 绘制平滑胶囊形线条 (端点为半圆)
func drawCapsule(img *image.RGBA, x1, y1, x2, y2 float64, c color.RGBA) {
	if x1 > x2 {
		x1, x2 = x2, x1
	}
	if y1 > y2 {
		y1, y2 = y2, y1
	}
	rad := (y2 - y1) / 2.0
	cap1X := x1 + rad
	cap2X := x2 - rad
	centerY := y1 + rad

	for py := int(y1); py <= int(y2); py++ {
		for px := int(x1); px <= int(x2); px++ {
			fx := float64(px)
			fy := float64(py)

			inside := false
			if fx >= cap1X && fx <= cap2X {
				inside = true
			} else if fx < cap1X {
				if math.Hypot(fx-cap1X, fy-centerY) <= rad {
					inside = true
				}
			} else {
				if math.Hypot(fx-cap2X, fy-centerY) <= rad {
					inside = true
				}
			}

			if inside {
				img.SetRGBA(px, py, c)
			}
		}
	}
}

// drawRing 绘制圆环
func drawRing(img *image.RGBA, cx, cy, r, thick float64, c color.RGBA) {
	for py := int(cy - r - thick); py <= int(cy+r+thick); py++ {
		for px := int(cx - r - thick); px <= int(cx+r+thick); px++ {
			d := math.Hypot(float64(px)-cx, float64(py)-cy)
			if math.Abs(d-r) <= thick/2.0 {
				img.SetRGBA(px, py, c)
			}
		}
	}
}

// EncodeICO 将 PNG 字节组装为标准的 Windows .ico 格式二进制数据
func EncodeICO(pngBytes []byte, width, height int) []byte {
	var buf bytes.Buffer
	// ICONDIR (6 bytes)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(0)) // Reserved
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // Type 1 = ICO
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // 1 个图像

	// ICONDIRENTRY (16 bytes)
	w := byte(width)
	h := byte(height)
	if width >= 256 {
		w = 0
	}
	if height >= 256 {
		h = 0
	}
	buf.WriteByte(w)
	buf.WriteByte(h)
	buf.WriteByte(0) // Color count
	buf.WriteByte(0) // Reserved
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))  // Planes
	_ = binary.Write(&buf, binary.LittleEndian, uint16(32)) // Bit count
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(pngBytes)))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(6+16)) // 偏移量: 22

	buf.Write(pngBytes)
	return buf.Bytes()
}

// LoadIconFromFile 从外部文件读取图标（支持 .png 或 .ico）
func LoadIconFromFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}
