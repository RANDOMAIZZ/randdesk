package desktop

import (
	"image"
	"runtime"
	"sync"
)

func rgbaToNV12(dst []byte, src *image.RGBA, w, h int) {
	toNV12(dst, src.Pix, src.Stride, src.Rect.Dx(), false, w, h)
}

// toNV12 переводит RGBA/BGRA (bgr) шириной sw в NV12 (BT.709, диапазон 16..235) размером w×h (чётные).
// Если источник вдвое шире — заодно уменьшает. Работает параллельно по полосам строк.
func toNV12(dst, pix []byte, stride, sw int, bgr bool, w, h int) {
	ri, bi := 0, 2
	if bgr {
		ri, bi = 2, 0
	}
	step := 1
	if sw >= 2*w {
		step = 2
	}
	yPlane, uv := dst[:w*h], dst[w*h:]
	bands := runtime.GOMAXPROCS(0)
	rows := h / 2 // по парам строк — из-за цветности 2×2
	per := (rows + bands - 1) / bands
	var wg sync.WaitGroup
	for b := 0; b < bands; b++ {
		r0, r1 := b*per, min(rows, (b+1)*per)
		if r0 >= r1 {
			break
		}
		wg.Add(1)
		go func(r0, r1 int) {
			defer wg.Done()
			for r := r0; r < r1; r++ {
				y0 := 2 * r
				for dy := 0; dy < 2; dy++ {
					row := pix[(y0+dy)*step*stride:]
					out := yPlane[(y0+dy)*w : (y0+dy+1)*w]
					for x := 0; x < w; x++ {
						p := row[x*step*4 : x*step*4+3]
						out[x] = uint8((47*int(p[ri])+157*int(p[1])+16*int(p[bi])+128)>>8 + 16)
					}
				}
				uvRow := uv[r*w : r*w+w]
				ra := pix[y0*step*stride:]
				rb := pix[(y0+1)*step*stride:]
				for x := 0; x < w; x += 2 {
					i, j := x*step*4, (x+1)*step*4
					R := int(ra[i+ri]) + int(ra[j+ri]) + int(rb[i+ri]) + int(rb[j+ri])
					G := int(ra[i+1]) + int(ra[j+1]) + int(rb[i+1]) + int(rb[j+1])
					B := int(ra[i+bi]) + int(ra[j+bi]) + int(rb[i+bi]) + int(rb[j+bi])
					uvRow[x] = uint8((-26*R-87*G+113*B+512)>>10 + 128)
					uvRow[x+1] = uint8((112*R-102*G-10*B+512)>>10 + 128)
				}
			}
		}(r0, r1)
	}
	wg.Wait()
}
