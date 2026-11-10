package hub

import (
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"randdesk/internal/pair"
)

// hAgentPair — привязка ПК по общему логину и паролю (режим узла), без ключа привязки.
func (s *Server) hAgentPair(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.node == nil {
		jsonErr(w, http.StatusNotFound, "этот хаб не поддерживает привязку по логину")
		return
	}
	if wait := s.enrollLimiter.blocked(ip); wait > 0 {
		jsonErr(w, http.StatusTooManyRequests, "слишком много попыток")
		return
	}
	acc := s.node.Account()
	if !acc.Valid() {
		jsonErr(w, http.StatusConflict, "этот ПК ещё не настроен")
		return
	}
	var req pair.Request
	if err := readJSON(r, &req); err != nil {
		jsonErr(w, 400, "неверный запрос")
		return
	}
	nonce, err := hex.DecodeString(req.Nonce)
	fresh := time.Since(time.Unix(req.TS, 0)).Abs() < 5*time.Minute
	if err != nil || len(nonce) < 16 || !fresh || req.FP != s.fingerprint || !acc.CheckRequest(req) || !s.useNonce(req.Nonce) {
		s.enrollLimiter.fail(ip)
		s.audit.Log("", ip, req.Host, "pair_failed", "")
		jsonErr(w, http.StatusForbidden, "отказано: логин или пароль на ПК не совпадают")
		return
	}
	er, err := s.store.Pair(req.Machine, req.Host, ip)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	host, _ := os.Hostname()
	resp := pair.Response{DeviceID: er.DeviceID, Token: er.Token, Name: host}
	acc.SignResponse(req.Nonce, &resp)
	s.enrollLimiter.reset(ip)
	s.audit.Log("", ip, req.Host, "device_paired", er.DeviceID)
	log.Printf("подключён ПК %s (%s)", req.Host, ip)
	writeJSON(w, 200, resp)
}

// useNonce — защита от повторной отправки перехваченного запроса.
func (s *Server) useNonce(n string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nonces == nil {
		s.nonces = map[string]time.Time{}
	}
	if _, ok := s.nonces[n]; ok {
		return false
	}
	now := time.Now()
	for k, t := range s.nonces {
		if now.Sub(t) > 10*time.Minute {
			delete(s.nonces, k)
		}
	}
	s.nonces[n] = now
	return true
}

func normMachine(m string) string {
	m = strings.TrimSpace(m)
	if len(m) > 64 {
		m = m[:64]
	}
	return m
}
