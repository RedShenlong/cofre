package main

// Icono PNG generado en código (iOS necesita PNG para la pantalla de inicio).

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"sync"
)

var iconCache sync.Map

func serveIcon(w http.ResponseWriter, size int) {
	b, ok := iconCache.Load(size)
	if !ok {
		b = renderIcon(size)
		iconCache.Store(size, b)
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(b.([]byte))
}

// sdfRoundRect: distancia con signo a un rectángulo redondeado centrado en (cx,cy).
func sdfRoundRect(x, y, cx, cy, hw, hh, r float64) float64 {
	dx := math.Abs(x-cx) - (hw - r)
	dy := math.Abs(y-cy) - (hh - r)
	out := math.Hypot(math.Max(dx, 0), math.Max(dy, 0))
	return out + math.Min(math.Max(dx, dy), 0) - r
}

func renderIcon(size int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	top := [3]float64{79, 70, 229}  // índigo
	bot := [3]float64{124, 58, 237} // violeta
	white := [3]float64{255, 255, 255}
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			x, y := (float64(px)+0.5)/s, (float64(py)+0.5)/s
			t := (x + y) / 2
			c := [3]float64{
				top[0] + (bot[0]-top[0])*t,
				top[1] + (bot[1]-top[1])*t,
				top[2] + (bot[2]-top[2])*t,
			}
			aa := 1.5 / s
			blend := func(d float64, col [3]float64) {
				a := math.Min(math.Max(0.5-d/aa, 0), 1)
				for i := range c {
					c[i] = c[i]*(1-a) + col[i]*a
				}
			}
			// Cuerpo del cofre
			blend(sdfRoundRect(x, y, 0.5, 0.57, 0.27, 0.17, 0.05), white)
			// Tapa
			blend(sdfRoundRect(x, y, 0.5, 0.33, 0.27, 0.075, 0.06), white)
			// Ranura entre tapa y cuerpo
			blend(sdfRoundRect(x, y, 0.5, 0.415, 0.27, 0.012, 0.0), c2(top, bot, t))
			// Cerradura
			blend(math.Hypot(x-0.5, y-0.5)-0.045, c2(top, bot, t))
			blend(sdfRoundRect(x, y, 0.5, 0.56, 0.016, 0.05, 0.012), c2(top, bot, t))
			img.Set(px, py, color.RGBA{uint8(c[0]), uint8(c[1]), uint8(c[2]), 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func c2(a, b [3]float64, t float64) [3]float64 {
	return [3]float64{a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t, a[2] + (b[2]-a[2])*t}
}
