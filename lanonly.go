package main

// Solo red local, sea cual sea la red.
//
// En lugar de fijar en el firewall la subred de una wifi concreta, Cofre
// comprueba cada conexión nueva: solo la acepta si viene del propio
// ordenador o de una red a la que el ordenador está conectado directamente
// en ese momento (la subred de la wifi actual, o una IPv6 de enlace local).
// Si cambias de wifi, la comprobación cambia con ella sin tocar nada.
// Cualquier conexión que llegue desde fuera (por ejemplo, desde internet
// por IPv6) se cierra antes de hacer TLS.

import (
	"log"
	"net"
	"net/netip"
	"sync"
	"time"
)

type lanListener struct {
	net.Listener

	mu       sync.Mutex
	prefixes []netip.Prefix
	fetched  time.Time

	logMu   sync.Mutex
	lastLog map[netip.Addr]time.Time
}

func newLANListener(l net.Listener) *lanListener {
	return &lanListener{Listener: l, lastLog: map[netip.Addr]time.Time{}}
}

func (l *lanListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		ap, err := netip.ParseAddrPort(c.RemoteAddr().String())
		if err == nil && l.allowed(ap.Addr()) {
			return c, nil
		}
		c.Close()
		if err == nil {
			l.logReject(ap.Addr())
		}
	}
}

// localPrefixes devuelve las redes conectadas directamente. Se cachea
// unos segundos para no leer las interfaces en cada conexión.
func (l *lanListener) localPrefixes() []netip.Prefix {
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Since(l.fetched) < 5*time.Second && l.prefixes != nil {
		return l.prefixes
	}
	l.prefixes = currentLANPrefixes()
	l.fetched = time.Now()
	return l.prefixes
}

func currentLANPrefixes() []netip.Prefix {
	out := []netip.Prefix{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		log.Printf("no se pudieron leer las redes del ordenador: %v", err)
		return out
	}
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(n.IP)
		if !ok {
			continue
		}
		ones, _ := n.Mask.Size()
		if p, err := ip.Unmap().Prefix(ones); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func lanAllowed(ip netip.Addr, prefixes []netip.Prefix) bool {
	ip = ip.WithZone("").Unmap()
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return true
	}
	for _, p := range prefixes {
		// Una /32 o /128 es solo nuestra propia IP: no abre ninguna red.
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func (l *lanListener) allowed(ip netip.Addr) bool {
	if lanAllowed(ip, l.localPrefixes()) {
		return true
	}
	// Puede que acabemos de cambiar de red: mirar otra vez sin caché.
	l.mu.Lock()
	l.fetched = time.Time{}
	l.mu.Unlock()
	return lanAllowed(ip, l.localPrefixes())
}

// logReject avisa como mucho una vez por minuto por IP.
func (l *lanListener) logReject(ip netip.Addr) {
	l.logMu.Lock()
	defer l.logMu.Unlock()
	if t, ok := l.lastLog[ip]; ok && time.Since(t) < time.Minute {
		return
	}
	if len(l.lastLog) > 1000 {
		clear(l.lastLog)
	}
	l.lastLog[ip] = time.Now()
	log.Printf("conexión rechazada: %s no está en tu red local", ip)
}
