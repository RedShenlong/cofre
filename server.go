package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

//go:embed web
var webFS embed.FS

const (
	sessionCookie  = "__Host-cofre"
	ceremonyCookie = "__Host-cofre-c"
	maxTextBytes   = 1 << 20
	linkTTL        = 15 * time.Minute
)

type ceremony struct {
	data    webauthn.SessionData
	kind    string // "login" | "register"
	name    string // nombre de la llave nueva
	byCode  bool   // alta inicial con código
	expires time.Time
}

type enrollCode struct {
	code     string
	expires  time.Time
	attempts int
}

type Server struct {
	store     *Store
	wa        *webauthn.WebAuthn
	host      string
	origin    string
	tls       tlsPaths
	linkKey   []byte
	maxUpload int64
	hub       *Hub

	mu         sync.Mutex
	ceremonies map[string]*ceremony
	enroll     *enrollCode
}

type ctxKey int

const (
	ctxSession ctxKey = iota
	ctxLocal
)

func newServer(store *Store, keys *Keyring, host string, port int, tp tlsPaths, maxUpload int64) (*Server, error) {
	origin := "https://" + host
	if port != 443 {
		origin += ":" + strconv.Itoa(port)
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          host,
		RPDisplayName: "Cofre",
		RPOrigins:     []string{origin},
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: 2 * time.Minute, TimeoutUVD: 2 * time.Minute},
			Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: 3 * time.Minute, TimeoutUVD: 3 * time.Minute},
		},
	})
	if err != nil {
		return nil, err
	}
	return &Server{
		store: store, wa: wa, host: host, origin: origin, tls: tp,
		linkKey: keys.derive("links", nil), maxUpload: maxUpload,
		hub: newHub(), ceremonies: map[string]*ceremony{},
	}, nil
}

// ================= Enrutado =================

func (s *Server) browserHandler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(webFS, "web")
	files := http.FileServerFS(static)

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		b, _ := webFS.ReadFile("web/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(b)
	})
	for _, f := range []string{"/app.js", "/app.css", "/manifest.webmanifest", "/icon.svg"} {
		mux.Handle("GET "+f, noCache(files))
	}
	for _, n := range []int{180, 192, 512} {
		mux.HandleFunc(fmt.Sprintf("GET /icon-%d.png", n), func(w http.ResponseWriter, r *http.Request) { serveIcon(w, n) })
	}
	mux.HandleFunc("GET /ca/{file}", s.serveCA)

	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("POST /api/enroll/begin", s.enrollBegin)
	mux.HandleFunc("POST /api/enroll/finish", s.enrollFinish)
	mux.HandleFunc("POST /api/login/begin", s.loginBegin)
	mux.HandleFunc("POST /api/login/finish", s.loginFinish)
	mux.HandleFunc("GET /api/raw/{id}", s.raw) // cookie o enlace firmado

	auth := func(p string, h http.HandlerFunc) { mux.Handle(p, s.requireAuth(h)) }
	auth("POST /api/logout", s.logout)
	auth("GET /api/items", s.listItems)
	auth("POST /api/text", s.postText)
	auth("POST /api/upload", s.upload)
	auth("DELETE /api/items/{id}", s.deleteItem)
	auth("POST /api/items/{id}/pin", s.pinItem)
	auth("POST /api/clear", s.clearItems)
	auth("GET /api/events", s.events)
	auth("GET /api/account", s.account)
	auth("DELETE /api/sessions/{id}", s.deleteSession)
	auth("DELETE /api/keys/{id}", s.deleteKey)
	auth("POST /api/settings", s.saveSettings)

	return s.secure(mux)
}

