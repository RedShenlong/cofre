package main

// Barra de progreso para "cofre enviar".

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	spinFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
	partials   = []rune(" ▏▎▍▌▋▊▉")
)

type progress struct {
	r          io.Reader
	name       string
	idx, count int
	total      int64
	done       atomic.Int64
	start      time.Time

	tty, color bool
	mu         sync.Mutex
	drawn      bool
	frame      int
	speed      float64 // media móvil (bytes/s)
	lastBytes  int64
	lastT      time.Time
	stop       chan struct{}
	stopped    chan struct{}
}

func newProgress(r io.Reader, name string, total int64, idx, count int) *progress {
	tty := isTerminal(os.Stderr)
	p := &progress{
		r: r, name: name, total: total, idx: idx, count: count,
		start: time.Now(), lastT: time.Now(),
		tty: tty, color: tty && os.Getenv("NO_COLOR") == "",
		stop: make(chan struct{}), stopped: make(chan struct{}),
	}
	if tty {
		go p.loop()
	} else {
		close(p.stopped)
	}
	return p
}

func (p *progress) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done.Add(int64(n))
	return n, err
}

func (p *progress) loop() {
	defer close(p.stopped)
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			p.draw()
		}
	}
}

func (p *progress) c(code, s string) string {
	if !p.color {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func (p *progress) bar(frac float64, width int) string {
	frac = min(max(frac, 0), 1)
	cells := frac * float64(width)
	full := int(cells)
	part := int((cells - float64(full)) * 8)
	var b strings.Builder
	for i := 0; i < width; i++ {
		ch := " "
		if i < full {
			ch = "█"
		} else if i == full && part > 0 {
			ch = string(partials[part])
		}
		if p.color {
			// degradado índigo → violeta → rosa
			t := float64(i) / float64(width-1)
			r := int(99 + (236-99)*t)
			g := int(102 + (72-102)*t)
			bl := int(241 + (153-241)*t)
			if ch == " " {
				fmt.Fprintf(&b, "\033[48;2;40;40;55m \033[0m")
			} else {
				fmt.Fprintf(&b, "\033[38;2;%d;%d;%dm\033[48;2;40;40;55m%s\033[0m", r, g, bl, ch)
			}
		} else {
			if ch == " " {
				ch = "░"
			}
			b.WriteString(ch)
		}
	}
	return b.String()
}

func (p *progress) draw() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	done := p.done.Load()

	// Velocidad suavizada para que no baile.
	if dt := now.Sub(p.lastT).Seconds(); dt >= 0.25 {
		inst := float64(done-p.lastBytes) / dt
		if p.speed == 0 {
			p.speed = inst
		} else {
			p.speed = 0.8*p.speed + 0.2*inst
		}
		p.lastBytes, p.lastT = done, now
	}
	p.frame++

	frac := 1.0
	if p.total > 0 {
		frac = float64(done) / float64(p.total)
	}
	sending := done < p.total

	name := []rune(p.name)
	if len(name) > 40 {
		name = append(name[:39], '…')
	}
	spin := string(spinFrames[p.frame%len(spinFrames)])
	count := ""
	if p.count > 1 {
		count = p.c("2", fmt.Sprintf("  (%d/%d)", p.idx, p.count))
	}
	title := "Enviando"
	if !sending {
		title = "Guardando y cifrando el final…"
	}

	l1 := fmt.Sprintf(" %s %s %s%s", p.c("35;1", spin), p.c("1", title), p.c("36", string(name)), count)
	l2 := fmt.Sprintf("   %s %s  %s",
		p.bar(frac, 32),
		p.c("1", fmt.Sprintf("%5.1f%%", frac*100)),
		p.c("2", humanSize(done)+" / "+humanSize(p.total)))
	eta := "—"
	if sending && p.speed > 0 {
		eta = fmtDur(time.Duration(float64(p.total-done) / p.speed * float64(time.Second)))
	} else if !sending {
		eta = "casi listo"
	}
	l3 := fmt.Sprintf("   %s %s   %s %s   %s %s",
		p.c("33", "⚡"), p.c("1", humanSize(int64(p.speed))+"/s"),
		p.c("34", "⏱"), fmtDur(now.Sub(p.start)),
		p.c("32", "⏳"), eta)

	if p.drawn {
		fmt.Fprint(os.Stderr, "\033[2F") // sube 2 líneas
	}
	fmt.Fprintf(os.Stderr, "\r\033[K%s\n\033[K%s\n\033[K%s", l1, l2, l3)
	p.drawn = true
}

// finish borra la barra y deja una línea de resumen.
func (p *progress) finish(err error) {
	if p.tty {
		close(p.stop)
		<-p.stopped
		p.mu.Lock()
		if p.drawn {
			fmt.Fprint(os.Stderr, "\033[2F\r\033[J")
		}
		p.mu.Unlock()
	}
	el := time.Since(p.start)
	if err != nil {
		fmt.Fprintf(os.Stderr, " %s %s %s\n", p.c("31;1", "✗"), p.name, p.c("31", err.Error()))
		return
	}
	avg := float64(p.total) / max(el.Seconds(), 0.001)
	fmt.Printf(" %s %s  %s en %s %s\n",
		p.c("32;1", "✓"), p.c("1", p.name), humanSize(p.total), fmtDur(el),
		p.c("2", "· media "+humanSize(int64(avg))+"/s"))
}

func fmtDur(d time.Duration) string {
	d = d.Round(time.Second)
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
