//go:build windows

package desktop

import (
	"encoding/base64"
	"image"
	"os"
	"testing"
	"time"
)

// RANDDESK_H264_DUMP=файл — пишет ключевой и 2 обычных кадра (base64 через перевод строки)
func TestH264Dump(t *testing.T) {
	out := os.Getenv("RANDDESK_H264_DUMP")
	if out == "" {
		t.Skip()
	}
	w, h := 640, 360
	enc, err := newH264(w, h, 60, h264Bitrate(85, w, h, 60))
	if err != nil {
		t.Fatal(err)
	}
	defer enc.close()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	cols := [][3]byte{{255, 0, 0}, {0, 255, 0}, {0, 0, 255}, {255, 255, 255}}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := cols[x*4/w]
			i := y*img.Stride + x*4
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c[0], c[1], c[2], 255
		}
	}
	var s string
	for i := 0; i < 3; i++ {
		if err := enc.encode(func(dst []byte) { rgbaToNV12(dst, img, w, h) }, false); err != nil {
			t.Fatal(err)
		}
		select {
		case f := <-enc.out:
			s += base64.StdEncoding.EncodeToString(f.data) + "\n"
		case <-time.After(2 * time.Second):
			t.Fatal("нет кадра")
		}
	}
	os.WriteFile(out, []byte(s), 0o644)
}
