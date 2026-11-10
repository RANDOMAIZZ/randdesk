//go:build windows

package agent

import (
	"encoding/json"
	"os"
	"sync"
	"time"
	"unsafe"

	"github.com/gorilla/websocket"
	"golang.org/x/sys/windows"

	"randdesk/internal/proto"
)

// pty — процесс, запущенный в псевдоконсоли Windows (ConPTY).
type pty struct {
	hpc     windows.Handle
	in      *os.File // запись -> stdin консоли
	out     *os.File // чтение <- вывод консоли
	proc    windows.Handle
	closeMu sync.Once
}

func startPty(cmdline string, cols, rows int) (*pty, error) {
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, err
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		windows.CloseHandle(inR)
		windows.CloseHandle(inW)
		return nil, err
	}
	closeAll := func() {
		for _, h := range []windows.Handle{inR, inW, outR, outW} {
			windows.CloseHandle(h)
		}
	}
	var hpc windows.Handle
	if err := windows.CreatePseudoConsole(windows.Coord{X: int16(cols), Y: int16(rows)}, inR, outW, 0, &hpc); err != nil {
		closeAll()
		return nil, err
	}
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		closeAll()
		return nil, err
	}
	defer attrs.Delete()
	// значение атрибута — сам HPCON (не указатель на него)
	hpcVal := *(*unsafe.Pointer)(unsafe.Pointer(&hpc))
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, hpcVal, unsafe.Sizeof(hpc)); err != nil {
		windows.ClosePseudoConsole(hpc)
		closeAll()
		return nil, err
	}
	si := &windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(*si))
	si.Flags = windows.STARTF_USESTDHANDLES // не наследовать stdio самого агента

	cmd, err := windows.UTF16PtrFromString(cmdline)
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		closeAll()
		return nil, err
	}
	var dir *uint16
	if home, err := os.UserHomeDir(); err == nil {
		dir, _ = windows.UTF16PtrFromString(home)
	}
	var pi windows.ProcessInformation
	err = windows.CreateProcess(nil, cmd, nil, nil, false,
		windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT, nil, dir, &si.StartupInfo, &pi)
	// концы каналов, переданные псевдоконсоли, нам больше не нужны
	windows.CloseHandle(inR)
	windows.CloseHandle(outW)
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		windows.CloseHandle(inW)
		windows.CloseHandle(outR)
		return nil, err
	}
	windows.CloseHandle(pi.Thread)
	return &pty{
		hpc:  hpc,
		in:   os.NewFile(uintptr(inW), "pty-in"),
		out:  os.NewFile(uintptr(outR), "pty-out"),
		proc: pi.Process,
	}, nil
}

func (p *pty) resize(cols, rows int) error {
	return windows.ResizePseudoConsole(p.hpc, windows.Coord{X: int16(cols), Y: int16(rows)})
}

// closeConsole закрывает псевдоконсоль; после этого чтение вывода получит EOF.
func (p *pty) closeConsole() {
	p.closeMu.Do(func() { windows.ClosePseudoConsole(p.hpc) })
}

func (p *pty) close() {
	windows.TerminateProcess(p.proc, 1)
	p.closeConsole()
	p.in.Close()
	p.out.Close()
	windows.CloseHandle(p.proc)
}

func shellCmdline(shell string) string {
	switch shell {
	case "cmd":
		return "cmd.exe"
	case "pwsh":
		return "pwsh.exe -NoLogo"
	default:
		return "powershell.exe -NoLogo"
	}
}

func runTerminal(ws *websocket.Conn, so proto.StreamOpen) {
	var wmu sync.Mutex
	write := func(mt int, b []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		ws.SetWriteDeadline(time.Now().Add(30 * time.Second))
		return ws.WriteMessage(mt, b)
	}
	p, err := startPty(shellCmdline(so.Shell), so.Cols, so.Rows)
	if err != nil {
		b, _ := json.Marshal(proto.TermControl{Type: "error", Message: "не удалось запустить оболочку: " + err.Error()})
		write(websocket.TextMessage, b)
		return
	}
	defer p.close()

	// процесс завершился -> закрываем консоль, чтобы читатель получил EOF
	go func() {
		windows.WaitForSingleObject(p.proc, windows.INFINITE)
		p.closeConsole()
	}()

	outDone := make(chan struct{})
	go func() {
		defer close(outDone)
		buf := make([]byte, 32*1024)
		for {
			n, err := p.out.Read(buf)
			if n > 0 {
				if werr := write(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	inErr := make(chan error, 1)
	go func() {
		for {
			mt, data, err := ws.ReadMessage()
			if err != nil {
				inErr <- err
				return
			}
			switch mt {
			case websocket.BinaryMessage:
				if _, err := p.in.Write(data); err != nil {
					inErr <- err
					return
				}
			case websocket.TextMessage:
				var c proto.TermControl
				if json.Unmarshal(data, &c) == nil && c.Type == "resize" && c.Cols > 0 && c.Rows > 0 && c.Cols < 1000 && c.Rows < 500 {
					p.resize(c.Cols, c.Rows)
				}
			}
		}
	}()

	select {
	case <-outDone: // оболочка завершилась
		write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "оболочка завершена"))
	case <-inErr: // браузер закрыл сеанс
	}
}
