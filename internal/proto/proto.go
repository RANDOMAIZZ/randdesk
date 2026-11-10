// Package proto описывает сообщения между хабом и агентами.
package proto

import "encoding/json"

const Version = "0.4.2"

// Типы сообщений в управляющем WebSocket-канале агента.
const (
	TypeHello      = "hello"       // агент -> хаб: информация о системе
	TypeStats      = "stats"       // агент -> хаб: периодическая статистика
	TypeReq        = "req"         // хаб -> агент: RPC-запрос
	TypeResp       = "resp"        // агент -> хаб: ответ на RPC
	TypeStreamOpen = "stream_open" // хаб -> агент: открыть поток (терминал и т.п.)
	TypeHubs       = "hubs"        // хаб -> агент: актуальные адреса хаба (локальные и внешние)
)

// Msg — конверт для всех сообщений управляющего канала.
type Msg struct {
	Type   string          `json:"type"`
	ID     string          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type NIC struct {
	Name string   `json:"name"`
	MAC  string   `json:"mac"`
	IPs  []string `json:"ips"`
}

type SysInfo struct {
	Hostname        string `json:"hostname"`
	OS              string `json:"os"`
	Platform        string `json:"platform"`
	PlatformVersion string `json:"platform_version"`
	Arch            string `json:"arch"`
	CPUModel        string `json:"cpu_model"`
	CPUCores        int    `json:"cpu_cores"`
	CPUThreads      int    `json:"cpu_threads"`
	MemTotal        uint64 `json:"mem_total"`
	BootTime        uint64 `json:"boot_time"`
	NICs            []NIC  `json:"nics"`
	AgentVersion    string `json:"agent_version"`
	AgentUser       string `json:"agent_user"`
}

type Disk struct {
	Mount string `json:"mount"`
	FS    string `json:"fs"`
	Used  uint64 `json:"used"`
	Total uint64 `json:"total"`
}

type Stats struct {
	Time     int64   `json:"t"` // unix ms (проставляет хаб)
	CPU      float64 `json:"cpu"`
	MemUsed  uint64  `json:"mem_used"`
	MemTotal uint64  `json:"mem_total"`
	Disks    []Disk  `json:"disks"`
	NetRx    float64 `json:"net_rx"` // байт/с
	NetTx    float64 `json:"net_tx"` // байт/с
	Uptime   uint64  `json:"uptime"` // секунды
	Procs    int     `json:"procs"`
}

type Proc struct {
	PID     uint32  `json:"pid"`
	PPID    uint32  `json:"ppid"`
	Name    string  `json:"name"`
	User    string  `json:"user"`
	CPU     float64 `json:"cpu"`
	Mem     uint64  `json:"mem"`
	Threads uint32  `json:"threads"`
}

type Service struct {
	Name    string `json:"name"`
	Display string `json:"display"`
	State   string `json:"state"`
	PID     uint32 `json:"pid"`
}

type KillReq struct {
	PID  uint32 `json:"pid"`
	Tree bool   `json:"tree"`
}

type ServiceReq struct {
	Name   string `json:"name"`
	Action string `json:"action"` // start | stop | restart
}

type ExecReq struct {
	Shell   string `json:"shell"` // powershell | cmd
	Command string `json:"command"`
	Timeout int    `json:"timeout"` // секунды
}

type ExecResp struct {
	Output    string `json:"output"`
	ExitCode  int    `json:"exit_code"`
	Duration  int64  `json:"duration_ms"`
	TimedOut  bool   `json:"timed_out"`
	Truncated bool   `json:"truncated"`
}

type PowerReq struct {
	Action string `json:"action"` // shutdown | reboot | sleep | hibernate | cancel
}

type WolReq struct {
	MAC string `json:"mac"`
}

type StreamOpen struct {
	Session string `json:"session"`
	Kind    string `json:"kind"` // terminal
	Shell   string `json:"shell"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
}

// TermControl — текстовые управляющие сообщения в потоке терминала.
type TermControl struct {
	Type    string `json:"type"` // resize | error
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
	Message string `json:"message,omitempty"`
}

// HubList — адреса хаба в порядке предпочтения (сначала локальные).
type HubList struct {
	URLs []string `json:"urls"`
}

type GamepadReq struct {
	Disable bool `json:"disable"`
}

// SessionState — хаб -> помощник сеанса: что заблокировать и участвовать ли в общем буфере.
type SessionState struct {
	Type      string `json:"type"` // state
	Keyboard  bool   `json:"keyboard"`
	Mouse     bool   `json:"mouse"`
	Clipboard bool   `json:"clipboard"`
}

// SessionMsg — сообщения помощника сеанса (буфер обмена).
type SessionMsg struct {
	Type string `json:"type"` // clip
	Text string `json:"text,omitempty"`
}

type EnrollReq struct {
	Key      string `json:"key"`
	Hostname string `json:"hostname"`
}

type EnrollResp struct {
	DeviceID string `json:"device_id"`
	Token    string `json:"token"`
}

type OK struct {
	OK bool `json:"ok"`
}
