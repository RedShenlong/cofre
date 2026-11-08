package main

// Prueba de extremo a extremo con una llave FIDO2 simulada por software:
// alta → login → subir → listar → descargar → sesión revocada.

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

const (
	tHost   = "prueba.local"
	tOrigin = "https://prueba.local:8443"
)

var b64 = base64.RawURLEncoding

type softKey struct {
	priv  *ecdsa.PrivateKey
	id    []byte
	count uint32
}

func newSoftKey() *softKey {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	id := make([]byte, 32)
	rand.Read(id)
	return &softKey{priv: k, id: id}
}

func (k *softKey) authData(flags byte, attested bool) []byte {
	rp := sha256.Sum256([]byte(tHost))
	var b bytes.Buffer
	b.Write(rp[:])
	b.WriteByte(flags)
	k.count++
	binary.Write(&b, binary.BigEndian, k.count)
	if attested {
		b.Write(make([]byte, 16)) // AAGUID
		binary.Write(&b, binary.BigEndian, uint16(len(k.id)))
		b.Write(k.id)
		x := k.priv.X.FillBytes(make([]byte, 32))
		y := k.priv.Y.FillBytes(make([]byte, 32))
		cose, _ := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
		b.Write(cose)
	}
	return b.Bytes()
}

func clientData(typ, challenge string) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": tOrigin, "crossOrigin": false})
	return b
}

func (k *softKey) create(challenge string) map[string]any {
	cd := clientData("webauthn.create", challenge)
	att, _ := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": k.authData(0x45, true)})
	return map[string]any{
		"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key",
		"clientExtensionResults": map[string]any{},
		"response":               map[string]any{"attestationObject": b64.EncodeToString(att), "clientDataJSON": b64.EncodeToString(cd), "transports": []string{"nfc", "usb"}},
	}
}

func (k *softKey) get(challenge string) map[string]any {
	cd := clientData("webauthn.get", challenge)
	ad := k.authData(0x05, false)
	h := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), h[:]...))
	sig, _ := ecdsa.SignASN1(rand.Reader, k.priv, digest[:])
	return map[string]any{
		"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key",
		"clientExtensionResults": map[string]any{},
		"response":               map[string]any{"authenticatorData": b64.EncodeToString(ad), "clientDataJSON": b64.EncodeToString(cd), "signature": b64.EncodeToString(sig)},
	}
}

// Cliente mínimo que guarda cookies a mano (las __Host- exigen HTTPS en un cookiejar real).
type tClient struct {
	t       *testing.T
	h       http.Handler
	cookies map[string]string
	remote  string // IP de origen ("" = el propio portátil)
}

func (c *tClient) do(method, path string, body io.Reader, ctype string) (int, []byte, http.Header) {
	req := httptest.NewRequest(method, "https://"+tHost+":8443"+path, body)
	req.Host = tHost + ":8443"
	req.RemoteAddr = "127.0.0.1:40000"
	if c.remote != "" {
		req.RemoteAddr = c.remote + ":40000"
	}
	if method != "GET" {
		req.Header.Set("Origin", tOrigin)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0) Safari/604.1")
	for k, v := range c.cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.MaxAge < 0 {
			delete(c.cookies, ck.Name)
		} else {
			c.cookies[ck.Name] = ck.Value
		}
	}
	return rec.Code, rec.Body.Bytes(), rec.Header()
}

func (c *tClient) json(method, path string, in any) (int, map[string]any) {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	code, b, _ := c.do(method, path, body, "application/json")
	var out map[string]any
	json.Unmarshal(b, &out)
	return code, out
}

func challengeOf(opts map[string]any) string {
	return opts["publicKey"].(map[string]any)["challenge"].(string)
}

