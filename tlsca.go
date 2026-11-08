package main

// Certificados TLS propios.
//
// Se genera una CA que SOLO puede firmar certificados para tu hostname
// (Name Constraints críticas), y con ella un certificado de servidor.
// La clave privada de la CA vive solo en memoria y se descarta al
// terminar: nadie (ni siquiera alguien que robe tu disco) puede usar
// esa CA para suplantar otras webs en tus dispositivos.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Apple no acepta certificados de servidor de más de 825 días.
const certValidity = 800 * 24 * time.Hour

type tlsPaths struct{ cert, key, ca string }

func tlsFiles(dir string) tlsPaths {
	d := filepath.Join(dir, "tls")
	return tlsPaths{
		cert: filepath.Join(d, "server.crt"),
		key:  filepath.Join(d, "server.key"),
		ca:   filepath.Join(d, "cofre-ca.crt"),
	}
}

// ensureTLS reutiliza el certificado si sigue siendo válido para host;
// si no, genera CA + certificado nuevos. Devuelve true si se regeneraron.
func ensureTLS(dir, host string) (tlsPaths, bool, error) {
	p := tlsFiles(dir)
	if leaf, err := readCert(p.cert); err == nil {
		if leaf.VerifyHostname(host) == nil && time.Until(leaf.NotAfter) > 15*24*time.Hour {
			if _, err := os.Stat(p.key); err == nil {
				return p, false, nil
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(p.cert), 0o700); err != nil {
		return p, false, err
	}
	now := time.Now().Add(-5 * time.Minute)

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return p, false, err
	}
	caTpl := &x509.Certificate{
		SerialNumber:                randSerial(),
		Subject:                     pkix.Name{CommonName: "Cofre CA (" + host + ")", Organization: []string{"Cofre personal"}},
		NotBefore:                   now,
		NotAfter:                    now.Add(certValidity + 24*time.Hour),
		IsCA:                        true,
		BasicConstraintsValid:       true,
		MaxPathLenZero:              true,
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{host},
		ExcludedIPRanges: []*net.IPNet{
			{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)},
			{IP: net.IPv6zero, Mask: net.CIDRMask(0, 128)},
		},
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		return p, false, err
	}
	ca, _ := x509.ParseCertificate(caDER)

	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return p, false, err
	}
	srvTpl := &x509.Certificate{
		SerialNumber: randSerial(),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    now,
		NotAfter:     now.Add(certValidity),
		DNSNames:     []string{host},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTpl, ca, &srvKey.PublicKey, caKey)
	if err != nil {
		return p, false, err
	}
	caKey = nil // la clave de la CA no se guarda nunca

	keyDER, err := x509.MarshalECPrivateKey(srvKey)
	if err != nil {
		return p, false, err
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	writes := []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{p.key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600},
		{p.cert, append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srvDER}), caPEM...), 0o644},
		{p.ca, caPEM, 0o644},
	}
	for _, w := range writes {
		if err := writeFileAtomic(w.path, w.data); err != nil {
			return p, false, err
		}
		os.Chmod(w.path, w.mode)
	}
	return p, true, nil
}

func randSerial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}

func readCert(path string) (*x509.Certificate, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, errors.New("PEM inválido")
	}
	return x509.ParseCertificate(blk.Bytes)
}

func fingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}
