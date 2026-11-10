//go:build windows

package desktop

// Аппаратное сжатие видео H.264 через Media Foundation (NVIDIA NVENC, AMD AMF, Intel Quick Sync).
// Работает без cgo: COM-интерфейсы вызываются напрямую по таблицам методов.

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type comObj struct{ vtbl *[64]uintptr }

func (o *comObj) call(idx int, args ...uintptr) uint32 {
	r, _, _ := syscall.SyscallN(o.vtbl[idx], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return uint32(r)
}

func (o *comObj) release() {
	if o != nil {
		o.call(2)
	}
}

func (o *comObj) qi(iid *windows.GUID) (*comObj, error) {
	var p *comObj
	if hr := o.call(0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&p))); failed(hr) {
		return nil, hrErr("QueryInterface", hr)
	}
	return p, nil
}

func failed(hr uint32) bool { return int32(hr) < 0 }

func hrErr(what string, hr uint32) error { return fmt.Errorf("%s: 0x%08X", what, hr) }

func guid(s string) windows.GUID {
	g, err := windows.GUIDFromString("{" + s + "}")
	if err != nil {
		panic(err)
	}
	return g
}

var (
	mfCategoryVideoEncoder = guid("f79eac7d-e545-4387-bdee-d647d7bde42a")
	mfMediaTypeVideo       = guid("73646976-0000-0010-8000-00AA00389B71")
	mfVideoFormatNV12      = guid("3231564E-0000-0010-8000-00AA00389B71")
	mfVideoFormatH264      = guid("34363248-0000-0010-8000-00AA00389B71")

	mfMTMajorType      = guid("48eba18e-f8c9-4687-bf11-0a74c9f96a8f")
	mfMTSubtype        = guid("f7e34c9a-42e8-4714-b74b-cb29d72c35e5")
	mfMTAvgBitrate     = guid("20332624-fb0d-4d9e-bd0d-cbf6786c102e")
	mfMTFrameSize      = guid("1652c33d-d6b2-4012-b834-72030849a37d")
	mfMTFrameRate      = guid("c459a2e8-3d2c-4e44-b132-fee5156c7bb0")
	mfMTPixelAspect    = guid("c6376a1e-8d0a-4027-be45-6d9a0ad39bb6")
	mfMTInterlaceMode  = guid("e2724bb8-e676-4806-b4b2-a8d6efb44ccd")
	mfMTMpeg2Profile   = guid("ad76a80b-2d5c-4e0b-b375-64e520137036")
	mfMTYUVMatrix      = guid("3e23d450-2c75-4d25-a00e-b91670d12327")
	mfMTNominalRange   = guid("c21b8ee5-b956-4071-8daf-325edf5cab11")
	mfMTVideoPrimaries = guid("dbfbe4d7-0740-4ee0-8192-850ab0e21935")
	mfMTTransferFunc   = guid("5fb0fce9-be5c-4935-a811-ec838f8eed93")

	mfTransformAsync       = guid("f81a699a-649a-497d-8c73-29f8fed6ad7a")
	mfTransformAsyncUnlock = guid("e5666d6b-3422-4eb6-a421-da7db1f8e207")
	mfLowLatency           = guid("9c27891a-ed7a-40e1-88e8-b22727a024ee")
	mftFriendlyName        = guid("314ffbae-5b41-4c95-9c19-4e7d586face3")

	iidIMFTransform           = guid("bf94c121-5b05-4e6f-8000-ba598961414d")
	iidIMFMediaEventGenerator = guid("2CD0BD52-BCD5-4B89-B62C-EADC0C031E7D")
	iidICodecAPI              = guid("901db4c7-31ce-41a2-85dc-8fa0bf41b8da")

	codecRateControlMode = guid("1c0608e9-370c-4710-8a58-cb6181c42423")
	codecMeanBitRate     = guid("f7222374-2144-4815-b550-a37f8e12ee52")
	codecGOPSize         = guid("95f31b26-95a4-41aa-9303-246a7fc6eef1")
	codecBPictureCount   = guid("8d390aac-dc5c-4200-b57f-814d04babab2")
	codecForceKeyFrame   = guid("398c1b98-8353-475a-9ef2-8f265d260345")
	codecLowLatency      = guid("9c27891a-ed7a-40e1-88e8-b22727a024ee")
	codecQualityVsSpeed  = guid("98332df8-03cd-476b-89fa-3f9e442dec9f")
)

