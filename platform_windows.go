//go:build windows

package main

// Windows: Programador de tareas + mDNS del sistema + PowerShell.

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

const platformName = "Windows"

func defaultDataDir() string {
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return filepath.Join(d, "Cofre")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "AppData", "Local", "Cofre")
}

// Windows 10 (1803+) admite sockets Unix. La carpeta de LOCALAPPDATA solo es accesible por tu usuario.
func socketPath() string { return filepath.Join(defaultDataDir(), "cofre.sock") }

func listenLocal(path string) (net.Listener, error) { return net.Listen("unix", path) }

func localHostname() string {
	h, _ := os.Hostname()
	return strings.Split(h, ".")[0]
}

func openURL(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

const serviceHint = "Get-ScheduledTask Cofre   (en PowerShell)"

func freeSpace(dir string) (int64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, nil, nil); err != nil {
		return 0, err
	}
	return int64(free), nil
}

// En Windows los permisos Unix no existen: la carpeta de datos ya es privada de tu usuario.
func checkKeyPerms(string, fs.FileMode) error { return nil }

// ansiOK activa los códigos de color/cursor en la consola (Windows 10+).
func ansiOK(f *os.File) bool {
	h := windows.Handle(f.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return false
	}
	return windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}

// powershell ejecuta un script sin abrir ninguna ventana. Los datos van por
// variables de entorno, nunca dentro del script, para que no se puedan inyectar órdenes.
func powershell(ctx context.Context, script string, env []string, stdin io.Reader) error {
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-Command", script)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd.Run()
}

func clipboardImageTypes() map[string]bool {
	return map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true}
}

func setClipboard(ctx context.Context, mimeType string, r io.Reader) error {
	if mimeType == "text/plain" {
		return powershell(ctx,
			`[Console]::InputEncoding = [Text.Encoding]::UTF8; Set-Clipboard -Value ([Console]::In.ReadToEnd())`,
			nil, r)
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
	if !clipboardImageTypes()[mimeType] {
		return errors.New("tipo de imagen no soportado")
	}
	return powershell(ctx, `Add-Type -AssemblyName System.Windows.Forms, System.Drawing
$img = [System.Drawing.Image]::FromFile($env:COFRE_FILE)
[System.Windows.Forms.Clipboard]::SetImage($img)
$img.Dispose()`, []string{"COFRE_FILE=" + tmp.Name()}, nil)
}

func notify(ctx context.Context, title, body string) {
	// Notificación nativa de Windows 10/11, firmada como PowerShell (no requiere registrar una app).
	powershell(ctx, `[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null
$x = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$t = $x.GetElementsByTagName('text')
$t.Item(0).AppendChild($x.CreateTextNode($env:COFRE_T)) > $null
$t.Item(1).AppendChild($x.CreateTextNode($env:COFRE_B)) > $null
$app = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe'
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($app).Show([Windows.UI.Notifications.ToastNotification]::new($x))`,
		[]string{"COFRE_T=" + title, "COFRE_B=" + body}, nil)
}

func caTrustHint(caPath string) string {
	return `Import-Certificate -FilePath "` + caPath + `" -CertStoreLocation Cert:\CurrentUser\Root   (PowerShell; Chrome/Edge)`
}
