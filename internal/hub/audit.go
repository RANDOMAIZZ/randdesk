package hub

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"time"
)

type AuditEntry struct {
	Time   time.Time `json:"time"`
	User   string    `json:"user"`
	IP     string    `json:"ip"`
	Device string    `json:"device"`
	Action string    `json:"action"`
	Detail string    `json:"detail"`
}

const auditKeep = 1000

// Audit пишет журнал действий в JSONL-файл и держит последние записи в памяти.
type Audit struct {
	mu     sync.Mutex
	f      *os.File
	recent []AuditEntry
}

func OpenAudit(path string) (*Audit, error) {
	a := &Audit{}
	if rf, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(rf)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			var e AuditEntry
			if json.Unmarshal(sc.Bytes(), &e) == nil {
				a.recent = append(a.recent, e)
				if len(a.recent) > auditKeep*2 {
					a.recent = append([]AuditEntry(nil), a.recent[len(a.recent)-auditKeep:]...)
				}
			}
		}
		rf.Close()
		if len(a.recent) > auditKeep {
			a.recent = append([]AuditEntry(nil), a.recent[len(a.recent)-auditKeep:]...)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	a.f = f
	return a, nil
}

func (a *Audit) Log(user, ip, device, action, detail string) {
	if len(detail) > 2000 {
		detail = detail[:2000] + "…"
	}
	e := AuditEntry{Time: time.Now(), User: user, IP: ip, Device: device, Action: action, Detail: detail}
	b, _ := json.Marshal(e)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.f.Write(append(b, '\n'))
	a.recent = append(a.recent, e)
	if len(a.recent) > auditKeep+200 {
		a.recent = append([]AuditEntry(nil), a.recent[len(a.recent)-auditKeep:]...)
	}
}

// Recent возвращает записи от новых к старым.
func (a *Audit) Recent(limit int) []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]AuditEntry, 0, limit)
	for i := len(a.recent) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, a.recent[i])
	}
	return out
}