const (
	mftEnumHardware      = 0x04
	mftEnumSortAndFilter = 0x40

	mftMsgBeginStreaming = 0x10000000
	mftMsgEndStreaming   = 0x10000001
	mftMsgStartOfStream  = 0x10000003

	meTransformNeedInput  = 601
	meTransformHaveOutput = 602

	mfENeedMoreInput = 0xC00D6D72
	mfEStreamChange  = 0xC00D6D61

	mftOutputProvidesSamples   = 0x100
	mftOutputCanProvideSamples = 0x200
	vtUI4                      = 19
	vtBool                     = 11
)

var (
	mfplat                = windows.NewLazySystemDLL("mfplat.dll")
	procMFStartup         = mfplat.NewProc("MFStartup")
	procMFTEnumEx         = mfplat.NewProc("MFTEnumEx")
	procMFCreateMediaType = mfplat.NewProc("MFCreateMediaType")
	procMFCreateSample    = mfplat.NewProc("MFCreateSample")
	procMFCreateMemBuf    = mfplat.NewProc("MFCreateMemoryBuffer")
	ole32                 = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx    = ole32.NewProc("CoInitializeEx")
	procCoTaskMemFree     = ole32.NewProc("CoTaskMemFree")
)

var (
	mfOnce sync.Once
	mfErr  error
)

// mfInit запускает Media Foundation и держит поток в многопоточном COM-апартаменте (MTA):
// пока он жив, остальные потоки процесса тоже работают с COM как MTA.
func mfInit() error {
	mfOnce.Do(func() {
		if err := mfplat.Load(); err != nil {
			mfErr = errors.New("Media Foundation недоступна (Windows N без пакета мультимедиа?)")
			return
		}
		ready := make(chan error)
		go func() {
			runtime.LockOSThread()
			procCoInitializeEx.Call(0, 0) // COINIT_MULTITHREADED
			hr, _, _ := procMFStartup.Call(0x20070, 0)
			if failed(uint32(hr)) {
				ready <- hrErr("MFStartup", uint32(hr))
				return
			}
			ready <- nil
			select {} // поток держит MTA до конца процесса
		}()
		mfErr = <-ready
	})
	return mfErr
}

// ---- атрибуты (IMFAttributes: тип носителя, сэмпл, активатор) ----

func setU32(o *comObj, key *windows.GUID, v uint32) uint32 {
	return o.call(21, uintptr(unsafe.Pointer(key)), uintptr(v))
}
func setU64(o *comObj, key *windows.GUID, v uint64) uint32 {
	return o.call(22, uintptr(unsafe.Pointer(key)), uintptr(v))
}
func setGUID(o *comObj, key, v *windows.GUID) uint32 {
	return o.call(24, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(v)))
}
func getU32(o *comObj, key *windows.GUID) (uint32, bool) {
	var v uint32
	hr := o.call(7, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&v)))
	return v, !failed(hr)
}

func getString(o *comObj, key *windows.GUID) string {
	var p *uint16
	var n uint32
	if failed(o.call(13, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&n)))) || p == nil {
		return ""
	}
	s := windows.UTF16PtrToString(p)
	procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	return s
}

// variant — VARIANT для ICodecAPI::SetValue (24 байта на amd64).
type variant struct {
	vt   uint16
	_    [3]uint16
	val  uint64
	_pad uint64
}

type mftTypeInfo struct{ major, sub windows.GUID }

type mftOutputDataBuffer struct {
	streamID uint32
	sample   *comObj
	status   uint32
	events   *comObj
}

type encodedFrame struct {
	data []byte
	key  bool
}

// h264Enc — аппаратный кодировщик H.264 (асинхронный MFT).
type h264Enc struct {
	name     string
	w, h     int
	fps      int
	act      *comObj
	mft      *comObj
	gen      *comObj
	codec    *comObj
	provides bool
	outSize  uint32

	needIn  chan struct{}
	out     chan encodedFrame
	evDone  chan struct{}
	evErr   atomic.Value
	closing atomic.Bool
	ts      int64
}

// h264Bitrate подбирает поток по «качеству» из настроек: q85 при 1440p/60 — около 35 Мбит/с.
func h264Bitrate(q, w, h, fps int) uint32 {
	bpp := 0.03 + float64(max(0, q-30))*0.0025
	br := bpp * float64(w*h) * float64(max(fps, 15))
	return uint32(max(1_000_000, min(60_000_000, br)))
}

