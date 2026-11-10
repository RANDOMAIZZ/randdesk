// Package desktop — удалённый рабочий стол: захват экрана, передача изменений, ввод.
package desktop

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"runtime"
	"sync"
)

// Бинарное сообщение кадра (агент -> браузер), little-endian:
//
//	[0]    тип = 1
//	[1:5]  номер кадра (u32)
//	[5:7]  ширина изображения (u16), [7:9] высота (u16)
//	[9:11] число прямоугольников N (u16)
//	N раз: x,y,w,h (u16 x4), длина JPEG (u32), JPEG
const msgFrame = 1

const (
	tileSize     = 64
	maxMergeRows = 256 // ограничение высоты склеенного блока — чтобы кодировать параллельно
)

type rect struct{ x, y, w, h int }

// diffRects находит изменившиеся области cur относительно prev (оба одного размера).
// Соседние изменённые плитки склеиваются по горизонтали и (если совпадают по x/ширине) по вертикали.
func diffRects(cur, prev *image.RGBA) []rect {
	W, H := cur.Rect.Dx(), cur.Rect.Dy()
	var out []rect
	open := map[[2]int]int{} // (x, w) -> индекс в out для склейки с предыдущей строкой плиток
	for ty := 0; ty < H; ty += tileSize {
		th := min(tileSize, H-ty)
		next := map[[2]int]int{}
		run := -1
		flush := func(end int) {
			r := rect{run, ty, end - run, th}
			k := [2]int{r.x, r.w}
			if i, ok := open[k]; ok && out[i].y+out[i].h == ty && out[i].h+th <= maxMergeRows {
				out[i].h += th
				next[k] = i
			} else {
				out = append(out, r)
				next[k] = len(out) - 1
			}
			run = -1
		}
		for tx := 0; tx < W; tx += tileSize {
			tw := min(tileSize, W-tx)
			changed := false
			for y := ty; y < ty+th; y++ {
				o := y*cur.Stride + tx*4
				if !bytes.Equal(cur.Pix[o:o+tw*4], prev.Pix[o:o+tw*4]) {
					changed = true
					break
				}
			}
			if changed {
				if run < 0 {
					run = tx
				}
			} else if run >= 0 {
				flush(tx)
			}
		}
		if run >= 0 {
			flush(W)
		}
		open = next
	}
	return out
}

// fullRects делит всё изображение на полосы (для первого кадра).
func fullRects(img *image.RGBA) []rect {
	W, H := img.Rect.Dx(), img.Rect.Dy()
	var out []rect
	for y := 0; y < H; y += maxMergeRows {
		out = append(out, rect{0, y, W, min(maxMergeRows, H-y)})
	}
	return out
}

// copyRects переносит изменённые области из src в dst.
func copyRects(dst, src *image.RGBA, rs []rect) {
	for _, r := range rs {
		for y := r.y; y < r.y+r.h; y++ {
			o := y*src.Stride + r.x*4
			copy(dst.Pix[o:o+r.w*4], src.Pix[o:o+r.w*4])
		}
	}
}

var bufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// encodeFrame кодирует области в JPEG параллельно и собирает одно бинарное сообщение.
func encodeFrame(img *image.RGBA, rs []rect, quality int, id uint32) []byte {
	parts := make([]*bytes.Buffer, len(rs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())
	for i, r := range rs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, r rect) {
			defer func() { <-sem; wg.Done() }()
			b := bufPool.Get().(*bytes.Buffer)
			b.Reset()
			sub := img.SubImage(image.Rect(r.x, r.y, r.x+r.w, r.y+r.h)).(*image.RGBA)
			jpeg.Encode(b, sub, &jpeg.Options{Quality: quality})
			parts[i] = b
		}(i, r)
	}
	wg.Wait()

	size := 11
	for _, p := range parts {
		size += 12 + p.Len()
	}
	msg := make([]byte, size)
	msg[0] = msgFrame
	binary.LittleEndian.PutUint32(msg[1:], id)
	binary.LittleEndian.PutUint16(msg[5:], uint16(img.Rect.Dx()))
	binary.LittleEndian.PutUint16(msg[7:], uint16(img.Rect.Dy()))
	binary.LittleEndian.PutUint16(msg[9:], uint16(len(rs)))
	o := 11
	for i, r := range rs {
		binary.LittleEndian.PutUint16(msg[o:], uint16(r.x))
		binary.LittleEndian.PutUint16(msg[o+2:], uint16(r.y))
		binary.LittleEndian.PutUint16(msg[o+4:], uint16(r.w))
		binary.LittleEndian.PutUint16(msg[o+6:], uint16(r.h))
		binary.LittleEndian.PutUint32(msg[o+8:], uint32(parts[i].Len()))
		o += 12
		o += copy(msg[o:], parts[i].Bytes())
		bufPool.Put(parts[i])
	}
	return msg
}

// downscale2x уменьшает изображение вдвое усреднением блоков 2x2.
func downscale2x(dst, src *image.RGBA) {
	W, H := dst.Rect.Dx(), dst.Rect.Dy()
	var wg sync.WaitGroup
	n := runtime.NumCPU()
	step := (H + n - 1) / n
	for y0 := 0; y0 < H; y0 += step {
		wg.Add(1)
		go func(y0, y1 int) {
			defer wg.Done()
			for y := y0; y < y1; y++ {
				s0 := src.Pix[(2*y)*src.Stride:]
				s1 := src.Pix[(2*y+1)*src.Stride:]
				d := dst.Pix[y*dst.Stride:]
				for x := 0; x < W; x++ {
					i, j := x*8, x*4
					d[j] = uint8((uint16(s0[i]) + uint16(s0[i+4]) + uint16(s1[i]) + uint16(s1[i+4]) + 2) >> 2)
					d[j+1] = uint8((uint16(s0[i+1]) + uint16(s0[i+5]) + uint16(s1[i+1]) + uint16(s1[i+5]) + 2) >> 2)
					d[j+2] = uint8((uint16(s0[i+2]) + uint16(s0[i+6]) + uint16(s1[i+2]) + uint16(s1[i+6]) + 2) >> 2)
					d[j+3] = 255
				}
			}
		}(y0, min(y0+step, H))
	}
	wg.Wait()
}
