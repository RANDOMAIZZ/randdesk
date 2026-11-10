package hub

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"randsec/internal/proto"
)

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("randdesk-dummy"), bcrypt.DefaultCost)

type User struct {
	Name       string    `json:"name"`
	PassHash   string    `json:"pass_hash"`
	Created    time.Time `json:"created,omitempty"`
	TOTPSecret string    `json:"totp_secret,omitempty"` // base32; пусто — 2FA выключена
	TOTPLast   int64     `json:"totp_last,omitempty"`   // последний использованный шаг (защита от повтора)
	Recovery   []string  `json:"recovery,omitempty"`    // хэши неиспользованных резервных кодов
}

// UserView — сведения о пользователе без секретов.
type UserView struct {
	Name         string    `json:"name"`
	Created      time.Time `json:"created"`
	TOTP         bool      `json:"totp"`
	RecoveryLeft int       `json:"recovery_left"`
}

// Settings — настройки хаба, меняемые из веб-интерфейса.
type Settings struct {
	UPnP       bool   `json:"upnp"`        // автоматически пробрасывать порт на роутере
	PublicHost string `json:"public_host"` // свой внешний адрес/домен (DDNS), host[:port]
	Touched    bool   `json:"touched"`     // пользователь менял настройки вручную
	ClipShared bool   `json:"clip_shared"` // единый буфер обмена между ПК
	Indicator  string `json:"indicator"`   // плашка на управляемом ПК: top, top-left, …, off
}

type Device struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	TokenHash string        `json:"token_hash"`
	Created   time.Time     `json:"created"`
	LastSeen  time.Time     `json:"last_seen"`
	LastIP    string        `json:"last_ip"`
	Info      proto.SysInfo `json:"info"`
	MachineID string        `json:"machine_id,omitempty"` // для привязки по логину: один ПК — одна запись
	Controls  []string      `json:"controls,omitempty"`   // ID устройств, которыми этому ПК разрешено управлять
	Lock      InputLock     `json:"lock"`                 // отключённые администратором устройства ввода
	NoClip    bool          `json:"no_clip,omitempty"`    // не участвует в едином буфере обмена
}

// InputLock — какие устройства ввода отключены на ПК.
type InputLock struct {
	Keyboard bool `json:"keyboard"`
	Mouse    bool `json:"mouse"`
	Gamepad  bool `json:"gamepad"`
}

type storeData struct {
	Users     []User    `json:"users"`
	EnrollKey string    `json:"enroll_key"`
	Devices   []*Device `json:"devices"`
	Settings  Settings  `json:"settings"`
}

// Store — простое JSON-хранилище хаба (аккаунт, ключ привязки, устройства).
type Store struct {
	mu   sync.Mutex
	path string
	d    storeData
}

func OpenStore(path string) (*Store, error) {
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &s.d); err != nil {
			return nil, fmt.Errorf("чтение %s: %w", path, err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	if s.d.EnrollKey == "" {
		s.d.EnrollKey = newEnrollKey()
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.d, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// ---------- пользователи ----------

func (s *Store) HasUsers() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.d.Users) > 0
}

func (s *Store) userLocked(name string) *User {
	for i := range s.d.Users {
		if strings.EqualFold(s.d.Users[i].Name, name) {
			return &s.d.Users[i]
		}
	}
	return nil
}

// CreateUser добавляет пользователя. onlyFirst — только если пользователей ещё нет
// (первичная настройка; проверка и запись под одной блокировкой, без гонки).
func (s *Store) CreateUser(name, pass string, onlyFirst bool) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if onlyFirst && len(s.d.Users) > 0 {
		return errors.New("администратор уже создан")
	}
	if s.userLocked(name) != nil {
		return errors.New("пользователь уже существует")
	}
	s.d.Users = append(s.d.Users, User{Name: name, PassHash: string(hash), Created: time.Now()})
	return s.saveLocked()
}

func (s *Store) DeleteUser(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.d.Users) <= 1 {
		return errors.New("нельзя удалить последнего пользователя")
	}
	for i := range s.d.Users {
		if strings.EqualFold(s.d.Users[i].Name, name) {
			s.d.Users = append(s.d.Users[:i], s.d.Users[i+1:]...)
			return s.saveLocked()
		}
	}
	return errors.New("пользователь не найден")
}

