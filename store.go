package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

type Item struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"` // "text" | "file"
	Text    string    `json:"text,omitempty"`
	Name    string    `json:"name,omitempty"`
	Mime    string    `json:"mime,omitempty"`
	Size    int64     `json:"size"`
	From    string    `json:"from"`
	Created time.Time `json:"created"`
	Pinned  bool      `json:"pinned"`
	Session string    `json:"session,omitempty"` // sesión que lo subió ("" = terminal del portátil)
}

type StoredCred struct {
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Added    time.Time           `json:"added"`
	LastUsed time.Time           `json:"lastUsed"`
	Cred     webauthn.Credential `json:"cred"`
}

type Session struct {
	ID       string    `json:"id"`   // identificador público (para revocar)
	Hash     string    `json:"hash"` // sha256 del token de la cookie; el token en sí nunca se guarda
	Device   string    `json:"device"`
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"lastSeen"`
	Expires  time.Time `json:"expires"`
}

type Settings struct {
	RetentionDays int  `json:"retentionDays"` // 0 = nunca borrar
	SessionDays   int  `json:"sessionDays"`
	ClipboardOff  bool `json:"clipboardOff"` // no copiar al portapapeles del portátil
}

type State struct {
	Version  int           `json:"version"`
	UserID   []byte        `json:"userId"`
	Items    []*Item       `json:"items"`
	Creds    []*StoredCred `json:"creds"`
	Sessions []*Session    `json:"sessions"`
	Settings Settings      `json:"settings"`
}

type Store struct {
	mu    sync.Mutex
	dir   string
	keys  *Keyring
	st    *State
	dirty bool
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func openStore(dir string, keys *Keyring) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, keys: keys}
	b, err := os.ReadFile(s.statePath())
	switch {
	case errors.Is(err, os.ErrNotExist):
		uid := make([]byte, 32)
		rand.Read(uid)
		s.st = &State{Version: 1, UserID: uid, Settings: Settings{RetentionDays: 30, SessionDays: 14}}
		return s, s.saveLocked()
	case err != nil:
		return nil, err
	}
	plain, err := openBlob(keys.aead("state", nil), b, []byte("cofre-state-v1"))
	if err != nil {
		return nil, fmt.Errorf("no se pudo descifrar el estado (¿master.key distinta?): %w", err)
	}
	var st State
	if err := json.Unmarshal(plain, &st); err != nil {
		return nil, err
	}
	s.st = &st
	return s, nil
}

func (s *Store) statePath() string { return filepath.Join(s.dir, "state.enc") }

func (s *Store) blobPath(id string) string { return filepath.Join(s.dir, "blobs", id) }

func (s *Store) saveLocked() error {
	plain, err := json.Marshal(s.st)
	if err != nil {
		return err
	}
	blob := sealBlob(s.keys.aead("state", nil), plain, []byte("cofre-state-v1"))
	if err := writeFileAtomic(s.statePath(), blob); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// update ejecuta fn con el candado tomado y guarda si fn devuelve nil.
func (s *Store) update(fn func(st *State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fn(s.st); err != nil {
		return err
	}
	return s.saveLocked()
}

func (s *Store) view(fn func(st *State)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s.st)
}

// ---- Elementos ----

func (s *Store) Items() []Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Item, 0, len(s.st.Items))
	for _, it := range s.st.Items {
		out = append(out, *it)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		return out[i].Created.After(out[j].Created)
	})
	return out
}

func (s *Store) Item(id string) (Item, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.st.Items {
		if it.ID == id {
			return *it, true
		}
	}
	return Item{}, false
}

func (s *Store) AddText(text, from, session string) (*Item, error) {
	it := &Item{ID: newID(), Kind: "text", Text: text, Size: int64(len(text)), From: from, Session: session, Created: time.Now()}
	return it, s.update(func(st *State) error { st.Items = append(st.Items, it); return nil })
}

// AddFile cifra el contenido de r a disco mientras lo lee (sin cargarlo en memoria).
func (s *Store) AddFile(name, mimeType, from, session string, r io.Reader) (*Item, error) {
	id := newID()
	path := s.blobPath(id)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(path)
		}
	}()
	ew := newEncWriter(f, s.keys.aead("file", []byte(id)), []byte(id))
	if _, err := io.Copy(ew, r); err != nil {
		return nil, err
	}
	if err := ew.Close(); err != nil {
		return nil, err
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	it := &Item{ID: id, Kind: "file", Name: name, Mime: mimeType, Size: ew.total, From: from, Session: session, Created: time.Now()}
	if err := s.update(func(st *State) error { st.Items = append(st.Items, it); return nil }); err != nil {
		return nil, err
	}
	ok = true
	return it, nil
}

func (s *Store) OpenFile(it Item) (*decReader, error) {
	return openDecReader(s.blobPath(it.ID), s.keys.aead("file", []byte(it.ID)), []byte(it.ID), it.Size)
}

func (s *Store) Delete(ids ...string) error {
	del := map[string]bool{}
	for _, id := range ids {
		del[id] = true
	}
	var removed []string
	err := s.update(func(st *State) error {
		kept := st.Items[:0]
		for _, it := range st.Items {
			if del[it.ID] {
				if it.Kind == "file" {
					removed = append(removed, it.ID)
				}
				continue
			}
			kept = append(kept, it)
		}
		st.Items = kept
		return nil
	})
	for _, id := range removed {
		os.Remove(s.blobPath(id))
	}
	return err
}

func (s *Store) SetPinned(id string, pinned bool) error {
	return s.update(func(st *State) error {
		for _, it := range st.Items {
			if it.ID == id {
				it.Pinned = pinned
				return nil
			}
		}
		return errors.New("no existe")
	})
}

// Cleanup borra elementos caducados (no fijados), sesiones caducadas y blobs huérfanos.
func (s *Store) Cleanup() (changed bool) {
	now := time.Now()
	var expired []string
	s.view(func(st *State) {
		if st.Settings.RetentionDays > 0 {
			limit := now.AddDate(0, 0, -st.Settings.RetentionDays)
			for _, it := range st.Items {
				if !it.Pinned && it.Created.Before(limit) {
					expired = append(expired, it.ID)
				}
			}
		}
	})
	if len(expired) > 0 {
		s.Delete(expired...)
		changed = true
	}
	s.update(func(st *State) error {
		kept := st.Sessions[:0]
		for _, se := range st.Sessions {
			if se.Expires.After(now) {
				kept = append(kept, se)
			}
		}
		st.Sessions = kept
		return nil
	})
	// Blobs huérfanos (p. ej. subida interrumpida por un apagón).
	known := map[string]bool{}
	s.view(func(st *State) {
		for _, it := range st.Items {
			known[it.ID] = true
		}
	})
	entries, _ := os.ReadDir(filepath.Join(s.dir, "blobs"))
	for _, e := range entries {
		if !known[e.Name()] {
			if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > time.Hour {
				os.Remove(filepath.Join(s.dir, "blobs", e.Name()))
			}
		}
	}
	return changed
}

// ---- Sesiones ----

func (s *Store) NewSession(device string) (token string, err error) {
	token = newToken()
	now := time.Now()
	err = s.update(func(st *State) error {
		days := st.Settings.SessionDays
		if days <= 0 {
			days = 14
		}
		st.Sessions = append(st.Sessions, &Session{
			ID: newID()[:12], Hash: hashToken(token), Device: device,
			Created: now, LastSeen: now, Expires: now.AddDate(0, 0, days),
		})
		return nil
	})
	return token, err
}

// SessionFor valida un token. Actualiza LastSeen en memoria y solo
// lo persiste de vez en cuando para no escribir en disco en cada petición.
func (s *Store) SessionFor(token string) (Session, bool) {
	if token == "" {
		return Session{}, false
	}
	h := hashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, se := range s.st.Sessions {
		if se.Hash == h {
			if now.After(se.Expires) {
				return Session{}, false
			}
			if now.Sub(se.LastSeen) > 10*time.Minute {
				se.LastSeen = now
				s.saveLocked()
			}
			return *se, true
		}
	}
	return Session{}, false
}

func (s *Store) DeleteSession(id string) error {
	return s.update(func(st *State) error {
		kept := st.Sessions[:0]
		for _, se := range st.Sessions {
			if se.ID != id {
				kept = append(kept, se)
			}
		}
		st.Sessions = kept
		return nil
	})
}

// ---- Usuario WebAuthn (solo hay uno: tú) ----

type waUser struct {
	id    []byte
	creds []webauthn.Credential
}

func (u waUser) WebAuthnID() []byte                         { return u.id }
func (u waUser) WebAuthnName() string                       { return "cofre" }
func (u waUser) WebAuthnDisplayName() string                { return "Mi Cofre" }
func (u waUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func (s *Store) User() waUser {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := waUser{id: s.st.UserID}
	for _, c := range s.st.Creds {
		u.creds = append(u.creds, c.Cred)
	}
	return u
}

func (s *Store) HasCreds() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.st.Creds) > 0
}
