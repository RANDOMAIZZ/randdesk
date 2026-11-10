package main

import (
	"fmt"
	"strings"

	"randdesk/internal/node"
	"randdesk/internal/proto"
	"randdesk/internal/ui"
)

const localURL = "http://localhost:8420"

// menu — запуск двойным щелчком. Не установлен — устанавливаем (с запросом прав администратора)
// и открываем страницу «Настроить ПК». Установлен — открываем страницу RandDesk.
func menu() {
	fmt.Printf("\n  RandDesk %s — удалённое управление своими ПК\n\n", proto.Version)
	if !node.Installed() {
		if ui.IsElevated() {
			node.Install()
			return
		}
		fmt.Println("  Сейчас RandDesk установится на этот ПК.")
		fmt.Println("  Windows спросит разрешение на изменения — нажмите «Да».")
		if err := ui.RunElevated([]string{"node", "install"}); err != nil {
			fmt.Println("\n  Не удалось получить права администратора:", err)
			ui.Pause()
		}
		return
	}
	ui.OpenBrowser(localURL)
	fmt.Println("  RandDesk уже установлен и работает. Страница открыта в браузере:", localURL)
	fmt.Println()
	fmt.Println("  Enter — закрыть это окно")
	fmt.Println("  У     — удалить RandDesk с этого ПК")
	switch strings.ToLower(ui.ReadLine("\n  > ")) {
	case "у", "y", "u", "удалить":
		if ui.IsElevated() {
			node.Uninstall()
			return
		}
		fmt.Println("  Windows спросит разрешение — нажмите «Да».")
		if err := ui.RunElevated([]string{"node", "uninstall"}); err != nil {
			fmt.Println("  Не удалось получить права администратора:", err)
			ui.Pause()
		}
	}
}