func (s *Store) Users() []UserView {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]UserView, 0, len(s.d.Users))
	for _, u := range s.d.Users {
		out = append(out, viewUser(u))
	}
	return out
}

func (s *Store) User(name string) (UserView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u := s.userLocked(name); u != nil {
		return viewUser(*u), true
	}
	return UserView{}, false
}

func viewUser(u User) UserView {
	return UserView{Name: u.Name, Created: u.Created, TOTP: u.TOTPSecret != "", RecoveryLeft: len(u.Recovery)}
}

// CheckUser проверяет пароль; возвращает каноническое имя пользователя.
func (s *Store) CheckUser(name, pass string) (string, bool) {
	s.mu.Lock()
	var hash, canon string
	if u := s.userLocked(name); u != nil {
		hash, canon = u.PassHash, u.Name
	}
	s.mu.Unlock()
	if hash == "" {
		// выравниваем время ответа, чтобы не выдавать существование пользователя
		bcrypt.CompareHashAndPassword(dummyHash, []byte(pass))
		return "", false
	}
	return canon, bcrypt.CompareHashAndPassword([]byte(hash), []byte(pass)) == nil
}

func (s *Store) SetPassword(name, pass string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if u := s.userLocked(name); u != nil {
		u.PassHash = string(hash)
		return s.saveLocked()
	}
	return errors.New("пользователь не найден")
}

// ---------- двухфакторная аутентификация ----------

func (s *Store) EnableTOTP(name, secret string, recoveryHashes []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.userLocked(name)
	if u == nil {
		return errors.New("пользователь не найден")
	}
	u.TOTPSecret, u.TOTPLast, u.Recovery = secret, time.Now().Unix()/totpPeriod, recoveryHashes
	return s.saveLocked()
}

func (s *Store) DisableTOTP(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.userLocked(name)
	if u == nil {
		return errors.New("пользователь не найден")
	}
	u.TOTPSecret, u.TOTPLast, u.Recovery = "", 0, nil
	return s.saveLocked()
}

func (s *Store) SetRecovery(name string, hashes []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.userLocked(name)
	if u == nil || u.TOTPSecret == "" {
		return errors.New("двухфакторная аутентификация не включена")
	}
	u.Recovery = hashes
	return s.saveLocked()
}

// CheckSecondFactor проверяет код из приложения или одноразовый резервный код.
// Использованный код больше не принимается.
func (s *Store) CheckSecondFactor(name, code string) (ok, usedRecovery bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.userLocked(name)
	if u == nil || u.TOTPSecret == "" {
		return false, false
	}
	if step, good := checkTOTP(u.TOTPSecret, code, u.TOTPLast, time.Now()); good {
		u.TOTPLast = step
		s.saveLocked()
		return true, false
	}
	if len(normRecovery(code)) == 8 {
		h := hashRecovery(code)
		for i, r := range u.Recovery {
			if subtle.ConstantTimeCompare([]byte(r), []byte(h)) == 1 {
				u.Recovery = append(u.Recovery[:i], u.Recovery[i+1:]...)
				s.saveLocked()
				return true, true
			}
		}
	}
	return false, false
}

// ---------- настройки ----------

func (s *Store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.Settings
}

func (s *Store) SetSettings(v Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Settings = v
	return s.saveLocked()
}

// ---------- ключ привязки ----------

func (s *Store) EnrollKey() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.EnrollKey
}

func (s *Store) RotateEnrollKey() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.EnrollKey = newEnrollKey()
	return s.d.EnrollKey, s.saveLocked()
}

// ---------- устройства ----------

func (s *Store) Enroll(key, hostname, ip string) (proto.EnrollResp, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(key)), []byte(s.d.EnrollKey)) != 1 {
		return proto.EnrollResp{}, errors.New("неверный ключ привязки")
	}
	token := randToken(32)
	name := strings.TrimSpace(hostname)
	if name == "" {
		name = "ПК"
	}
	d := &Device{
		ID:        hex.EncodeToString(randBytes(8)),
		Name:      name,
		TokenHash: hashToken(token),
		Created:   time.Now(),
		LastSeen:  time.Now(),
		LastIP:    ip,
	}
	s.d.Devices = append(s.d.Devices, d)
	if err := s.saveLocked(); err != nil {
		return proto.EnrollResp{}, err
	}
	return proto.EnrollResp{DeviceID: d.ID, Token: token}, nil
}

