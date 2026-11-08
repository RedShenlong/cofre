package main

import (
	"bytes"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func testKeys(t *testing.T) *Keyring {
	k, err := loadOrCreateMasterKey(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestFileRoundTripAndSeek(t *testing.T) {
	keys := testKeys(t)
	dir := t.TempDir()
	st, err := openStore(dir, keys)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{0, 1, chunkSize - 1, chunkSize, chunkSize + 1, 3*chunkSize + 77} {
		data := make([]byte, size)
		rand.Read(data)
		it, err := st.AddFile("x.bin", "application/octet-stream", "test", "", bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(st.blobPath(it.ID))
		if size > 16 && bytes.Contains(raw, data[:16]) {
			t.Fatal("el texto plano aparece en disco")
		}
		rd, err := st.OpenFile(*it)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(rd)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("size %d: contenido distinto (err=%v)", size, err)
		}
		if size > 100 {
			off := int64(size / 2)
			rd.Seek(off, io.SeekStart)
			buf := make([]byte, 10)
			io.ReadFull(rd, buf)
			if !bytes.Equal(buf, data[off:off+10]) {
				t.Fatalf("seek incorrecto")
			}
		}
		rd.Close()
	}
}

func TestTamperDetected(t *testing.T) {
	keys := testKeys(t)
	st, _ := openStore(t.TempDir(), keys)
	data := bytes.Repeat([]byte("secreto "), 30000)
	it, _ := st.AddFile("a.txt", "text/plain", "t", "", bytes.NewReader(data))
	p := st.blobPath(it.ID)
	raw, _ := os.ReadFile(p)

	// 1) bit cambiado
	mod := append([]byte(nil), raw...)
	mod[100] ^= 1
	os.WriteFile(p, mod, 0o600)
	rd, _ := st.OpenFile(*it)
	if _, err := io.ReadAll(rd); err == nil {
		t.Fatal("no se detectó la modificación")
	}
	rd.Close()

	// 2) trozos intercambiados
	cs := chunkSize + tagSize
	mod = append([]byte(nil), raw...)
	copy(mod[0:cs], raw[cs:2*cs])
	copy(mod[cs:2*cs], raw[0:cs])
	os.WriteFile(p, mod, 0o600)
	rd, _ = st.OpenFile(*it)
	if _, err := io.ReadAll(rd); err == nil {
		t.Fatal("no se detectó el reordenado")
	}
	rd.Close()

	// 3) truncado
	os.WriteFile(p, raw[:len(raw)-10], 0o600)
	if _, err := st.OpenFile(*it); err == nil {
		t.Fatal("no se detectó el truncado")
	}
}

func TestStateEncryptedAndPersistent(t *testing.T) {
	keys := testKeys(t)
	dir := t.TempDir()
	st, _ := openStore(dir, keys)
	st.AddText("mi contraseña súper secreta", "test", "")
	raw, _ := os.ReadFile(filepath.Join(dir, "state.enc"))
	if bytes.Contains(raw, []byte("secreta")) {
		t.Fatal("el texto aparece sin cifrar en disco")
	}
	st2, err := openStore(dir, keys)
	if err != nil || len(st2.Items()) != 1 {
		t.Fatalf("no se recuperó el estado: %v", err)
	}
	if _, err := openStore(dir, testKeys(t)); err == nil {
		t.Fatal("se abrió el estado con otra clave")
	}
}

func TestTLSNameConstraints(t *testing.T) {
	dir := t.TempDir()
	p, regen, err := ensureTLS(dir, "prueba.local")
	if err != nil || !regen {
		t.Fatal(err)
	}
	ca, _ := readCert(p.ca)
	if !ca.PermittedDNSDomainsCritical || ca.PermittedDNSDomains[0] != "prueba.local" {
		t.Fatal("la CA no está restringida")
	}
	if _, regen, _ := ensureTLS(dir, "prueba.local"); regen {
		t.Fatal("regeneró sin necesidad")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "tls"))
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(dir, "tls", e.Name()))
		if e.Name() != "server.key" && bytes.Contains(b, []byte("PRIVATE KEY")) {
			t.Fatalf("%s contiene una clave privada", e.Name())
		}
	}
}

func TestLinksAndCodes(t *testing.T) {
	keys := testKeys(t)
	st, _ := openStore(t.TempDir(), keys)
	s, err := newServer(st, keys, "prueba.local", 8443, tlsPaths{}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	tok := s.signLink("abc")
	if !s.checkLink("abc", tok) || s.checkLink("abd", tok) || s.checkLink("abc", tok+"x") {
		t.Fatal("firma de enlaces incorrecta")
	}
	code, _ := s.newEnrollCode()
	for i := 0; i < 5; i++ {
		s.checkEnrollCode("AAAA-AAAA")
	}
	if s.checkEnrollCode(code) {
		t.Fatal("el código debería invalidarse tras 5 fallos")
	}
	code, _ = s.newEnrollCode()
	if !s.checkEnrollCode(code) || s.checkEnrollCode(code) {
		t.Fatal("el código debe valer una sola vez")
	}
}