// Handler del socket Unix local (solo accesible por tu usuario del portátil).
func (s *Server) localHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /local/code", func(w http.ResponseWriter, r *http.Request) {
		code, exp := s.newEnrollCode()
		writeJSON(w, map[string]any{"code": code, "expires": exp, "origin": s.origin})
	})
	mux.HandleFunc("GET /local/info", func(w http.ResponseWriter, r *http.Request) {
		info := map[string]any{"origin": s.origin, "host": s.host, "items": len(s.store.Items()), "enrolled": s.store.HasCreds()}
		if c, err := readCert(s.tls.ca); err == nil {
			info["fingerprint"] = fingerprint(c)
			info["caPath"] = s.tls.ca
			info["caExpires"] = c.NotAfter
		}
		writeJSON(w, info)
	})
	mux.HandleFunc("POST /local/text", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxTextBytes))
		if err != nil || len(bytes.TrimSpace(b)) == 0 {
			httpErr(w, 400, "texto vacío o demasiado largo")
			return
		}
		it, err := s.store.AddText(string(b), "Portátil (terminal)", "")
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		s.hub.publish("items")
		writeJSON(w, it)
	})
	mux.HandleFunc("POST /local/upload", func(w http.ResponseWriter, r *http.Request) {
		if err := s.checkSpace(r.ContentLength); err != nil {
			httpErr(w, http.StatusInsufficientStorage, err.Error())
			return
		}
		name := cleanName(r.URL.Query().Get("name"))
		br := bufio.NewReaderSize(r.Body, 4096)
		it, err := s.store.AddFile(name, detectMime(name, br), "Portátil (terminal)", "", br)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		s.hub.publish("items")
		writeJSON(w, it)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxLocal, true)))
	})
}

// secure aplica cabeceras de seguridad, comprueba Host y Origin.
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Strict-Transport-Security", "max-age=31536000")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' blob: data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'")

		// El certificado de la CA es público: se puede descargar desde cualquier dirección.
		if strings.HasPrefix(r.URL.Path, "/ca/") {
			next.ServeHTTP(w, r)
			return
		}
		// Solo se responde al nombre correcto (evita DNS rebinding y
		// garantiza que WebAuthn funcione con el origen esperado).
		hostOnly := r.Host
		if hn, _, err := net.SplitHostPort(r.Host); err == nil {
			hostOnly = hn
		}
		if !strings.EqualFold(hostOnly, s.host) {
			if r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/api/") {
				http.Redirect(w, r, s.origin+r.URL.RequestURI(), http.StatusFound)
				return
			}
			httpErr(w, http.StatusMisdirectedRequest, "nombre de host incorrecto")
			return
		}
		// Protección CSRF: toda petición que modifica algo debe venir de nuestra propia página.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("Origin") != s.origin {
				httpErr(w, http.StatusForbidden, "origen no permitido")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

func (s *Server) currentSession(r *http.Request) (Session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return Session{}, false
	}
	return s.store.SessionFor(c.Value)
}

func (s *Server) requireAuth(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		se, ok := s.currentSession(r)
		if !ok {
			httpErr(w, http.StatusUnauthorized, "inicia sesión con tu llave")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), ctxSession, se)))
	})
}

func sessionOf(r *http.Request) Session {
	se, _ := r.Context().Value(ctxSession).(Session)
	return se
}

// ================= Utilidades =================

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*maxTextBytes)).Decode(v)
}

func setCookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", MaxAge: maxAge,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
}

func cleanName(n string) string {
	n = filepath.Base(strings.ReplaceAll(n, "\\", "/"))
	n = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' {
			return -1
		}
		return r
	}, n)
	n = strings.TrimSpace(n)
	if n == "" || n == "." || n == ".." {
		n = "archivo"
	}
	if r := []rune(n); len(r) > 180 {
		ext := filepath.Ext(n)
		if len([]rune(ext)) > 20 {
			ext = ""
		}
		n = string(r[:180-len([]rune(ext))]) + ext
	}
	return n
}

func detectMime(name string, br *bufio.Reader) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		return t
	}
	head, _ := br.Peek(512)
	return http.DetectContentType(head)
}