// Pair — устройство по постоянному идентификатору ПК: повторная привязка того же ПК
// выдаёт новый токен существующей записи, а не создаёт дубль.
func (s *Store) Pair(machine, hostname, ip string) (proto.EnrollResp, error) {
	machine = normMachine(machine)
	s.mu.Lock()
	defer s.mu.Unlock()
	token := randToken(32)
	name := strings.TrimSpace(hostname)
	if name == "" {
		name = "ПК"
	}
	for _, d := range s.d.Devices {
		if machine != "" && d.MachineID == machine {
			d.TokenHash = hashToken(token)
			d.LastIP = ip
			if err := s.saveLocked(); err != nil {
				return proto.EnrollResp{}, err
			}
			return proto.EnrollResp{DeviceID: d.ID, Token: token}, nil
		}
	}
	d := &Device{
		ID:        hex.EncodeToString(randBytes(8)),
		Name:      name,
		TokenHash: hashToken(token),
		Created:   time.Now(),
		LastSeen:  time.Now(),
		LastIP:    ip,
		MachineID: machine,
	}
	s.d.Devices = append(s.d.Devices, d)
	if err := s.saveLocked(); err != nil {
		return proto.EnrollResp{}, err
	}
	return proto.EnrollResp{DeviceID: d.ID, Token: token}, nil
}

func (s *Store) AuthDevice(id, token string) (Device, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.d.Devices {
		if d.ID == id {
			if subtle.ConstantTimeCompare([]byte(hashToken(token)), []byte(d.TokenHash)) == 1 {
				return *d, true
			}
			return Device{}, false
		}
	}
	return Device{}, false
}

func (s *Store) Devices() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Device, 0, len(s.d.Devices))
	for _, d := range s.d.Devices {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func (s *Store) Device(id string) (Device, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.d.Devices {
		if d.ID == id {
			return *d, true
		}
	}
	return Device{}, false
}

func (s *Store) UpdateInfo(id string, info proto.SysInfo, ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.d.Devices {
		if d.ID == id {
			d.Info = info
			d.LastIP = ip
			d.LastSeen = time.Now()
			s.saveLocked()
			return
		}
	}
}

func (s *Store) Touch(id, ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.d.Devices {
		if d.ID == id {
			d.LastSeen = time.Now()
			if ip != "" {
				d.LastIP = ip
			}
			s.saveLocked()
			return
		}
	}
}

// UpdateDevice изменяет запись устройства под блокировкой.
func (s *Store) UpdateDevice(id string, fn func(d *Device)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.d.Devices {
		if d.ID == id {
			fn(d)
			return s.saveLocked()
		}
	}
	return errors.New("устройство не найдено")
}

// CanControl — разрешено ли ПК from управлять ПК to.
func (s *Store) CanControl(from, to string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.d.Devices {
		if d.ID == from {
			for _, c := range d.Controls {
				if c == to {
					return true
				}
			}
		}
	}
	return false
}

func (s *Store) Rename(id, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.d.Devices {
		if d.ID == id {
			d.Name = name
			return s.saveLocked()
		}
	}
	return errors.New("устройство не найдено")
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range s.d.Devices {
		if d.ID == id {
			s.d.Devices = append(s.d.Devices[:i], s.d.Devices[i+1:]...)
			for _, o := range s.d.Devices { // убираем удалённый ПК из разрешений
				o.Controls = removeStr(o.Controls, id)
			}
			return s.saveLocked()
		}
	}
	return errors.New("устройство не найдено")
}

// ---------- утилиты ----------

func removeStr(list []string, v string) []string {
	out := list[:0]
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func randToken(n int) string { return base64.RawURLEncoding.EncodeToString(randBytes(n)) }

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func newEnrollKey() string {
	raw := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(randBytes(10)) // 16 символов
	return "RD-" + raw[0:4] + "-" + raw[4:8] + "-" + raw[8:12] + "-" + raw[12:16]
}
