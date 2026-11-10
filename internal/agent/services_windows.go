//go:build windows

package agent

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"randsec/internal/proto"
)

const selfServiceName = "RandDeskAgent"

func stateName(s uint32) string {
	switch s {
	case windows.SERVICE_STOPPED:
		return "stopped"
	case windows.SERVICE_START_PENDING:
		return "starting"
	case windows.SERVICE_STOP_PENDING:
		return "stopping"
	case windows.SERVICE_RUNNING:
		return "running"
	case windows.SERVICE_CONTINUE_PENDING:
		return "continuing"
	case windows.SERVICE_PAUSE_PENDING:
		return "pausing"
	case windows.SERVICE_PAUSED:
		return "paused"
	}
	return "unknown"
}

func listServices() ([]proto.Service, error) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT|windows.SC_MANAGER_ENUMERATE_SERVICE)
	if err != nil {
		return nil, err
	}
	defer windows.CloseServiceHandle(scm)

	size := uint32(256 * 1024)
	var buf []byte
	var needed, count uint32
	for tries := 0; ; tries++ {
		buf = make([]byte, size)
		var resume uint32
		err = windows.EnumServicesStatusEx(scm, windows.SC_ENUM_PROCESS_INFO, windows.SERVICE_WIN32,
			windows.SERVICE_STATE_ALL, &buf[0], size, &needed, &count, &resume, nil)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_MORE_DATA) || tries > 3 {
			return nil, err
		}
		size += needed + 16*1024
	}
	out := make([]proto.Service, 0, count)
	if count == 0 {
		return out, nil
	}
	entries := unsafe.Slice((*windows.ENUM_SERVICE_STATUS_PROCESS)(unsafe.Pointer(&buf[0])), count)
	for _, e := range entries {
		out = append(out, proto.Service{
			Name:    windows.UTF16PtrToString(e.ServiceName),
			Display: windows.UTF16PtrToString(e.DisplayName),
			State:   stateName(e.ServiceStatusProcess.CurrentState),
			PID:     e.ServiceStatusProcess.ProcessId,
		})
	}
	return out, nil
}

func controlService(name, action string) error {
	if name == "" {
		return errors.New("не указано имя службы")
	}
	if (strings.EqualFold(name, selfServiceName) || strings.EqualFold(name, "RandDesk")) && action != "start" {
		return errors.New("нельзя останавливать службу самого агента")
	}
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return err
	}
	defer windows.CloseServiceHandle(scm)
	np, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	h, err := windows.OpenService(scm, np, windows.SERVICE_START|windows.SERVICE_STOP|windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return fmt.Errorf("служба %s: %w", name, err)
	}
	defer windows.CloseServiceHandle(h)

	stop := func() error {
		var st windows.SERVICE_STATUS
		err := windows.ControlService(h, windows.SERVICE_CONTROL_STOP, &st)
		if err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return err
		}
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if err := windows.QueryServiceStatus(h, &st); err != nil {
				return err
			}
			if st.CurrentState == windows.SERVICE_STOPPED {
				return nil
			}
			time.Sleep(300 * time.Millisecond)
		}
		return errors.New("служба не остановилась за 20 с")
	}
	start := func() error {
		err := windows.StartService(h, 0, nil)
		if errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
			return nil
		}
		return err
	}
	switch action {
	case "start":
		return start()
	case "stop":
		return stop()
	case "restart":
		if err := stop(); err != nil {
			return err
		}
		return start()
	}
	return fmt.Errorf("неизвестное действие %q", action)
}
