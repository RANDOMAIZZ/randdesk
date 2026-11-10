package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"randdesk/internal/ui"
)

// setupInTerminal — «Настроить ПК» без браузера: роль, логин и пароль вводятся в терминале.
func setupInTerminal(base string) {
	var st struct {
		NodeSetup bool `json:"node_setup"`
	}
	if err := getJSON(base+"/api/state", &st); err != nil {
		fmt.Println("Служба не отвечает:", err)
		return
	}
	if !st.NodeSetup {
		fmt.Println("\nЭтот ПК уже настроен. Страница RandDesk: " + base)
		return
	}
	fmt.Println("\n  Настройка ПК «" + hostname() + "»")
	fmt.Println("  1  Главный ПК (администратор) — отсюда управляют всеми ПК")
	fmt.Println("  2  Обычный ПК — подключится к главному с тем же логином и паролем")
	role := RoleMember
	if strings.TrimSpace(ui.ReadLine("\n  Выберите 1 или 2 [2]: ")) == "1" {
		role = RoleAdmin
	}
	var login, pass string
	for {
		login = strings.TrimSpace(ui.ReadLine("  Логин: "))
		if login != "" {
			break
		}
	}
	for {
		pass = readPassword("  Пароль (от 8 символов): ")
		if len(pass) < 8 {
			fmt.Println("  Пароль слишком короткий.")
			continue
		}
		if role == RoleAdmin && readPassword("  Повторите пароль: ") != pass {
			fmt.Println("  Пароли не совпадают.")
			continue
		}
		break
	}
	body, _ := json.Marshal(map[string]string{"role": role, "user": login, "password": pass})
	req, _ := http.NewRequest(http.MethodPost, base+"/api/node/setup", bytes.NewReader(body))
	req.Header.Set("X-Requested-With", "randdesk")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("  Ошибка:", err)
		return
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Println("  Ошибка:", strings.TrimSpace(string(data)))
		return
	}
	if role == RoleAdmin {
		fmt.Println("\n  Готово: это главный ПК. Панель администратора: " + base)
		fmt.Println("  На других ПК запустите RandDesk и введите тот же логин и пароль.")
		return
	}
	fmt.Println("\n  Готово. Ищу главный ПК…")
	for range 60 {
		time.Sleep(2 * time.Second)
		var s struct {
			Online bool   `json:"online"`
			Admin  string `json:"admin"`
			Error  string `json:"error"`
		}
		if getJSON(base+"/api/node/status", &s) == nil && s.Online {
			fmt.Println("  Подключено к главному ПК «" + s.Admin + "».")
			return
		}
	}
	fmt.Println("  Главный ПК пока не найден — поиск продолжится в фоне.")
	fmt.Println("  Состояние: " + base + " (или journalctl -u RandDesk)")
}

func readPassword(prompt string) string {
	fmt.Print(prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return ui.ReadLine("")
	}
	return string(b)
}

func getJSON(url string, v any) error {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}