func deviceName(ua string, touch bool) string {
	dev := "Dispositivo"
	switch {
	case strings.Contains(ua, "iPhone"):
		dev = "iPhone"
	case strings.Contains(ua, "iPad"):
		dev = "iPad"
	case strings.Contains(ua, "Android") && strings.Contains(ua, "Mobile"):
		dev = "Móvil Android"
	case strings.Contains(ua, "Android"):
		dev = "Tablet Android"
	case strings.Contains(ua, "Macintosh") && touch:
		dev = "iPad"
	case strings.Contains(ua, "Macintosh"):
		dev = "Mac"
	case strings.Contains(ua, "Windows"):
		dev = "Windows"
	case strings.Contains(ua, "Linux") && touch:
		// Las tablets Android (p. ej. Samsung) usan por defecto el modo escritorio.
		dev = "Tablet Android"
	case strings.Contains(ua, "Linux"):
		dev = "Portátil Linux"
	}
	br := ""
	switch {
	case strings.Contains(ua, "Firefox/"), strings.Contains(ua, "FxiOS"):
		br = "Firefox"
	case strings.Contains(ua, "Edg/"):
		br = "Edge"
	case strings.Contains(ua, "Chrome/"), strings.Contains(ua, "CriOS"):
		br = "Chrome"
	case strings.Contains(ua, "Safari/"):
		br = "Safari"
	}
	if br != "" {
		return dev + " · " + br
	}
	return dev
}

// ---- Enlaces firmados para abrir/descargar sin cookie (pestañas nuevas, PWA de iOS) ----

func (s *Server) signLink(id string) string {
	exp := strconv.FormatInt(time.Now().Add(linkTTL).Unix(), 36)
	m := hmac.New(sha256.New, s.linkKey)
	m.Write([]byte(id + "|" + exp))
	return exp + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Server) checkLink(id, tok string) bool {
	exp, sig, ok := strings.Cut(tok, ".")
	if !ok {
		return false
	}
	ts, err := strconv.ParseInt(exp, 36, 64)
	if err != nil || time.Now().Unix() > ts {
		return false
	}
	m := hmac.New(sha256.New, s.linkKey)
	m.Write([]byte(id + "|" + exp))
	want := base64.RawURLEncoding.EncodeToString(m.Sum(nil))
	return hmac.Equal([]byte(sig), []byte(want))
}

// ================= Ceremonias WebAuthn =================

func (s *Server) putCeremony(w http.ResponseWriter, c *ceremony) {
	id := newToken()
	s.mu.Lock()
	now := time.Now()
	for k, v := range s.ceremonies {
		if now.After(v.expires) {
			delete(s.ceremonies, k)
		}
	}
	if len(s.ceremonies) > 200 { // evita que alguien llene la memoria
		s.ceremonies = map[string]*ceremony{}
	}
	s.ceremonies[id] = c
	s.mu.Unlock()
	setCookie(w, ceremonyCookie, id, 300)
}

func (s *Server) takeCeremony(w http.ResponseWriter, r *http.Request, kind string) (*ceremony, bool) {
	ck, err := r.Cookie(ceremonyCookie)
	if err != nil {
		return nil, false
	}
	setCookie(w, ceremonyCookie, "", -1)
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.ceremonies[ck.Value]
	delete(s.ceremonies, ck.Value) // un solo uso
	if !ok || c.kind != kind || time.Now().After(c.expires) {
		return nil, false
	}
	return c, true
}

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func (s *Server) newEnrollCode() (string, time.Time) {
	b := make([]byte, 8)
	rand.Read(b)
	out := make([]byte, 8)
	for i, v := range b {
		out[i] = codeAlphabet[int(v)%len(codeAlphabet)]
	}
	code := string(out[:4]) + "-" + string(out[4:])
	exp := time.Now().Add(15 * time.Minute)
	s.mu.Lock()
	s.enroll = &enrollCode{code: string(out), expires: exp}
	s.mu.Unlock()
	return code, exp
}

