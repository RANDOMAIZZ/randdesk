package main

import (
	"fmt"
	"runtime"
	"strings"

	"randsec/internal/node"
	"randsec/internal/proto"
	"randsec/internal/ui"
)

const localURL = "http://localhost:8420"

// menu — запуск двойным щелчком. Не установлен — устанавливаем (с запросом прав администратора)
// и открываем страницу «Настроить ПК». Установлен — открываем страницу RANDSEC.
func menu() {
	fmt.Printf("\n  RANDSEC %s — удалённое управление своими ПК\n\n", proto.Version)
	if !node.Installed() {
		if ui.IsElevated() {
			node.Install()
			return
		}
		fmt.Println("  Сейчас RANDSEC установится на этот ПК.")
		if runtime.GOOS == "windows" {
			fmt.Println("  Windows спросит разрешение на изменения — нажмите «Да».")
		} else {
			fmt.Println("  Нужны права root — введите пароль sudo, если система спросит.")
		}
		if err := ui.RunElevated([]string{"node", "install"}); err != nil {
			fmt.Println("\n  Не удалось получить права администратора:", err)
			ui.Pause()
		}
		return
	}
	if runtime.GOOS == "windows" {
		ui.OpenBrowser(localURL)
		fmt.Println("  RANDSEC уже установлен и работает. Страница открыта в браузере:", localURL)
	} else {
		fmt.Println("  RANDSEC уже установлен и работает. Страница:", localURL, "(служба: systemctl status RandDesk)")
	}
	fmt.Println()
	fmt.Println("  Enter — закрыть это окно")
	fmt.Println("  У     — удалить RANDSEC с этого ПК")
	switch strings.ToLower(ui.ReadLine("\n  > ")) {
	case "у", "y", "u", "удалить":
		if ui.IsElevated() {
			node.Uninstall()
			return
		}
		if runtime.GOOS == "windows" {
			fmt.Println("  Windows спросит разрешение — нажмите «Да».")
		}
		if err := ui.RunElevated([]string{"node", "uninstall"}); err != nil {
			fmt.Println("  Не удалось получить права администратора:", err)
			ui.Pause()
		}
	}
}
