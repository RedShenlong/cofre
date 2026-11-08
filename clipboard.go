package main

// Copia al portapapeles del portátil (Wayland) lo que llega desde otros dispositivos.

import (
	"context"
	"html"
	"io"
	"log"
	"mime"
	"os"
	"os/exec"
	"strings"
	"time"
)

var clipboardImages = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true}

func (s *Server) toLaptopClipboard(it Item) {
	var off bool
	s.store.view(func(st *State) { off = st.Settings.ClipboardOff })
	if off || os.Getenv("WAYLAND_DISPLAY") == "" {
		return
	}
	if _, err := exec.LookPath("wl-copy"); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		src   io.Reader
		args  []string
		title string
		body  string
	)
	switch it.Kind {
	case "text":
		src = strings.NewReader(it.Text)
		args = []string{"--type", "text/plain;charset=utf-8"}
		title = "📋 Texto copiado desde " + it.From
		body = it.Text
		if r := []rune(body); len(r) > 120 {
			body = string(r[:120]) + "…"
		}
	case "file":
		base, _, _ := mime.ParseMediaType(it.Mime)
		if !clipboardImages[base] || it.Size > 50<<20 {
			return
		}
		rd, err := s.store.OpenFile(it)
		if err != nil {
			return
		}
		defer rd.Close()
		src = rd
		args = []string{"--type", base}
		title = "🖼️ Imagen copiada desde " + it.From
		body = it.Name
	default:
		return
	}

	cmd := exec.CommandContext(ctx, "wl-copy", args...)
	cmd.Stdin = src
	if err := cmd.Run(); err != nil {
		log.Printf("no se pudo copiar al portapapeles: %v", err)
		return
	}
	if _, err := exec.LookPath("notify-send"); err == nil {
		exec.CommandContext(ctx, "notify-send", "-a", "Cofre", "-i", "edit-paste", "-t", "4000", title, html.EscapeString(body)).Run()
	}
}
