//go:build !windows && !linux

package agent

import (
	"errors"

	"github.com/gorilla/websocket"

	"randsec/internal/proto"
)

// Пока агент поддерживает только Windows; заглушки позволяют собрать хаб на других ОС.

var errUnsupported = errors.New("функция пока доступна только на Windows")

type procCache struct{}

func newProcCache() *procCache                   { return &procCache{} }
func (c *procCache) list() ([]proto.Proc, error) { return nil, errUnsupported }
func killProcess(uint32, bool) error             { return errUnsupported }
func listServices() ([]proto.Service, error)     { return nil, errUnsupported }
func controlService(string, string) error        { return errUnsupported }
func runExec(proto.ExecReq) proto.ExecResp {
	return proto.ExecResp{Output: errUnsupported.Error(), ExitCode: -1}
}
func power(string) error                                      { return errUnsupported }
func runTerminal(*websocket.Conn, proto.StreamOpen)           {}
func isElevated() bool                                        { return false }
func startDesktopHelper(string, string, string, string) error { return errUnsupported }
func setGamepads(bool) error                                  { return errUnsupported }
func runSessionHelper(*Config)                                {}
func (a *Agent) superviseSessionHelper(<-chan struct{})       {}
func runDesktopHelper(*Config, string)                        {}
func protectFile(string)                                      {}
