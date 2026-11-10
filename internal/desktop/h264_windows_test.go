//go:build windows

package desktop

import (
	"image"
	"os"
	"testing"
	"time"
)

// RANDDESK_H264_TEST=1 go test -run TestH264 -v ./internal/desktop
func TestH264(t *testing.T) {
	if os.Getenv("RANDDESK_H264_TEST") == "" {
		t.Skip("нужна видеокарта: RANDDESK_H264_TEST=1")
	}
	w, h := 2560, 1440
	enc, err := newH264(w, h, 60, h264Bitrate(85, w, h, 60))
	if err != nil {
		t.Fatal(err)
	}
	defer enc.close()
	t.Logf("кодировщик: %s", enc.name)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	got := make(chan encodedFrame, 256)
	go func() {
		for f := range enc.out {
			got <- f
		}
	}()
	const n = 120
	start := time.Now()
	var conv time.Duration
	total := 0
	for i := 0; i < n; i++ {
		for y := 0; y < h; y += 4 {
			img.Pix[y*img.Stride+(i*16)%(w*4)] = byte(i * 7)
		}
		c0 := time.Now()
		if err := enc.encode(func(dst []byte) { rgbaToNV12(dst, img, w, h) }, i == 60); err != nil {
			t.Fatal(err)
		}
		conv += time.Since(c0)
	}
	keys := 0
	deadline := time.After(3 * time.Second)
	for i := 0; i < n; i++ {
		select {
		case f := <-got:
			total += len(f.data)
			if f.key {
				keys++
			}
		case <-deadline:
			t.Fatalf("получено только %d из %d кадров", i, n)
		}
	}
	el := time.Since(start)
	t.Logf("%d кадров за %v (%.1f к/с), подача кадра %.1f мс, %d КБ, ключевых %d", n, el, float64(n)/el.Seconds(), float64(conv.Microseconds())/n/1000, total/1024, keys)
}
