package zt

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Installed — установлена ли служба ZeroTier One (есть ключ локального API).
func Installed() bool {
	_, err := os.Stat(authTokenPath())
	return err == nil
}

func authTokenPath() string {
	if runtime.GOOS == "windows" {
		d := os.Getenv("ProgramData")
		if d == "" {
			d = `C:\ProgramData`
		}
		return filepath.Join(d, "ZeroTier", "One", "authtoken.secret")
	}
	return "/var/lib/zerotier-one/authtoken.secret"
}

// download сохраняет файл с официального сайта ZeroTier во временную папку.
func download(ctx context.Context, url, name string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	c := &http.Client{Timeout: 5 * time.Minute}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("загрузка %s: %s", url, resp.Status)
	}
	dir, err := os.MkdirTemp("", "randdesk-zt-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 200<<20)); err != nil {
		f.Close()
		return "", err
	}
	return path, f.Close()
}

// EnsureInstalled ставит ZeroTier One, если его ещё нет, и ждёт запуска службы.
func EnsureInstalled(ctx context.Context) error {
	if Installed() {
		return nil
	}
	log.Printf("ZeroTier: установка…")
	if err := install(ctx); err != nil {
		return fmt.Errorf("установка ZeroTier: %w", err)
	}
	for range 60 {
		if Installed() {
			log.Printf("ZeroTier: установлен")
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("ZeroTier установлен, но служба не запустилась")
}
