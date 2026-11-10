//go:build windows

package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"

	"randsec/internal/proto"
)

const maxOutput = 2 << 20

type limitedBuf struct {
	buf       bytes.Buffer
	truncated bool
}

func (l *limitedBuf) Write(p []byte) (int, error) {
	if room := maxOutput - l.buf.Len(); len(p) > room {
		if room > 0 {
			l.buf.Write(p[:room])
		}
		l.truncated = true
		return len(p), nil
	}
	return l.buf.Write(p)
}

func encodePS(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		b[i*2] = byte(v)
		b[i*2+1] = byte(v >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func runExec(r proto.ExecReq) proto.ExecResp {
	timeout := time.Duration(r.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if timeout > 10*time.Minute {
		timeout = 10 * time.Minute
	}
	if strings.TrimSpace(r.Command) == "" {
		return proto.ExecResp{Output: "пустая команда", ExitCode: -1}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var cmd *exec.Cmd
	// Отдельная скрытая консоль: без консоли chcp/OutputEncoding не действуют и
	// cmd и внешние утилиты пишут в OEM-кодировке (CP866) вместо UTF-8.
	attr := &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NEW_CONSOLE}
	switch r.Shell {
	case "cmd":
		// Команда пишется во временный .cmd: chcp в отдельной строке действует на
		// последующие строки (в одной строке cmd уже разобрал текст в старой кодировке),
		// и так же поддерживаются многострочные команды.
		f, err := os.CreateTemp("", "randsec-*.cmd")
		if err != nil {
			return proto.ExecResp{Output: "не удалось создать временный файл: " + err.Error(), ExitCode: -1}
		}
		body := strings.ReplaceAll(strings.ReplaceAll(r.Command, "\r\n", "\n"), "\n", "\r\n")
		_, werr := f.WriteString("@echo off\r\nchcp 65001>nul\r\n" + body + "\r\n")
		f.Close()
		defer os.Remove(f.Name())
		if werr != nil {
			return proto.ExecResp{Output: "не удалось записать временный файл: " + werr.Error(), ExitCode: -1}
		}
		cmd = exec.CommandContext(ctx, "cmd.exe")
		attr.CmdLine = `cmd.exe /d /c "` + f.Name() + `"`
	default:
		script := "$ProgressPreference='SilentlyContinue'; [Console]::OutputEncoding=[Text.Encoding]::UTF8; " +
			"& {\n" + r.Command + "\n} 2>&1 | Out-String -Stream -Width 250; if ($LASTEXITCODE) { exit $LASTEXITCODE }"
		cmd = exec.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive",
			"-ExecutionPolicy", "Bypass", "-EncodedCommand", encodePS(script))
	}
	cmd.SysProcAttr = attr
	out := &limitedBuf{}
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return killProcess(uint32(cmd.Process.Pid), true)
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second

	start := time.Now()
	err := cmd.Run()
	res := proto.ExecResp{
		Duration:  time.Since(start).Milliseconds(),
		Truncated: out.truncated,
		TimedOut:  errors.Is(ctx.Err(), context.DeadlineExceeded),
	}
	res.Output = strings.ToValidUTF8(out.buf.String(), "�")
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		res.ExitCode = ee.ExitCode()
	default:
		res.ExitCode = -1
		if res.Output != "" {
			res.Output += "\n"
		}
		res.Output += "ошибка запуска: " + err.Error()
	}
	return res
}