func (s *Server) checkEnrollCode(input string) bool {
	norm := strings.Map(func(r rune) rune {
		r = unicode.ToUpper(r)
		if strings.ContainsRune(codeAlphabet, r) {
			return r
		}
		return -1
	}, input)
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.enroll
	if e == nil || time.Now().After(e.expires) {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(norm), []byte(e.code)) == 1 {
		s.enroll = nil // un solo uso
		return true
	}
	e.attempts++
	if e.attempts >= 5 {
		s.enroll = nil
		log.Printf("código de alta invalidado tras 5 intentos fallidos")
	}
	return false
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	_, authed := s.currentSession(r)
	writeJSON(w, map[string]any{"enrolled": s.store.HasCreds(), "authed": authed, "host": s.host})
}

func (s *Server) enrollBegin(w http.ResponseWriter, r *http.Request) {
	var req struct{ Code, Name string }
	if err := readJSON(w, r, &req); err != nil {
		httpErr(w, 400, "petición inválida")
		return
	}
	_, authed := s.currentSession(r)
	byCode := false
	if !authed {
		if !s.checkEnrollCode(req.Code) {
			httpErr(w, 403, "código incorrecto o caducado. Genera uno nuevo en el portátil con: cofre codigo")
			return
		}
		byCode = true
	}
	user := s.store.User()
	var excl []protocol.CredentialDescriptor
	for _, c := range user.creds {
		excl = append(excl, c.Descriptor())
	}
	opts, data, err := s.wa.BeginRegistration(user,
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			AuthenticatorAttachment: protocol.CrossPlatform, // llave física
			ResidentKey:             protocol.ResidentKeyRequirementDiscouraged,
			UserVerification:        protocol.VerificationPreferred,
		}),
		webauthn.WithExclusions(excl),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
		webauthn.WithPublicKeyCredentialHints([]protocol.PublicKeyCredentialHints{protocol.PublicKeyCredentialHintSecurityKey}),
	)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Llave de seguridad"
	}
	if len([]rune(name)) > 40 {
		name = string([]rune(name)[:40])
	}
	s.putCeremony(w, &ceremony{data: *data, kind: "register", name: name, byCode: byCode, expires: time.Now().Add(4 * time.Minute)})
	writeJSON(w, opts)
}

