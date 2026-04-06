package relay

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// TLSMode configures how TLS certificates are obtained.
type TLSMode string

const (
	TLSModeACME       TLSMode = "acme"
	TLSModeManual     TLSMode = "manual"
	TLSModeSelfSigned TLSMode = "self_signed"
)

// TLSConfig holds resolved TLS configuration.
type TLSConfig struct {
	Mode     TLSMode
	Domain   string
	CertFile string
	KeyFile  string
}

// BuildTLSConfig creates a *tls.Config based on the TLS mode.
func BuildTLSConfig(cfg TLSConfig, logger *slog.Logger) (*tls.Config, error) {
	if logger == nil {
		logger = slog.Default()
	}

	switch cfg.Mode {
	case TLSModeACME:
		return buildACMETLS(cfg.Domain, logger)
	case TLSModeManual:
		return buildManualTLS(cfg.CertFile, cfg.KeyFile)
	case TLSModeSelfSigned:
		return buildSelfSignedTLS(logger)
	default:
		return buildSelfSignedTLS(logger)
	}
}

// buildACMETLS uses Let's Encrypt autocert for automatic certificate management.
func buildACMETLS(domain string, logger *slog.Logger) (*tls.Config, error) {
	if domain == "" {
		return nil, fmt.Errorf("acme: domain required")
	}

	manager := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(os.TempDir() + "/duman-certs"),
		HostPolicy: autocert.HostWhitelist(domain),
	}

	logger.Info("ACME TLS enabled", "domain", domain)

	return manager.TLSConfig(), nil
}

// buildManualTLS loads a certificate from files.
func buildManualTLS(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load certificate: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// buildSelfSignedTLS generates a self-signed certificate for development.
func buildSelfSignedTLS(logger *slog.Logger) (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Duman Development"},
			CommonName:   "localhost",
		},
		NotBefore: time.Now().Add(-1 * time.Hour),
		NotAfter:  time.Now().Add(365 * 24 * time.Hour),

		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,

		IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:    []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse key pair: %w", err)
	}

	logger.Info("self-signed TLS certificate generated",
		"expires", template.NotAfter.Format("2006-01-02"))

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}
