package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Prueba real con el portapapeles de Wayland: COFRE_CLIP_TEST=1 go test -run Clip
func TestLaptopClipboardLive(t *testing.T) {
	if os.Getenv("COFRE_CLIP_TEST") == "" {
		t.Skip("solo con COFRE_CLIP_TEST=1")
	}
	prev, _ := exec.Command("wl-paste", "-n").Output()
	defer func() {
		c := exec.Command("wl-copy")
		c.Stdin = strings.NewReader(string(prev))
		c.Run()
	}()
	desktopIntegration = true
	defer func() { desktopIntegration = false }()
	keys := testKeys(t)
	st, _ := openStore(t.TempDir(), keys)
	s, _ := newServer(st, keys, tHost, 8443, tlsPaths{}, 1<<20)
	s.toLaptopClipboard(Item{Kind: "text", Text: "hola desde el Cofre 👋", From: "prueba"})
	time.Sleep(300 * time.Millisecond)
	got, _ := exec.Command("wl-paste", "-n").Output()
	if string(got) != "hola desde el Cofre 👋" {
		t.Fatalf("portapapeles: %q", got)
	}
}