func (s *Server) enrollFinish(w http.ResponseWriter, r *http.Request) {
	c, ok := s.takeCeremony(w, r, "register")
	if !ok {
		httpErr(w, 400, "la operación caducó, vuelve a intentarlo")
		return
	}
	_, authed := s.currentSession(r)
	if !authed && !c.byCode {
		httpErr(w, 401, "sesión no válida")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	cred, err := s.wa.FinishRegistration(s.store.User(), c.data, r)
	if err != nil {
		log.Printf("alta de llave fallida: %v", err)
		httpErr(w, 400, "no se pudo registrar la llave")
		return
	}
	err = s.store.update(func(st *State) error {
		st.Creds = append(st.Creds, &StoredCred{ID: newID()[:12], Name: c.name, Added: time.Now(), Cred: *cred})
		return nil
	})
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	log.Printf("llave registrada: %q", c.name)
	if !authed {
		if err := s.startSession(w, r); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
	}
	s.hub.publish("account")
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) loginBegin(w http.ResponseWriter, r *http.Request) {
	user := s.store.User()
	if len(user.creds) == 0 {
		httpErr(w, 409, "todavía no hay ninguna llave registrada")
		return
	}
	opts, data, err := s.wa.BeginLogin(user,
		webauthn.WithUserVerification(protocol.VerificationPreferred),
		webauthn.WithAssertionPublicKeyCredentialHints([]protocol.PublicKeyCredentialHints{protocol.PublicKeyCredentialHintSecurityKey}),
	)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	s.putCeremony(w, &ceremony{data: *data, kind: "login", expires: time.Now().Add(3 * time.Minute)})
	writeJSON(w, opts)
}

func (s *Server) loginFinish(w http.ResponseWriter, r *http.Request) {
	c, ok := s.takeCeremony(w, r, "login")
	if !ok {
		httpErr(w, 400, "la operación caducó, vuelve a intentarlo")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	cred, err := s.wa.FinishLogin(s.store.User(), c.data, r)
	if err != nil {
		log.Printf("inicio de sesión fallido: %v", err)
		httpErr(w, 401, "la llave no es válida")
		return
	}
	if cred.Authenticator.CloneWarning {
		log.Printf("¡AVISO! posible llave clonada (contador de firmas no válido)")
		httpErr(w, 401, "la llave parece clonada; acceso denegado")
		return
	}
	s.store.update(func(st *State) error {
		for _, sc := range st.Creds {
			if bytes.Equal(sc.Cred.ID, cred.ID) {
				sc.Cred.Authenticator = cred.Authenticator
				sc.Cred.Flags = cred.Flags
				sc.LastUsed = time.Now()
			}
		}
		return nil
	})
	if err := s.startSession(w, r); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	s.hub.publish("account")
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request) error {
	dev := deviceName(r.UserAgent(), r.Header.Get("X-Cofre-Touch") == "1")
	tok, err := s.store.NewSession(dev)
	if err != nil {
		return err
	}
	var days int
	s.store.view(func(st *State) { days = st.Settings.SessionDays })
	setCookie(w, sessionCookie, tok, days*86400)
	log.Printf("nueva sesión: %s", dev)
	return nil
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.store.DeleteSession(sessionOf(r).ID)
	setCookie(w, sessionCookie, "", -1)
	s.hub.publish("account")
	writeJSON(w, map[string]bool{"ok": true})
}

// ================= Elementos =================

type itemOut struct {
	Item
	Mine bool   `json:"mine"` // subido desde este mismo dispositivo
	URL  string `json:"url,omitempty"`
	DL   string `json:"dl,omitempty"`
}

func (s *Server) listItems(w http.ResponseWriter, r *http.Request) {
	items := s.store.Items()
	me := sessionOf(r).ID
	out := make([]itemOut, 0, len(items))
	for _, it := range items {
		o := itemOut{Item: it, Mine: it.Session != "" && it.Session == me}
		o.Session = ""
		if it.Kind == "file" {
			t := s.signLink(it.ID)
			o.URL = "/api/raw/" + it.ID + "?t=" + t
			o.DL = o.URL + "&dl=1"
		}
		out = append(out, o)
	}
	writeJSON(w, map[string]any{"items": out, "linkTTL": int(linkTTL.Seconds()), "maxUpload": s.maxUpload})
}

func (s *Server) postText(w http.ResponseWriter, r *http.Request) {
	var req struct{ Text string }
	if err := readJSON(w, r, &req); err != nil {
		httpErr(w, 400, "petición inválida")
		return
	}
	if strings.TrimSpace(req.Text) == "" || len(req.Text) > maxTextBytes {
		httpErr(w, 400, "texto vacío o demasiado largo (máx. 1 MB)")
		return
	}
	se := sessionOf(r)
	it, err := s.store.AddText(req.Text, se.Device, se.ID)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	s.hub.publish("items")
	go s.toLaptopClipboard(*it)
	writeJSON(w, it)
}

// checkSpace evita llenar el disco: exige el tamaño + 2 % + 1 GB de margen.
func (s *Server) checkSpace(n int64) error {
	if n <= 0 {
		return nil
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(s.store.dir, &st); err != nil {
		return nil
	}
	free := int64(st.Bavail) * int64(st.Bsize)
	need := encryptedSize(n) + n/50 + 1<<30
	if free < need {
		return fmt.Errorf("no hay espacio en el portátil: hay %s libres y hacen falta %s", humanSize(free), humanSize(need))
	}
	return nil
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > s.maxUpload {
		httpErr(w, 413, fmt.Sprintf("demasiado grande para subir desde el navegador (máx. %s). Desde el portátil usa: cofre enviar", humanSize(s.maxUpload)))
		return
	}
	if err := s.checkSpace(r.ContentLength); err != nil {
		httpErr(w, http.StatusInsufficientStorage, err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload)
	mr, err := r.MultipartReader()
	if err != nil {
		httpErr(w, 400, "se esperaba multipart/form-data")
		return
	}
	se := sessionOf(r)
	var added []*Item
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				httpErr(w, 413, fmt.Sprintf("archivo demasiado grande (máx. %s)", humanSize(s.maxUpload)))
				return
			}
			httpErr(w, 400, "subida interrumpida")
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			part.Close()
			continue
		}
		name := cleanName(part.FileName())
		br := bufio.NewReaderSize(part, 4096)
		it, err := s.store.AddFile(name, detectMime(name, br), se.Device, se.ID, br)
		part.Close()
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				httpErr(w, 413, fmt.Sprintf("archivo demasiado grande (máx. %s)", humanSize(s.maxUpload)))
				return
			}
			httpErr(w, 500, "error guardando el archivo")
			return
		}
		added = append(added, it)
		s.hub.publish("items")
		go s.toLaptopClipboard(*it)
	}
	writeJSON(w, map[string]any{"added": added})
}