func newH264(w, h, fps int, bitrate uint32) (*h264Enc, error) {
	if err := mfInit(); err != nil {
		return nil, err
	}
	in := mftTypeInfo{mfMediaTypeVideo, mfVideoFormatNV12}
	out := mftTypeInfo{mfMediaTypeVideo, mfVideoFormatH264}
	var arr *[64]*comObj
	var n uint32
	hr, _, _ := procMFTEnumEx.Call(uintptr(unsafe.Pointer(&mfCategoryVideoEncoder)), mftEnumHardware|mftEnumSortAndFilter,
		uintptr(unsafe.Pointer(&in)), uintptr(unsafe.Pointer(&out)), uintptr(unsafe.Pointer(&arr)), uintptr(unsafe.Pointer(&n)))
	if failed(uint32(hr)) {
		return nil, hrErr("MFTEnumEx", uint32(hr))
	}
	if n == 0 || arr == nil {
		return nil, errors.New("аппаратный кодировщик H.264 не найден")
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(arr)))
	var lastErr error
	var enc *h264Enc
	for i := 0; i < int(min(n, 64)); i++ {
		a := arr[i]
		if enc == nil {
			e, err := openH264(a, w, h, fps, bitrate)
			if err == nil {
				enc = e
				continue // активатор теперь принадлежит кодировщику
			}
			lastErr = err
		}
		a.release()
	}
	if enc == nil {
		return nil, lastErr
	}
	return enc, nil
}

func openH264(act *comObj, w, h, fps int, bitrate uint32) (*h264Enc, error) {
	name := getString(act, &mftFriendlyName)
	var mft *comObj
	if hr := act.call(33, uintptr(unsafe.Pointer(&iidIMFTransform)), uintptr(unsafe.Pointer(&mft))); failed(hr) {
		return nil, hrErr(name+": ActivateObject", hr)
	}
	e := &h264Enc{name: name, w: w, h: h, fps: fps, act: act, mft: mft,
		needIn: make(chan struct{}, 64), out: make(chan encodedFrame, 16), evDone: make(chan struct{})}
	ok := false
	defer func() {
		if !ok {
			e.free()
		}
	}()
	var attrs *comObj
	if hr := mft.call(8, uintptr(unsafe.Pointer(&attrs))); !failed(hr) && attrs != nil {
		if v, _ := getU32(attrs, &mfTransformAsync); v != 0 {
			setU32(attrs, &mfTransformAsyncUnlock, 1)
		}
		setU32(attrs, &mfLowLatency, 1)
		attrs.release()
	}
	if c, err := mft.qi(&iidICodecAPI); err == nil {
		e.codec = c
		e.setCodec(&codecLowLatency, vtBool, 0xFFFF)
		e.setCodec(&codecRateControlMode, vtUI4, 0) // CBR: ровная задержка
		e.setCodec(&codecMeanBitRate, vtUI4, uint64(bitrate))
		e.setCodec(&codecBPictureCount, vtUI4, 0)
		e.setCodec(&codecQualityVsSpeed, vtUI4, 0)                // максимальная скорость: при нашем потоке разница в качестве мала
		e.setCodec(&codecGOPSize, vtUI4, uint64(max(fps, 15)*60)) // ключевые кадры — по запросу
	}
	mk := func(sub *windows.GUID) (*comObj, error) {
		var t *comObj
		if hr, _, _ := procMFCreateMediaType.Call(uintptr(unsafe.Pointer(&t))); failed(uint32(hr)) {
			return nil, hrErr("MFCreateMediaType", uint32(hr))
		}
		setGUID(t, &mfMTMajorType, &mfMediaTypeVideo)
		setGUID(t, &mfMTSubtype, sub)
		setU64(t, &mfMTFrameSize, uint64(w)<<32|uint64(h))
		setU64(t, &mfMTFrameRate, uint64(fps)<<32|1)
		setU64(t, &mfMTPixelAspect, 1<<32|1)
		setU32(t, &mfMTInterlaceMode, 2) // прогрессивная развёртка
		setU32(t, &mfMTYUVMatrix, 1)     // BT.709
		setU32(t, &mfMTNominalRange, 2)  // 16..235
		setU32(t, &mfMTVideoPrimaries, 2)
		setU32(t, &mfMTTransferFunc, 5)
		return t, nil
	}
	ot, err := mk(&mfVideoFormatH264)
	if err != nil {
		return nil, err
	}
	setU32(ot, &mfMTAvgBitrate, bitrate)
	setU32(ot, &mfMTMpeg2Profile, 100) // High
	hr := mft.call(16, 0, uintptr(unsafe.Pointer(ot)), 0)
	if failed(hr) {
		setU32(ot, &mfMTMpeg2Profile, 77) // Main
		hr = mft.call(16, 0, uintptr(unsafe.Pointer(ot)), 0)
	}
	ot.release()
	if failed(hr) {
		return nil, hrErr(name+": SetOutputType", hr)
	}
	it, err := mk(&mfVideoFormatNV12)
	if err != nil {
		return nil, err
	}
	hr = mft.call(15, 0, uintptr(unsafe.Pointer(it)), 0)
	it.release()
	if failed(hr) {
		return nil, hrErr(name+": SetInputType NV12", hr)
	}
	var info [3]uint32
	if hr := mft.call(7, 0, uintptr(unsafe.Pointer(&info))); !failed(hr) {
		e.provides = info[0]&(mftOutputProvidesSamples|mftOutputCanProvideSamples) != 0
		e.outSize = info[1]
	}
	gen, err := mft.qi(&iidIMFMediaEventGenerator)
	if err != nil {
		return nil, fmt.Errorf("%s: кодировщик не асинхронный", name)
	}
	e.gen = gen
	mft.call(23, mftMsgBeginStreaming, 0)
	mft.call(23, mftMsgStartOfStream, 0)
	go e.events()
	ok = true
	return e, nil
}

