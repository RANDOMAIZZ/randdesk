package desktop

import (
	"image"
	"testing"
)

func BenchmarkNV12(b *testing.B) {
	w, h := 2560, 1440
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 31)
	}
	dst := make([]byte, w*h*3/2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rgbaToNV12(dst, img, w, h)
	}
}

func BenchmarkCopy(b *testing.B) {
	src := make([]byte, 2560*1440*4)
	dst := make([]byte, len(src))
	for i := 0; i < b.N; i++ {
		copy(dst, src)
	}
}
