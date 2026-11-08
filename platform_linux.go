//go:build linux

package main

// Linux: systemd + Avahi + Wayland.

import (
	"context"
	"errors"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const platformName = "Linux"

func defaultDataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "cofre")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "cofre")
}

func socketPath() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "cofre.sock")
	}
	return filepath.Join(defaultDataDir(), "cofre.sock")
}

// Avahi publica el nombre del equipo como <hostname>.local.
func localHostname() string {
	h, _ := os.Hostname()
	return strings.Split(h, ".")[0]
}

func openURL(url string) error { return exec.Command("xdg-open", url).Start() }

const serviceHint = "systemctl --user status cofre"

// setClipboard copia al portapapeles de Wayland.
func setClipboard(ctx context.Context, mimeType string, r io.Reader) error {
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		return errors.New("sin sesión Wayland")
	}
	if _, err := exec.LookPath("wl-copy"); err != nil {
		return errors.New("falta wl-copy (paquete wl-clipboard)")
	}
	if mimeType == "text/plain" {
		mimeType = "text/plain;charset=utf-8"
	}
	cmd := exec.CommandContext(ctx, "wl-copy", "--type", mimeType)
	cmd.Stdin = r
	return cmd.Run()
}

func clipboardImageTypes() map[string]bool {
	return map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true}
}

func notify(ctx context.Context, title, body string) {
	if _, err := exec.LookPath("notify-send"); err == nil {
		exec.CommandContext(ctx, "notify-send", "-a", "Cofre", "-i", "edit-paste", "-t", "4000", title, html.EscapeString(body)).Run()
	}
}

func caTrustHint(caPath string) string {
	return "certutil -d sql:$HOME/.pki/nssdb -A -t \"C,,\" -n \"Cofre CA\" -i " + caPath + "   (Chrome/Chromium/Brave)"
}
