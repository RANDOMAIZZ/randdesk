package main

import (
	"fmt"
	"strings"

	"randdesk/internal/agent"
	"randdesk/internal/hub"
	"randdesk/internal/proto"
	"randdesk/internal/ui"
)

// menu — запуск двойным щелчком: вместо справки по параметрам предлагаем понятный выбор.
func menu() {
	for {
		fmt.Printf(`
  RandDesk %s — удалённое управление своими ПК

  1  Запустить хаб на этом ПК (главный ПК: здесь веб-интерфейс, к нему подключаются остальные)
  2  Подключить этот ПК к хабу (установить агента — нужны права администратора)
  3  Удалить агента с этого ПК
  0  Выход

`, proto.Version)
		switch ui.ReadLine("Выберите пункт и нажмите Enter: ") {
		case "1":
			fmt.Println()
			hub.Main(nil)
			return
		case "2":
			if installAgent() {
				return
			}
		case "3":
			runAdmin([]string{"agent", "uninstall"})
			return
		case "0", "":
			return
		default:
			fmt.Println("Нет такого пункта.")
		}
	}
}

// installAgent спрашивает данные привязки. Удобнее всего вставить целиком команду
// из окна «Добавить ПК» в веб-интерфейсе хаба.
func installAgent() bool {
	fmt.Println(`
Откройте на главном ПК веб-интерфейс хаба → «Добавить ПК» → скопируйте команду установки
и вставьте её сюда (правый щелчок мыши в этом окне — вставить).`)
	line := ui.ReadLine("\nКоманда или адрес хаба: ")
	if line == "" {
		return false
	}
	hubs, key, fp := parseInstall(line)
	if key == "" {
		key = ui.ReadLine("Ключ привязки (RD-XXXX-XXXX-XXXX-XXXX): ")
	}
	if fp == "" && strings.Contains(hubs, "https://") {
		fp = ui.ReadLine("Отпечаток сертификата хаба (из «Добавить ПК», можно пропустить): ")
	}
	if hubs == "" || key == "" {
		fmt.Println("Нужны адрес хаба и ключ привязки.")
		return false
	}
	args := []string{"agent", "install", "-hub", hubs, "-key", key}
	if fp != "" {
		args = append(args, "-fp", fp)
	}
	runAdmin(args)
	return true
}

// parseInstall достаёт -hub, -key и -fp из вставленной команды. Без -hub вся строка — адрес.
func parseInstall(line string) (hubs, key, fp string) {
	f := strings.Fields(strings.ReplaceAll(line, "\"", " "))
	for i := 0; i < len(f); i++ {
		next := func() string {
			if i+1 < len(f) {
				i++
				return f[i]
			}
			return ""
		}
		switch strings.TrimLeft(f[i], "-") {
		case "hub":
			if strings.HasPrefix(f[i], "-") {
				hubs = next()
			}
		case "key":
			if strings.HasPrefix(f[i], "-") {
				key = next()
			}
		case "fp":
			if strings.HasPrefix(f[i], "-") {
				fp = next()
			}
		}
	}
	if hubs == "" && !strings.Contains(line, " -") {
		hubs = strings.TrimSpace(line)
	}
	return hubs, key, fp
}

// runAdmin выполняет команду агента сразу или, без прав администратора, в новом окне через запрос UAC.
func runAdmin(args []string) {
	if ui.IsElevated() {
		agent.Main(args[1:])
		return
	}
	fmt.Println("\nНужны права администратора — Windows покажет запрос, нажмите «Да».\nУстановка продолжится в новом окне.")
	if err := ui.RunElevated(args); err != nil {
		fmt.Println("Не удалось получить права администратора:", err)
		ui.Pause()
	}
}
