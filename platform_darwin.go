//go:build darwin

package main

// macOS: launchd + Bonjour + pbcopy/AppleScript.

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const platformName = "macOS"

func defaultDataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "Cofre")
}

func socketPath() string { return filepath.Join(defaultDataDir(), "cofre.sock") }

// Bonjour anuncia el equipo con su "LocalHostName" (Ajustes → General → Compartir).
func localHostname() string {
	if out, err := exec.Command("scutil", "--get", "LocalHostName").Output(); err == nil {
		if h := strings.TrimSpace(string(out)); h != "" {
			return h
		}
	}
	h, _ := os.Hostname()
	return strings.Split(h, ".")[0]
}

func openURL(url string) error { return exec.Command("open", url).Start() }

const serviceHint = "launchctl print gui/$(id -u)/com.redshenlong.cofre"

func clipboardImageTypes() map[string]bool {
	return map[string]bool{"image/png": true, "image/jpeg": true}
}

func setClipboard(ctx context.Context, mimeType string, r io.Reader) error {
	if mimeType == "text/plain" {
		cmd := exec.CommandContext(ctx, "pbcopy")
		cmd.Env = append(os.Environ(), "LANG=en_US.UTF-8") // pbcopy necesita UTF-8 para tildes y emojis
		cmd.Stdin = r
		return cmd.Run()
	}
	// Las imágenes se copian con AppleScript a partir de un archivo temporal.
	class := map[string]string{"image/png": "«class PNGf»", "image/jpeg": "JPEG picture"}[mimeType]
	if class == "" {
		return errors.New("tipo de imagen no soportado")
	}
	tmp, err := os.CreateTemp("", "cofre-clip-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	return exec.CommandContext(ctx, "osascript",
		"-e", "on run argv",
		"-e", "set the clipboard to (read (POSIX file (item 1 of argv)) as "+class+")",
		"-e", "end run", tmp.Name()).Run()
}

func notify(ctx context.Context, title, body string) {
	// Los textos van como argumentos (no dentro del script) para que no se puedan inyectar órdenes.
	exec.CommandContext(ctx, "osascript",
		"-e", "on run argv",
		"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
		"-e", "end run", title, body).Run()
}

func caTrustHint(caPath string) string {
	return "security add-trusted-cert -r trustRoot -k ~/Library/Keychains/login.keychain-db \"" + caPath + "\"   (Safari/Chrome)"
}