func (e *h264Enc) setCodec(api *windows.GUID, vt uint16, v uint64) {
	if e.codec == nil {
		return
	}
	val := variant{vt: vt, val: v}
	e.codec.call(9, uintptr(unsafe.Pointer(api)), uintptr(unsafe.Pointer(&val)))
}

// events — цикл событий асинхронного MFT: «нужен кадр» и «готов результат».
func (e *h264Enc) events() {
	defer close(e.evDone)
	defer close(e.out)
	for {
		var ev *comObj
		hr := e.gen.call(3, 0, uintptr(unsafe.Pointer(&ev))) // GetEvent, блокирующий
		if failed(hr) {
			if !e.closing.Load() {
				e.evErr.Store(hrErr("GetEvent", hr))
			}
			return
		}
		var typ uint32
		ev.call(33, uintptr(unsafe.Pointer(&typ)))
		ev.release()
		if e.closing.Load() {
			return
		}
		switch typ {
		case meTransformNeedInput:
			select {
			case e.needIn <- struct{}{}:
			default:
			}
		case meTransformHaveOutput:
			f, err := e.pull()
			if err != nil {
				e.evErr.Store(err)
				return
			}
			if f.data != nil {
				e.out <- f
			}
		}
	}
}

func (e *h264Enc) pull() (encodedFrame, error) {
	db := mftOutputDataBuffer{}
	if !e.provides {
		s, err := newSample(int(max(e.outSize, uint32(e.w*e.h))))
		if err != nil {
			return encodedFrame{}, err
		}
		db.sample = s
	}
	var status uint32
	hr := e.mft.call(25, 0, 1, uintptr(unsafe.Pointer(&db)), uintptr(unsafe.Pointer(&status)))
	if db.events != nil {
		db.events.release()
	}
	if hr == mfEStreamChange { // кодировщик уточнил выходной формат
		if db.sample != nil {
			db.sample.release()
		}
		var t *comObj
		if !failed(e.mft.call(14, 0, 0, uintptr(unsafe.Pointer(&t)))) && t != nil {
			e.mft.call(16, 0, uintptr(unsafe.Pointer(t)), 0)
			t.release()
		}
		return encodedFrame{}, nil
	}
	if hr == mfENeedMoreInput {
		if db.sample != nil {
			db.sample.release()
		}
		return encodedFrame{}, nil
	}
	if failed(hr) {
		if db.sample != nil {
			db.sample.release()
		}
		return encodedFrame{}, hrErr("ProcessOutput", hr)
	}
	s := db.sample
	if s == nil {
		return encodedFrame{}, nil
	}
	defer s.release()
	var buf *comObj
	if hr := s.call(41, uintptr(unsafe.Pointer(&buf))); failed(hr) { // ConvertToContiguousBuffer
		return encodedFrame{}, hrErr("ConvertToContiguousBuffer", hr)
	}
	defer buf.release()
	var p *byte
	var cur uint32
	if hr := buf.call(3, uintptr(unsafe.Pointer(&p)), 0, uintptr(unsafe.Pointer(&cur))); failed(hr) {
		return encodedFrame{}, hrErr("Lock", hr)
	}
	data := make([]byte, cur)
	copy(data, unsafe.Slice(p, cur))
	buf.call(4)
	return encodedFrame{data: data, key: hasIDR(data)}, nil
}

