package app

import (
	"image"
	"image/color"
	"testing"
)

// TestScaleBilinearDimensions 验证等比缩放与“只缩小不放大”。
func TestScaleBilinearDimensions(t *testing.T) {
	cases := []struct {
		name          string
		width, height int
		maxEdge       int
		wantW, wantH  int
	}{
		{"横图缩小", 1024, 512, 512, 512, 256},
		{"竖图缩小", 1000, 3000, 512, 171, 512},
		{"小图不放大", 100, 50, 512, 100, 50},
		{"正方形边界不变", 512, 512, 512, 512, 512},
		{"单像素", 1, 1, 512, 1, 1},
		{"极端长条不小于1", 3, 7, 2, 1, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := image.NewRGBA(image.Rect(0, 0, tc.width, tc.height))
			scaled, err := scaleBilinear(source, tc.maxEdge)
			if err != nil {
				t.Fatalf("缩放失败: %v", err)
			}
			bounds := scaled.Bounds()
			if bounds.Dx() != tc.wantW || bounds.Dy() != tc.wantH {
				t.Fatalf("尺寸 = %dx%d，期望 %dx%d", bounds.Dx(), bounds.Dy(), tc.wantW, tc.wantH)
			}
		})
	}
}

// TestScaleBilinearSolidColor 验证纯色图缩放后颜色不变且完全不透明。
func TestScaleBilinearSolidColor(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 40, 30))
	fill := color.RGBA{R: 10, G: 20, B: 30, A: 255}
	for y := 0; y < 30; y++ {
		for x := 0; x < 40; x++ {
			source.Set(x, y, fill)
		}
	}
	scaled, err := scaleBilinear(source, 16)
	if err != nil {
		t.Fatalf("缩放失败: %v", err)
	}
	if scaled.Bounds().Dx() != 16 || scaled.Bounds().Dy() != 12 {
		t.Fatalf("尺寸 = %dx%d，期望 16x12", scaled.Bounds().Dx(), scaled.Bounds().Dy())
	}
	for y := 0; y < scaled.Bounds().Dy(); y++ {
		for x := 0; x < scaled.Bounds().Dx(); x++ {
			r, g, b, a := scaled.At(x, y).RGBA()
			if r>>8 != 10 || g>>8 != 20 || b>>8 != 30 || a>>8 != 255 {
				t.Fatalf("像素 (%d,%d) = (%d,%d,%d,%d)，期望 (10,20,30,255)", x, y, r>>8, g>>8, b>>8, a>>8)
			}
		}
	}
}

// TestScaleBilinearAlphaOverWhite 验证带 alpha 的像素与白底合成。
func TestScaleBilinearAlphaOverWhite(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 255, G: 0, B: 0, A: 128})
		}
	}
	scaled, err := scaleBilinear(source, 1)
	if err != nil {
		t.Fatalf("缩放失败: %v", err)
	}
	r, g, b, a := scaled.At(0, 0).RGBA()
	// 红色 alpha=128 与白色合成后约为 (255,127,127)，JS 端 8 位表示如下。
	if r>>8 != 255 || a>>8 != 255 {
		t.Fatalf("红/alpha = (%d,%d)，期望 (255,255)", r>>8, a>>8)
	}
	if g>>8 < 125 || g>>8 > 129 || b>>8 < 125 || b>>8 > 129 {
		t.Fatalf("绿/蓝 = (%d,%d)，期望约 127", g>>8, b>>8)
	}

	transparent := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	scaledTransparent, err := scaleBilinear(transparent, 1)
	if err != nil {
		t.Fatalf("缩放失败: %v", err)
	}
	r, g, b, a = scaledTransparent.At(0, 0).RGBA()
	if r>>8 != 255 || g>>8 != 255 || b>>8 != 255 || a>>8 != 255 {
		t.Fatalf("全透明像素 = (%d,%d,%d,%d)，期望白底 (255,255,255,255)", r>>8, g>>8, b>>8, a>>8)
	}
}

// TestScaleBilinearRejectsInvalidInput 验证边界情况返回错误而不是 panic。
func TestScaleBilinearRejectsInvalidInput(t *testing.T) {
	if _, err := scaleBilinear(nil, 512); err == nil {
		t.Fatal("空图像应当返回错误")
	}
	empty := image.NewRGBA(image.Rect(0, 0, 0, 0))
	if _, err := scaleBilinear(empty, 512); err == nil {
		t.Fatal("0 尺寸源图应当返回错误")
	}
	valid := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if _, err := scaleBilinear(valid, 0); err == nil {
		t.Fatal("maxEdge 为 0 应当返回错误")
	}
	if _, err := scaleBilinear(valid, -1); err == nil {
		t.Fatal("maxEdge 为负数应当返回错误")
	}
}

// TestTargetSize 验证目标尺寸计算（只缩小不放大且至少 1 像素）。
func TestTargetSize(t *testing.T) {
	cases := []struct {
		width, height, maxEdge int
		wantW, wantH           int
	}{
		{2000, 1000, 512, 512, 256},
		{1000, 3000, 512, 171, 512},
		{50, 50, 512, 50, 50},
		{3, 7, 2, 1, 2},
	}
	for _, tc := range cases {
		gotW, gotH := targetSize(tc.width, tc.height, tc.maxEdge)
		if gotW != tc.wantW || gotH != tc.wantH {
			t.Errorf("targetSize(%d,%d,%d) = %d,%d，期望 %d,%d",
				tc.width, tc.height, tc.maxEdge, gotW, gotH, tc.wantW, tc.wantH)
		}
	}
}
