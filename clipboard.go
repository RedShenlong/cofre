package main

// Copia al portapapeles del ordenador lo que llega desde otros dispositivos.
// La forma concreta de hacerlo está en platform_<sistema>.go.

import (
	"context"
	"io"
	"log"
	"mime"
	"strings"
	"time"
)

// desktopIntegration permite desactivar portapapeles y notificaciones
// (las pruebas lo apagan para no tocar el escritorio de quien las ejecuta).
var desktopIntegration = true

func (s *Server) toLaptopClipboard(it Item) {
	if !desktopIntegration {
		return
	}
	var off bool
	s.store.view(func(st *State) { off = st.Settings.ClipboardOff })
	if off {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		src         io.Reader
		mimeType    string
		title, body string
	)
	switch it.Kind {
	case "text":
		src, mimeType = strings.NewReader(it.Text), "text/plain"
		title = "📋 Texto copiado desde " + it.From
		body = it.Text
		if r := []rune(body); len(r) > 120 {
			body = string(r[:120]) + "…"
		}
	case "file":
		base, _, _ := mime.ParseMediaType(it.Mime)
		if !clipboardImageTypes()[base] || it.Size > 50<<20 {
			return
		}
		rd, err := s.store.OpenFile(it)
		if err != nil {
			return
		}
		defer rd.Close()
		src, mimeType = rd, base
		title = "🖼️ Imagen copiada desde " + it.From
		body = it.Name
	default:
		return
	}
	if err := setClipboard(ctx, mimeType, src); err != nil {
		log.Printf("no se pudo copiar al portapapeles: %v", err)
		return
	}
	notify(ctx, title, body)
}
