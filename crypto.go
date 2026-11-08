package main

// Cifrado en reposo.
//
// - Una clave maestra de 32 bytes (master.key, permisos 0600).
// - De ella se derivan subclaves con HKDF-SHA256, una por propósito
//   (estado, enlaces) y una distinta por cada archivo.
// - El estado (lista de elementos, textos, llaves, sesiones) se guarda
//   como un único bloque AES-256-GCM.
// - Cada archivo se cifra en trozos de 64 KiB con AES-256-GCM. El nonce
//   lleva el número de trozo y un indicador de "último trozo", así que
//   no se pueden reordenar, quitar ni truncar trozos sin que se detecte.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	chunkSize = 64 * 1024
	tagSize   = 16
)

type Keyring struct{ master []byte }

func loadOrCreateMasterKey(path string) (*Keyring, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		if len(b) != 32 {
			return nil, fmt.Errorf("%s no tiene 32 bytes", path)
		}
		if st, err := os.Stat(path); err == nil {
			if err := checkKeyPerms(path, st.Mode()); err != nil {
				return nil, err
			}
		}
		return &Keyring{master: b}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(key); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	return &Keyring{master: key}, f.Close()
}

func (k *Keyring) derive(label string, id []byte) []byte {
	key, err := hkdf.Key(sha256.New, k.master, nil, label+"\x00"+string(id), 32)
	if err != nil {
		panic(err)
	}
	return key
}

func (k *Keyring) aead(label string, id []byte) cipher.AEAD {
	block, err := aes.NewCipher(k.derive(label, id))
	if err != nil {
		panic(err)
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return g
}

// sealBlob: nonce aleatorio (12) || ciphertext.
func sealBlob(a cipher.AEAD, plain, aad []byte) []byte {
	nonce := make([]byte, a.NonceSize(), a.NonceSize()+len(plain)+a.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return a.Seal(nonce, nonce, plain, aad)
}

func openBlob(a cipher.AEAD, blob, aad []byte) ([]byte, error) {
	if len(blob) < a.NonceSize()+a.Overhead() {
		return nil, errors.New("bloque cifrado demasiado corto")
	}
	return a.Open(nil, blob[:a.NonceSize()], blob[a.NonceSize():], aad)
}

func chunkNonce(i uint64, last bool) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n, i)
	if last {
		n[8] = 1
	}
	return n
}

// encryptedSize devuelve el tamaño en disco de un archivo de "size" bytes.
func encryptedSize(size int64) int64 {
	if size == 0 {
		return tagSize
	}
	n := (size + chunkSize - 1) / chunkSize
	return size + n*tagSize
}

// ---- Escritura cifrada por trozos ----

type encWriter struct {
	w     io.Writer
	a     cipher.AEAD
	aad   []byte
	buf   []byte
	idx   uint64
	total int64
}

func newEncWriter(w io.Writer, a cipher.AEAD, aad []byte) *encWriter {
	return &encWriter{w: w, a: a, aad: aad, buf: make([]byte, 0, chunkSize*2)}
}

func (e *encWriter) Write(p []byte) (int, error) {
	e.buf = append(e.buf, p...)
	e.total += int64(len(p))
	// Se deja siempre al menos un trozo en el buffer: el último se sella en Close.
	for len(e.buf) > chunkSize {
		if err := e.flush(e.buf[:chunkSize], false); err != nil {
			return 0, err
		}
		e.buf = append(e.buf[:0], e.buf[chunkSize:]...)
	}
	return len(p), nil
}

func (e *encWriter) flush(chunk []byte, last bool) error {
	out := e.a.Seal(nil, chunkNonce(e.idx, last), chunk, e.aad)
	e.idx++
	_, err := e.w.Write(out)
	return err
}

func (e *encWriter) Close() error { return e.flush(e.buf, true) }

// ---- Lectura descifrada con soporte de Seek (para Range / vistas previas) ----

type decReader struct {
	f       *os.File
	a       cipher.AEAD
	aad     []byte
	size    int64
	nChunks int64
	pos     int64
	cur     int64
	plain   []byte
	cbuf    []byte
}

func openDecReader(path string, a cipher.AEAD, aad []byte, size int64) (*decReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if st.Size() != encryptedSize(size) {
		f.Close()
		return nil, errors.New("el archivo cifrado tiene un tamaño inesperado (¿manipulado?)")
	}
	n := (size + chunkSize - 1) / chunkSize
	if n == 0 {
		n = 1
	}
	return &decReader{f: f, a: a, aad: aad, size: size, nChunks: n, cur: -1, cbuf: make([]byte, chunkSize+tagSize)}, nil
}

func (d *decReader) load(idx int64) error {
	plainLen := min(int64(chunkSize), d.size-idx*chunkSize)
	if plainLen < 0 {
		plainLen = 0
	}
	ct := d.cbuf[:plainLen+tagSize]
	if _, err := d.f.ReadAt(ct, idx*(chunkSize+tagSize)); err != nil {
		return err
	}
	p, err := d.a.Open(d.plain[:0], chunkNonce(uint64(idx), idx == d.nChunks-1), ct, d.aad)
	if err != nil {
		return errors.New("fallo de integridad al descifrar (¿archivo manipulado?)")
	}
	d.plain, d.cur = p, idx
	return nil
}

func (d *decReader) Read(p []byte) (int, error) {
	if d.pos >= d.size {
		return 0, io.EOF
	}
	idx := d.pos / chunkSize
	if idx != d.cur {
		if err := d.load(idx); err != nil {
			return 0, err
		}
	}
	n := copy(p, d.plain[d.pos-idx*chunkSize:])
	d.pos += int64(n)
	return n, nil
}

func (d *decReader) Seek(off int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = off
	case io.SeekCurrent:
		abs = d.pos + off
	case io.SeekEnd:
		abs = d.size + off
	default:
		return 0, errors.New("whence inválido")
	}
	if abs < 0 {
		return 0, errors.New("posición negativa")
	}
	d.pos = abs
	return abs, nil
}

func (d *decReader) Close() error { return d.f.Close() }
