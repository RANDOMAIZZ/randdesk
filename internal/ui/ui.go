package ui

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"
)

var stdin = bufio.NewReader(os.Stdin)

// ReadLine читает строку, введённую пользователем.
func ReadLine(prompt string) string {
	fmt.Print(prompt)
	s, _ := stdin.ReadString('\n')
	return strings.TrimSpace(s)
}

// Pause ждёт Enter, если окно консоли закроется после выхода программы.
func Pause() {
	if OwnConsole() {
		ReadLine("\nНажмите Enter, чтобы закрыть окно…")
	}
}

// Fatalf — как log.Fatalf, но окно не исчезает раньше, чем пользователь прочтёт ошибку.
func Fatalf(format string, v ...any) {
	log.Printf(format, v...)
	Pause()
	os.Exit(1)
}

func Fatal(v ...any) {
	log.Print(v...)
	Pause()
	os.Exit(1)
}
