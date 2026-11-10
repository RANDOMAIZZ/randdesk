//go:build linux

package agent

import (
	"encoding/json"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"

	"randsec/internal/proto"
)

// runTerminal — интерактивная оболочка (bash) в псевдотерминале.
func runTerminal(ws *websocket.Conn, so proto.StreamOpen) {
	var wmu sync.Mutex
	write := func(mt int, b []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		ws.SetWriteDeadline(time.Now().Add(30 * time.Second))
		return ws.WriteMessage(mt, b)
	}
	sh := unixShell("")
	cmd := exec.Command(sh, "-l")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "LANG=C.UTF-8")
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(so.Cols), Rows: uint16(so.Rows)})
	if err != nil {
		b, _ := json.Marshal(proto.TermControl{Type: "error", Message: "не удалось запустить оболочку: " + err.Error()})
		write(websocket.TextMessage, b)
		return
	}
	defer func() {
		cmd.Process.Kill()
		f.Close()
		cmd.Wait()
	}()

	outDone := make(chan struct{})
	go func() {
		defer close(outDone)
		buf := make([]byte, 32*1024)
		for {
			n, err := f.Read(buf)
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
				if _, err := f.Write(data); err != nil {
					inErr <- err
					return
				}
			case websocket.TextMessage:
				var c proto.TermControl
				if json.Unmarshal(data, &c) == nil && c.Type == "resize" && c.Cols > 0 && c.Rows > 0 && c.Cols < 1000 && c.Rows < 500 {
					pty.Setsize(f, &pty.Winsize{Cols: uint16(c.Cols), Rows: uint16(c.Rows)})
				}
			}
		}
	}()
	select {
	case <-outDone:
		write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "оболочка завершена"))
	case <-inErr:
	}
}
