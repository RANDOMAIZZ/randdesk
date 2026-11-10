package main

import (
	"fmt"
	"os"

	"randdesk/internal/agent"
	"randdesk/internal/hub"
	"randdesk/internal/node"
	"randdesk/internal/proto"
	"randdesk/internal/ui"
)

func usage() {
	fmt.Fprintf(os.Stderr, `RandDesk %s — удалённое управление ПК

Обычно достаточно запустить randdesk.exe двойным щелчком: он сам установится и откроет страницу «Настроить ПК».

Использование:
  randdesk install                                установить RandDesk на этот ПК (служба, от администратора)
  randdesk uninstall                              удалить RandDesk с этого ПК
  randdesk node console [-dir DIR] [-port 8420]   запустить без установки (для проверки)
  randdesk hub [-listen :8420] [-data DIR]       запустить хаб (сервер + веб-интерфейс)
  randdesk agent -hub URL -key KEY                привязать ПК и запустить агента в консоли
  randdesk agent install -hub URL -key KEY        привязать ПК и установить агента службой Windows (от администратора)
  randdesk agent uninstall                        удалить службу агента
  randdesk agent run                              запустить агента с сохранённой конфигурацией
  randdesk version                                показать версию
`, proto.Version)
}

func main() {
	if len(os.Args) < 2 {
		if ui.OwnConsole() { // запуск двойным щелчком — показываем меню, а не исчезающую справку
			menu()
			return
		}
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "hub":
		hub.Main(os.Args[2:])
	case "agent":
		agent.Main(os.Args[2:])
	case "node":
		node.Main(os.Args[2:])
	case "install":
		node.Install()
	case "uninstall":
		node.Uninstall()
	case "version", "-v", "--version":
		fmt.Println(proto.Version)
	default:
		usage()
		os.Exit(2)
	}
}
