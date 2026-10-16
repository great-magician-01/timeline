package media

import (
	"errors"
	"fmt"
	"image"
	"math"

	// 注册标准库图片解码器：缩略图生成与上传时的尺寸解析都依赖它们。
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// scaleBilinear 把 src 等比缩放到最长边不超过 maxEdge，使用双线性插值，只缩小不放大。
//
// 结果始终与白底合成（把 alpha 与白色按预乘规则混合），避免 PNG 透明区域转成 JPEG 后变黑。
// 源图尺寸非法、src 为空或 maxEdge 非正时返回错误而不是 panic。
func scaleBilinear(src image.Image, maxEdge int) (*image.RGBA, error) {
	if src == nil {
		return nil, errors.New("待缩放的图像为空")
	}
	if maxEdge <= 0 {
		return nil, fmt.Errorf("目标最长边必须大于零，当前为 %d", maxEdge)
	}
	bounds := src.Bounds()
	srcWidth, srcHeight := bounds.Dx(), bounds.Dy()
	if srcWidth <= 0 || srcHeight <= 0 {
		return nil, errors.New("源图尺寸为空，无法缩放")
	}

	dstWidth, dstHeight := targetSize(srcWidth, srcHeight, maxEdge)
	dst := image.NewRGBA(image.Rect(0, 0, dstWidth, dstHeight))
	scaleX := float64(srcWidth) / float64(dstWidth)
	scaleY := float64(srcHeight) / float64(dstHeight)

	for dy := 0; dy < dstHeight; dy++ {
		// 目标像素中心映射回源坐标（align corners），边界像素钳制到源图范围内。
		sourceY := (float64(dy)+0.5)*scaleY - 0.5
		y0 := int(math.Floor(sourceY))
		fy := sourceY - float64(y0)
		if y0 < 0 {
			y0, fy = 0, 0
		}
		if y0 > srcHeight-1 {
			y0, fy = srcHeight-1, 0
		}
		y1 := y0 + 1
		if y1 > srcHeight-1 {
			y1 = srcHeight - 1
		}
		for dx := 0; dx < dstWidth; dx++ {
			sourceX := (float64(dx)+0.5)*scaleX - 0.5
			x0 := int(math.Floor(sourceX))
			fx := sourceX - float64(x0)
			if x0 < 0 {
				x0, fx = 0, 0
			}
			if x0 > srcWidth-1 {
				x0, fx = srcWidth-1, 0
			}
			x1 := x0 + 1
			if x1 > srcWidth-1 {
				x1 = srcWidth - 1
			}

			// At/RGBA 返回 16 位预乘 alpha 分量，逐通道做双线性插值。
			r00, g00, b00, a00 := src.At(bounds.Min.X+x0, bounds.Min.Y+y0).RGBA()
			r01, g01, b01, a01 := src.At(bounds.Min.X+x1, bounds.Min.Y+y0).RGBA()
			r10, g10, b10, a10 := src.At(bounds.Min.X+x0, bounds.Min.Y+y1).RGBA()
			r11, g11, b11, a11 := src.At(bounds.Min.X+x1, bounds.Min.Y+y1).RGBA()

			red := bilinear(r00, r01, r10, r11, fx, fy)
			green := bilinear(g00, g01, g10, g11, fx, fy)
			blue := bilinear(b00, b01, b10, b11, fx, fy)
			alpha := bilinear(a00, a01, a10, a11, fx, fy)

			// 与白底合成：预乘分量 + 白色分量 * (1 - alpha)，结果完全不透明。
			offset := 65535 - alpha
			index := dst.PixOffset(dx, dy)
			dst.Pix[index+0] = to8Bit(red + offset)
			dst.Pix[index+1] = to8Bit(green + offset)
			dst.Pix[index+2] = to8Bit(blue + offset)
			dst.Pix[index+3] = 255
		}
	}
	return dst, nil
}

// targetSize 返回等比缩放后的目标尺寸：只缩小不放大，任一维至少为 1。
func targetSize(width, height, maxEdge int) (int, int) {
	longest := width
	if height > longest {
		longest = height
	}
	if longest <= maxEdge {
		return width, height
	}
	if width >= height {
		return maxEdge, max(1, int(math.Round(float64(height)*float64(maxEdge)/float64(width))))
	}
	return max(1, int(math.Round(float64(width)*float64(maxEdge)/float64(height)))), maxEdge
}

// bilinear 对 16 位分量做双线性插值。
func bilinear(v00, v01, v10, v11 uint32, fx, fy float64) float64 {
	top := float64(v00) + (float64(v01)-float64(v00))*fx
	bottom := float64(v10) + (float64(v11)-float64(v10))*fx
	return top + (bottom-top)*fy
}

// to8Bit 把 16 位分量转换为 8 位，与标准库一致取高 8 位。
func to8Bit(value float64) uint8 {
	switch {
	case value <= 0:
		return 0
	case value >= 65535:
		return 255
	}
	return uint8(int(value) >> 8)
}
