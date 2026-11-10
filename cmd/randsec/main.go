package main

import (
	"fmt"
	"os"

	"randsec/internal/agent"
	"randsec/internal/hub"
	"randsec/internal/node"
	"randsec/internal/proto"
	"randsec/internal/ui"
)

func usage() {
	fmt.Fprintf(os.Stderr, `RANDSEC %s — удалённое управление ПК

Обычно достаточно запустить randsec.exe двойным щелчком: он сам установится и откроет страницу «Настроить ПК».

Использование:
  randsec install                                установить RANDSEC на этот ПК (служба, от администратора)
  randsec uninstall                              удалить RANDSEC с этого ПК
  randsec node console [-dir DIR] [-port 8420]   запустить без установки (для проверки)
  randsec hub [-listen :8420] [-data DIR]       запустить хаб (сервер + веб-интерфейс)
  randsec agent -hub URL -key KEY                привязать ПК и запустить агента в консоли
  randsec agent install -hub URL -key KEY        привязать ПК и установить агента службой Windows (от администратора)
  randsec agent uninstall                        удалить службу агента
  randsec agent run                              запустить агента с сохранённой конфигурацией
  randsec version                                показать версию
`, proto.Version)
}

func main() {
	if len(os.Args) < 2 {
		if ui.OwnConsole() || ui.IsTerminal() { // двойной щелчок (Windows) или запуск в терминале (Linux) — меню вместо справки
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