func newSample(size int) (*comObj, error) {
	var buf *comObj
	if hr, _, _ := procMFCreateMemBuf.Call(uintptr(size), uintptr(unsafe.Pointer(&buf))); failed(uint32(hr)) {
		return nil, hrErr("MFCreateMemoryBuffer", uint32(hr))
	}
	defer buf.release()
	var s *comObj
	if hr, _, _ := procMFCreateSample.Call(uintptr(unsafe.Pointer(&s))); failed(uint32(hr)) {
		return nil, hrErr("MFCreateSample", uint32(hr))
	}
	s.call(42, uintptr(unsafe.Pointer(buf))) // AddBuffer
	return s, nil
}

// encode ждёт, пока кодировщик попросит кадр, и передаёт ему NV12, который fill пишет прямо
// в буфер Media Foundation (без лишнего копирования).
func (e *h264Enc) encode(fill func(dst []byte), key bool) error {
	size := e.w * e.h * 3 / 2
	var buf *comObj
	if hr, _, _ := procMFCreateMemBuf.Call(uintptr(size), uintptr(unsafe.Pointer(&buf))); failed(uint32(hr)) {
		return hrErr("MFCreateMemoryBuffer", uint32(hr))
	}
	defer buf.release()
	var p *byte
	if hr := buf.call(3, uintptr(unsafe.Pointer(&p)), 0, 0); failed(hr) {
		return hrErr("Lock", hr)
	}
	fill(unsafe.Slice(p, size))
	buf.call(4)
	buf.call(6, uintptr(size)) // SetCurrentLength
	var s *comObj
	if hr, _, _ := procMFCreateSample.Call(uintptr(unsafe.Pointer(&s))); failed(uint32(hr)) {
		return hrErr("MFCreateSample", uint32(hr))
	}
	defer s.release()
	s.call(42, uintptr(unsafe.Pointer(buf)))
	// кадр уже подготовлен, пока кодировался предыдущий; теперь ждём, когда кодировщик освободится
	select {
	case <-e.needIn:
	case <-e.evDone:
		return e.err()
	case <-time.After(2 * time.Second):
		return errors.New(e.name + ": кодировщик не отвечает")
	}
	dur := int64(10_000_000 / max(e.fps, 1))
	s.call(36, uintptr(e.ts)) // SetSampleTime (100 нс)
	s.call(38, uintptr(dur))
	e.ts += dur
	if key {
		e.setCodec(&codecForceKeyFrame, vtUI4, 1)
	}
	if hr := e.mft.call(24, 0, uintptr(unsafe.Pointer(s)), 0); failed(hr) {
		return hrErr("ProcessInput", hr)
	}
	return nil
}

func (e *h264Enc) err() error {
	if v, ok := e.evErr.Load().(error); ok {
		return v
	}
	return errors.New(e.name + ": кодировщик остановился")
}

func (e *h264Enc) close() {
	e.closing.Store(true)
	if e.mft != nil {
		e.mft.call(23, mftMsgEndStreaming, 0)
	}
	if e.act != nil {
		e.act.call(34) // ShutdownObject: GetEvent возвращает ошибку, цикл событий завершается
	}
	if e.gen != nil {
		select {
		case <-e.evDone:
		case <-time.After(time.Second):
			return // поток событий завис — не освобождаем объекты, которые он ещё использует
		}
	}
	for range e.out {
	}
	e.free()
}

func (e *h264Enc) free() {
	for _, o := range []**comObj{&e.codec, &e.gen, &e.mft, &e.act} {
		if *o != nil {
			(*o).release()
			*o = nil
		}
	}
}

// hasIDR — есть ли в потоке Annex B кадр IDR (NAL типа 5).
func hasIDR(b []byte) bool {
	for i := 0; i+3 < len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			if b[i+3]&0x1f == 5 {
				return true
			}
			i += 2
		}
	}
	return false
}