// Tipos que el navegador puede mostrar sin riesgo de ejecutar código en nuestra página.
var inlineSafe = map[string]bool{
	"application/pdf": true, "image/png": true, "image/jpeg": true, "image/gif": true,
	"image/webp": true, "image/avif": true, "image/heic": true, "text/plain": true,
	"audio/mpeg": true, "audio/mp4": true, "audio/ogg": true, "audio/wav": true,
	"video/mp4": true, "video/webm": true, "video/quicktime": true,
}

func (s *Server) raw(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.currentSession(r); !ok && !s.checkLink(id, r.URL.Query().Get("t")) {
		httpErr(w, 401, "enlace caducado: recarga la página")
		return
	}
	it, ok := s.store.Item(id)
	if !ok || it.Kind != "file" {
		httpErr(w, 404, "no existe")
		return
	}
	rd, err := s.store.OpenFile(it)
	if err != nil {
		log.Printf("error abriendo %s: %v", it.ID, err)
		httpErr(w, 500, "no se pudo descifrar el archivo")
		return
	}
	defer rd.Close()

	base, _, _ := mime.ParseMediaType(it.Mime)
	inline := inlineSafe[base] && r.URL.Query().Get("dl") != "1"
	ctype := "application/octet-stream"
	if inlineSafe[base] {
		ctype = base
		if base == "text/plain" {
			ctype = "text/plain; charset=utf-8"
		}
	}
	disp := "attachment"
	if inline {
		disp = "inline"
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": it.Name}))
	h.Set("Cache-Control", "private, no-store")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	if base == "application/pdf" {
		h.Del("Content-Security-Policy") // el visor de PDF del navegador necesita su propia política
		h.Set("X-Frame-Options", "SAMEORIGIN")
	} else {
		h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; media-src 'self'; style-src 'unsafe-inline'; sandbox")
	}
	http.ServeContent(w, r, "", it.Created, rd)
}

func (s *Server) deleteItem(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Delete(r.PathValue("id")); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	s.hub.publish("items")
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) pinItem(w http.ResponseWriter, r *http.Request) {
	var req struct{ Pinned bool }
	if err := readJSON(w, r, &req); err != nil {
		httpErr(w, 400, "petición inválida")
		return
	}
	if err := s.store.SetPinned(r.PathValue("id"), req.Pinned); err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	s.hub.publish("items")
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) clearItems(w http.ResponseWriter, r *http.Request) {
	var req struct{ KeepPinned bool }
	readJSON(w, r, &req)
	var ids []string
	for _, it := range s.store.Items() {
		if !(req.KeepPinned && it.Pinned) {
			ids = append(ids, it.ID)
		}
	}
	if err := s.store.Delete(ids...); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	s.hub.publish("items")
	writeJSON(w, map[string]int{"deleted": len(ids)})
}

// ================= Cuenta / dispositivos =================

func (s *Server) account(w http.ResponseWriter, r *http.Request) {
	cur := sessionOf(r)
	type sessOut struct {
		ID, Device                 string
		Created, LastSeen, Expires time.Time
		Current                    bool
	}
	type keyOut struct {
		ID, Name        string
		Added, LastUsed time.Time
	}
	var ss []sessOut
	var ks []keyOut
	var set Settings
	s.store.view(func(st *State) {
		for _, x := range st.Sessions {
			ss = append(ss, sessOut{x.ID, x.Device, x.Created, x.LastSeen, x.Expires, x.ID == cur.ID})
		}
		for _, k := range st.Creds {
			ks = append(ks, keyOut{k.ID, k.Name, k.Added, k.LastUsed})
		}
		set = st.Settings
	})
	writeJSON(w, map[string]any{"sessions": ss, "keys": ks, "settings": set, "maxUploadMB": s.maxUpload >> 20})
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	s.store.DeleteSession(r.PathValue("id"))
	s.hub.publish("account")
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) deleteKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.update(func(st *State) error {
		if len(st.Creds) <= 1 {
			return errors.New("no puedes borrar tu única llave")
		}
		kept := st.Creds[:0]
		for _, c := range st.Creds {
			if c.ID != id {
				kept = append(kept, c)
			}
		}
		st.Creds = kept
		return nil
	})
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	s.hub.publish("account")
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	var req Settings
	if err := readJSON(w, r, &req); err != nil {
		httpErr(w, 400, "petición inválida")
		return
	}
	validRet := map[int]bool{0: true, 1: true, 3: true, 7: true, 14: true, 30: true, 90: true}
	validSes := map[int]bool{1: true, 7: true, 14: true, 30: true, 90: true}
	if !validRet[req.RetentionDays] || !validSes[req.SessionDays] {
		httpErr(w, 400, "valor no permitido")
		return
	}
	s.store.update(func(st *State) error { st.Settings = req; return nil })
	if s.store.Cleanup() {
		s.hub.publish("items")
	}
	s.hub.publish("account")
	writeJSON(w, map[string]bool{"ok": true})
}

