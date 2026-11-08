package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const usage = `Cofre — comparte archivos y texto entre tus dispositivos, protegido con tu llave FIDO2.

Uso:
  cofre serve [opciones]      Arranca el servidor (normalmente lo hace systemd).
  cofre enviar ARCHIVO...     Sube uno o varios archivos al cofre.
  cofre enviar -t "texto"     Sube un texto.
  cofre enviar -              Sube lo que llegue por la entrada estándar como texto.
  cofre registrar             Registra tu llave (abre el navegador del portátil).
  cofre reiniciar-llaves      Borra todas las llaves y sesiones (si pierdes tu llave).
  cofre ca                    Muestra el certificado a instalar en tus dispositivos.
  cofre estado                Muestra el estado del servidor.

Opciones de "serve":
`

func main() {
	log.SetFlags(log.Ldate | log.Ltime)
	if len(os.Args) < 2 {
		fmt.Print(usage)
		serveFlags().PrintDefaults()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "serve", "servir":
		err = serve(args)
	case "enviar", "send":
		err = cmdSend(args)
	case "registrar", "register", "codigo":
		err = cmdRegister()
	case "reiniciar-llaves", "reset-keys":
		err = cmdResetKeys(args)
	case "ca":
		err = cmdCA()
	case "estado", "status":
		err = cmdStatus()
	case "-h", "--help", "help", "ayuda":
		fmt.Print(usage)
		serveFlags().PrintDefaults()
		return
	default:
		fmt.Fprintf(os.Stderr, "comando desconocido: %s\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

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

func defaultHost() string {
	h, _ := os.Hostname()
	h = strings.ToLower(strings.Split(h, ".")[0])
	if h == "" {
		h = "cofre"
	}
	return h + ".local"
}

var (
	fData, fHost, fAddr *string
	fPort               *int
	fMaxMB              *int64
)

func serveFlags() *flag.FlagSet {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	fData = fs.String("data", defaultDataDir(), "carpeta de datos (cifrados)")
	fHost = fs.String("host", defaultHost(), "nombre con el que tus dispositivos encuentran el portátil (mDNS)")
	fAddr = fs.String("addr", "", "IP en la que escuchar (vacío = todas)")
	fPort = fs.Int("port", 8443, "puerto HTTPS")
	fMaxMB = fs.Int64("max-mb", 16384, "tamaño máximo de subida desde el navegador en MB")
	return fs
}

func serve(args []string) error {
	fs := serveFlags()
	fs.Parse(args)
	host := strings.ToLower(*fHost)
	if err := os.MkdirAll(*fData, 0o700); err != nil {
		return err
	}
	os.Chmod(*fData, 0o700)

	keys, err := loadOrCreateMasterKey(filepath.Join(*fData, "master.key"))
	if err != nil {
		return err
	}
	store, err := openStore(*fData, keys)
	if err != nil {
		return err
	}
	tp, regenerated, err := ensureTLS(*fData, host)
	if err != nil {
		return fmt.Errorf("certificados: %w", err)
	}
	ca, _ := readCert(tp.ca)
	if regenerated {
		log.Printf("Certificado nuevo generado. Instálalo en tus dispositivos (ver: cofre ca)")
	}
	log.Printf("Huella SHA-256 de la CA: %s", fingerprint(ca))

	srv, err := newServer(store, keys, host, *fPort, tp, *fMaxMB<<20)
	if err != nil {
		return err
	}

	cert, err := tls.LoadX509KeyPair(tp.cert, tp.key)
	if err != nil {
		return err
	}
	httpsSrv := &http.Server{
		Addr:              net.JoinHostPort(*fAddr, fmt.Sprint(*fPort)),
		Handler:           srv.browserHandler(),
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}},
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          log.New(io.Discard, "", 0), // silencia el ruido de handshakes fallidos
	}

	// Socket local para el comando "cofre enviar" (solo tu usuario puede usarlo).
	sock := socketPath()
	os.Remove(sock)
	oldMask := syscall.Umask(0o177)
	ul, err := net.Listen("unix", sock)
	syscall.Umask(oldMask)
	if err != nil {
		return fmt.Errorf("socket local: %w", err)
	}
	localSrv := &http.Server{Handler: srv.localHandler(), ReadHeaderTimeout: 10 * time.Second}
	go localSrv.Serve(ul)

	// Limpieza periódica (una vez por hora: casi no consume nada).
	go func() {
		store.Cleanup()
		t := time.NewTicker(time.Hour)
		for range t.C {
			if store.Cleanup() {
				srv.hub.publish("items")
			}
		}
	}()

	if !store.HasCreds() {
		log.Printf("Aún no hay llaves registradas. En el portátil ejecuta: cofre registrar")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() {
		log.Printf("Cofre escuchando en %s", srv.origin)
		errc <- httpsSrv.ListenAndServeTLS("", "")
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Printf("Cerrando…")
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpsSrv.Shutdown(sctx)
	localSrv.Shutdown(sctx)
	os.Remove(sock)
	return nil
}

// ================= Cliente local (socket Unix) =================

func localClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath())
		},
	}}
}

