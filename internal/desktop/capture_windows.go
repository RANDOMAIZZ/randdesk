//go:build windows

package desktop

import (
	"errors"
	"fmt"
	"image"
	"reflect"
	"time"
	"unsafe"

	"github.com/kbinani/screenshot"
	"github.com/kirides/go-d3d/d3d11"
	"github.com/kirides/go-d3d/outputduplication"
)

type monitor struct {
	Index   int    `json:"index"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	W       int    `json:"w"`
	H       int    `json:"h"`
	Primary bool   `json:"primary"`
	Name    string `json:"name"`
}

func monitors() []monitor {
	n := screenshot.NumActiveDisplays()
	out := make([]monitor, 0, n)
	for i := 0; i < n; i++ {
		b := screenshot.GetDisplayBounds(i)
		out = append(out, monitor{Index: i, X: b.Min.X, Y: b.Min.Y, W: b.Dx(), H: b.Dy(),
			Primary: b.Min.X == 0 && b.Min.Y == 0, Name: fmt.Sprintf("Монитор %d (%d×%d)", i+1, b.Dx(), b.Dy())})
	}
	return out
}

// capturer пишет текущее изображение монитора в img (RGBA, размер = bounds).
type capturer interface {
	// next ждёт новый кадр до timeoutMs; false — изображение не менялось.
	next(img *image.RGBA, timeoutMs uint) (bool, error)
	bounds() image.Rectangle
	kind() string
	close()
}

// ---------- DXGI Desktop Duplication (быстро, через GPU) ----------

type dxgiCap struct {
	dev *d3d11.ID3D11Device
	ctx *d3d11.ID3D11DeviceContext
	dup *outputduplication.OutputDuplicator
	b   image.Rectangle
	bgr bool // старый способ дублирования (DuplicateOutput) отдаёт BGRA
}

func newDXGI(target image.Rectangle) (*dxgiCap, error) {
	dev, ctx, err := d3d11.NewD3D11Device()
	if err != nil {
		return nil, err
	}
	var lastErr error = errors.New("монитор не найден среди выходов DXGI")
	for i := uint(0); i < 16; i++ {
		dup, err := outputduplication.NewIDXGIOutputDuplication(dev, ctx, i)
		if err != nil {
			lastErr = err
			continue
		}
		b, err := dup.GetBounds()
		if err == nil && b == target {
			// библиотека не сообщает формат наружу — читаем её внутренний признак
			bgr := reflect.ValueOf(dup).Elem().FieldByName("needsSwizzle").Bool()
			return &dxgiCap{dev: dev, ctx: ctx, dup: dup, b: b, bgr: bgr}, nil
		}
		dup.Release()
	}
	ctx.Release()
	dev.Release()
	return nil, lastErr
}

func (c *dxgiCap) next(img *image.RGBA, timeoutMs uint) (bool, error) {
	err := c.dup.GetImage(img, timeoutMs)
	if errors.Is(err, outputduplication.ErrNoImageYet) {
		return false, nil
	}
	return err == nil, err
}

// rawCapturer отдаёт кадр прямо из памяти захвата, без копирования в image.RGBA.
type rawCapturer interface {
	nextRaw(timeoutMs uint, use func(pix []byte, stride int, bgr bool)) (bool, error)
}

func (c *dxgiCap) nextRaw(timeoutMs uint, use func(pix []byte, stride int, bgr bool)) (bool, error) {
	unmap, mr, size, err := c.dup.Snapshot(timeoutMs)
	if errors.Is(err, outputduplication.ErrNoImageYet) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer unmap()
	use(unsafe.Slice((*byte)(mr.PBits), int(mr.Pitch)*int(size.Y)), int(mr.Pitch), c.bgr)
	return true, nil
}

func (c *dxgiCap) bounds() image.Rectangle { return c.b }
func (c *dxgiCap) kind() string            { return "DXGI" }
func (c *dxgiCap) close() {
	c.dup.Release()
	c.ctx.Release()
	c.dev.Release()
}

// ---------- GDI (запасной вариант: RDP-сеансы, старые драйверы) ----------

type gdiCap struct {
	b    image.Rectangle
	last time.Time
}

func (c *gdiCap) next(img *image.RGBA, timeoutMs uint) (bool, error) {
	// GDI не сообщает об изменениях — снимаем не чаще ~30 раз в секунду
	if d := 33*time.Millisecond - time.Since(c.last); d > 0 {
		time.Sleep(d)
	}
	c.last = time.Now()
	shot, err := screenshot.CaptureRect(c.b)
	if err != nil {
		return false, err
	}
	copy(img.Pix, shot.Pix)
	return true, nil
}

func (c *gdiCap) bounds() image.Rectangle { return c.b }
func (c *gdiCap) kind() string            { return "GDI" }
func (c *gdiCap) close()                  {}

func openCapturer(target image.Rectangle) (capturer, error) {
	if c, err := newDXGI(target); err == nil {
		return c, nil
	}
	if target.Empty() {
		return nil, errors.New("нет активного монитора")
	}
	return &gdiCap{b: target}, nil
}