// ================= CA pública =================

func (s *Server) serveCA(w http.ResponseWriter, r *http.Request) {
	switch r.PathValue("file") {
	case "cofre-ca.crt":
		w.Header().Set("Content-Type", "application/x-x509-ca-cert")
		w.Header().Set("Content-Disposition", `attachment; filename="cofre-ca.crt"`)
		http.ServeFile(w, r, s.tls.ca)
	default:
		http.NotFound(w, r)
	}
}

// ================= Tiempo real (Server-Sent Events) =================

type Hub struct {
	mu   sync.Mutex
	subs map[chan string]struct{}
}

func newHub() *Hub { return &Hub{subs: map[chan string]struct{}{}} }

func (h *Hub) subscribe() chan string {
	ch := make(chan string, 8)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) unsubscribe(ch chan string) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

func (h *Hub) publish(ev string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, 500, "streaming no soportado")
		return
	}
	ck, _ := r.Cookie(sessionCookie)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	ch := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)
	fmt.Fprint(w, "retry: 3000\n\n")
	fl.Flush()
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			if _, ok := s.store.SessionFor(ck.Value); !ok {
				fmt.Fprint(w, "event: logout\ndata: 1\n\n")
				fl.Flush()
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: 1\n\n", ev)
		case <-tick.C:
			if _, ok := s.store.SessionFor(ck.Value); !ok {
				fmt.Fprint(w, "event: logout\ndata: 1\n\n")
				fl.Flush()
				return
			}
			fmt.Fprint(w, ": ping\n\n")
		}
		fl.Flush()
	}
}