func localDo(method, path string, body io.Reader, out any) error {
	req, _ := http.NewRequest(method, "http://cofre"+path, body)
	resp, err := localClient().Do(req)
	if err != nil {
		return errors.New("no se pudo conectar con el servidor. ¿Está en marcha? (systemctl --user status cofre)")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var e struct{ Error string }
		json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("%s", e.Error)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func cmdSend(args []string) error {
	if len(args) == 0 {
		return errors.New(`indica uno o más archivos, -t "texto" o - para leer de la entrada estándar`)
	}
	if args[0] == "-t" {
		if len(args) < 2 {
			return errors.New(`falta el texto: cofre enviar -t "hola"`)
		}
		if err := localDo("POST", "/local/text", strings.NewReader(strings.Join(args[1:], " ")), nil); err != nil {
			return err
		}
		fmt.Println("✓ Texto enviado")
		return nil
	}
	if args[0] == "-" {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, maxTextBytes+1))
		if err != nil {
			return err
		}
		if err := localDo("POST", "/local/text", strings.NewReader(string(b)), nil); err != nil {
			return err
		}
		fmt.Println("✓ Texto enviado")
		return nil
	}
	for i, p := range args {
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		st, err := f.Stat()
		if err != nil || st.IsDir() {
			f.Close()
			return fmt.Errorf("%s no es un archivo", p)
		}
		err = localUpload(filepath.Base(p), f, st.Size(), i+1, len(args))
		f.Close()
		if err != nil {
			os.Exit(1) // el error ya se ha mostrado
		}
	}
	return nil
}

// localUpload sube un archivo por el socket local mostrando el progreso.
func localUpload(name string, r io.Reader, size int64, idx, count int) error {
	pr := newProgress(r, name, size, idx, count)
	req, _ := http.NewRequest("POST", "http://cofre/local/upload?name="+url.QueryEscape(name), pr)
	req.ContentLength = size
	resp, err := localClient().Do(req)
	if err != nil {
		if pr.done.Load() > 0 {
			err = fmt.Errorf("la subida se cortó tras %s: %v", humanSize(pr.done.Load()), err)
		} else {
			err = errors.New("no se pudo conectar con el servidor. ¿Está en marcha? (systemctl --user status cofre)")
		}
		pr.finish(err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var e struct{ Error string }
		json.NewDecoder(resp.Body).Decode(&e)
		err = errors.New(e.Error)
	}
	pr.finish(err)
	return err
}

func cmdRegister() error {
	var out struct {
		URL     string
		Expires time.Time
	}
	if err := localDo("POST", "/local/register", nil, &out); err != nil {
		return err
	}
	fmt.Println("🔑 Abriendo el navegador para registrar tu llave…")
	fmt.Println("   Escribe un nombre, pulsa \"Registrar llave\" y toca tu llave.")
	fmt.Printf("   El enlace vale una sola vez y caduca a las %s.\n", out.Expires.Format("15:04"))
	if exec.Command("xdg-open", out.URL).Start() != nil {
		fmt.Println("\n   No se pudo abrir el navegador. Abre este enlace EN ESTE PORTÁTIL:")
		fmt.Println("  ", out.URL)
	}
	return nil
}

func cmdResetKeys(args []string) error {
	if len(args) == 0 || args[0] != "--si" {
		fmt.Print("Esto borra TODAS las llaves registradas y cierra todas las sesiones\n" +
			"(tus archivos no se tocan, y tu llave física tampoco). ¿Seguro? [s/N] ")
		var ans string
		fmt.Scanln(&ans)
		if a := strings.ToLower(strings.TrimSpace(ans)); a != "s" && a != "si" && a != "sí" {
			fmt.Println("Cancelado.")
			return nil
		}
	}
	var out struct{ Keys int }
	if err := localDo("POST", "/local/reset-keys", nil, &out); err != nil {
		return err
	}
	fmt.Printf("✓ %d llave(s) y todas las sesiones borradas. Registra de nuevo con: cofre registrar\n", out.Keys)
	return nil
}

func cmdCA() error {
	var info map[string]any
	if err := localDo("GET", "/local/info", nil, &info); err != nil {
		return err
	}
	o := info["origin"]
	fmt.Printf(`Certificado de tu Cofre (instálalo UNA vez en cada dispositivo)

  Huella SHA-256: %v

  Archivos:
    %v

  Android:  envíatelo con LocalSend (va cifrado).
  iPhone:   abre en Safari %v/ca/cofre-ca.crt
            (avisará de que no es seguro: es normal la primera vez).
  COMPRUEBA después que la huella coincide con la de arriba.

  Android:  Ajustes → Seguridad → Más ajustes → Cifrado y credenciales →
            Instalar un certificado → Certificado de CA.
  iPhone:   Permitir → Ajustes → Perfil descargado → Instalar.
            Después: Ajustes → General → Información → Ajustes de confianza
            de certificados → activa "Cofre CA".

  Esta CA solo puede firmar certificados para %v: no sirve para espiar otras webs.
`, info["fingerprint"], info["caPath"], o, info["host"])
	return nil
}

func cmdStatus() error {
	var info map[string]any
	if err := localDo("GET", "/local/info", nil, &info); err != nil {
		return err
	}
	fmt.Printf("✓ Cofre en marcha en %v\n  Elementos: %v\n  Llave registrada: %v\n  Certificado válido hasta: %v\n",
		info["origin"], info["items"], info["enrolled"], info["caExpires"])
	return nil
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