func TestFullFlowWithSoftKey(t *testing.T) {
	keys := testKeys(t)
	st, _ := openStore(t.TempDir(), keys)
	s, err := newServer(st, keys, tHost, 8443, tlsPaths{}, 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	h := s.browserHandler()
	c := &tClient{t: t, h: h, cookies: map[string]string{}}
	key := newSoftKey()

	// Alta con código
	// Alguien en la wifi con un token válido (p. ej. robado) NO puede registrar: no es el portátil.
	stolen, _ := s.newRegisterToken()
	lan := &tClient{t: t, h: h, cookies: map[string]string{}, remote: "192.0.2.50"}
	if code, _ := lan.json("POST", "/api/enroll/begin", map[string]string{"token": stolen, "name": "intruso"}); code != 403 {
		t.Fatalf("registro desde otra IP debería fallar, fue %d", code)
	}
	// Desde el propio portátil, con el enlace de "cofre registrar"
	reg, _ := s.newRegisterToken()
	if code, _ := c.json("POST", "/api/enroll/begin", map[string]string{"token": "inventado", "name": "x"}); code != 403 {
		t.Fatal("token inventado aceptado")
	}
	st1, opts := c.json("POST", "/api/enroll/begin", map[string]string{"token": reg, "name": "Titan"})
	if st1 != 200 {
		t.Fatalf("enroll begin: %d %v", st1, opts)
	}
	pk := opts["publicKey"].(map[string]any)
	if pk["authenticatorSelection"].(map[string]any)["authenticatorAttachment"] != "cross-platform" {
		t.Fatal("se esperaba exigir llave física")
	}
	st2, res := c.json("POST", "/api/enroll/finish", key.create(challengeOf(opts)))
	if st2 != 200 {
		t.Fatalf("enroll finish: %d %v", st2, res)
	}
	if c.cookies[sessionCookie] == "" {
		t.Fatal("tras el alta debería haber sesión")
	}

	// Subir un archivo y un texto
	var mp bytes.Buffer
	mw := multipart.NewWriter(&mp)
	fw, _ := mw.CreateFormFile("file", "../../etc/apuntes tema 3.pdf")
	pdf := bytes.Repeat([]byte("%PDF-1.4 contenido "), 10000)
	fw.Write(pdf)
	mw.Close()
	if code, b, _ := c.do("POST", "/api/upload", &mp, mw.FormDataContentType()); code != 200 {
		t.Fatalf("upload: %d %s", code, b)
	}
	if code, _ := c.json("POST", "/api/text", map[string]string{"text": "respuesta 4: b"}); code != 200 {
		t.Fatal("texto")
	}

	_, list := c.json("GET", "/api/items", nil)
	items := list["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("esperaba 2 elementos, hay %d", len(items))
	}
	var fileURL string
	for _, x := range items {
		it := x.(map[string]any)
		if it["kind"] == "file" {
			if it["name"] != "apuntes tema 3.pdf" || it["mime"] != "application/pdf" || it["from"] != "iPhone · Safari" {
				t.Fatalf("metadatos raros: %v", it)
			}
			fileURL = it["url"].(string)
		}
	}

	// Descargar con enlace firmado y SIN cookie
	anon := &tClient{t: t, h: h, cookies: map[string]string{}}
	code2, got, hdr := anon.do("GET", fileURL, nil, "")
	if code2 != 200 || !bytes.Equal(got, pdf) {
		t.Fatalf("descarga con enlace: %d (%d bytes)", code2, len(got))
	}
	if hdr.Get("Content-Type") != "application/pdf" || !strings.HasPrefix(hdr.Get("Content-Disposition"), "inline") {
		t.Fatalf("cabeceras: %v", hdr)
	}
	// Range (vista previa por partes)
	req := httptest.NewRequest("GET", "https://"+tHost+":8443"+fileURL, nil)
	req.Host = tHost + ":8443"
	req.Header.Set("Range", "bytes=70000-70009")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 206 || !bytes.Equal(rec.Body.Bytes(), pdf[70000:70010]) {
		t.Fatalf("range: %d", rec.Code)
	}
	// Sin enlace ni cookie → 401
	if code, _, _ := anon.do("GET", strings.Split(fileURL, "?")[0], nil, ""); code != 401 {
		t.Fatalf("raw sin auth debería ser 401, fue %d", code)
	}
	if code, _ := anon.json("GET", "/api/items", nil); code != 401 {
		t.Fatal("items sin sesión debería ser 401")
	}

	// Login desde "otro dispositivo"
	other := &tClient{t: t, h: h, cookies: map[string]string{}}
	_, lopts := other.json("POST", "/api/login/begin", nil)
	if code, r := other.json("POST", "/api/login/finish", key.get(challengeOf(lopts))); code != 200 {
		t.Fatalf("login: %d %v", code, r)
	}
	// "mine": cada dispositivo sabe qué subió él (para no copiarse a sí mismo)
	mineCount := func(cl *tClient) (n int) {
		_, l := cl.json("GET", "/api/items", nil)
		for _, x := range l["items"].([]any) {
			if x.(map[string]any)["mine"] == true {
				n++
			}
			if _, leak := x.(map[string]any)["session"]; leak {
				t.Fatal("no se debe exponer el id de sesión")
			}
		}
		return
	}
	if mineCount(c) != 2 || mineCount(other) != 0 {
		t.Fatalf("mine incorrecto: %d / %d", mineCount(c), mineCount(other))
	}
	// Reutilizar el mismo reto (replay) debe fallar
	if code, _ := other.json("POST", "/api/login/finish", key.get(challengeOf(lopts))); code == 200 {
		t.Fatal("el reto no debería poder reutilizarse")
	}
	// Llave distinta → rechazada
	evil := newSoftKey()
	evil.id = key.id
	_, lopts = anon.json("POST", "/api/login/begin", nil)
	if code, _ := anon.json("POST", "/api/login/finish", evil.get(challengeOf(lopts))); code == 200 {
		t.Fatal("una llave falsa no debe entrar")
	}

	// Revocar la sesión del otro dispositivo desde el primero
	_, acc := c.json("GET", "/api/account", nil)
	for _, x := range acc["sessions"].([]any) {
		se := x.(map[string]any)
		if se["Current"] == false {
			c.json("DELETE", "/api/sessions/"+se["ID"].(string), nil)
		}
	}
	if code, _ := other.json("GET", "/api/items", nil); code != 401 {
		t.Fatal("la sesión revocada sigue funcionando")
	}

	// Añadir una segunda llave estando dentro (sin código)
	key2 := newSoftKey()
	_, o2 := c.json("POST", "/api/enroll/begin", map[string]string{"name": "Repuesto"})
	if code, r := c.json("POST", "/api/enroll/finish", key2.create(challengeOf(o2))); code != 200 {
		t.Fatalf("segunda llave: %d %v", code, r)
	}
	// Sin sesión y sin código no se puede añadir llave
	if code, _ := anon.json("POST", "/api/enroll/begin", map[string]string{"name": "x"}); code != 403 {
		t.Fatal("alta sin token debería fallar")
	}
}
