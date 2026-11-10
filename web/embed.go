// Package web содержит встроенный веб-интерфейс.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var files embed.FS

// FS возвращает файловую систему со статикой (корень = static/).
func FS() fs.FS {
	sub, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
