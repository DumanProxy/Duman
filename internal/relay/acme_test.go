package relay

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildTLSConfig_SelfSigned(t *testing.T) {
	cfg := TLSConfig{Mode: TLSModeSelfSigned}
	tlsCfg, err := BuildTLSConfig(cfg, nil)
	if err != nil {
		t.Fatalf("BuildTLSConfig(self_signed): %v", err)
	}
	if tlsCfg == nil {
		t.Fatal("expected non-nil tls.Config")
	}
	if len(tlsCfg.Certificates) == 0 {
		t.Fatal("expected at least one certificate")
	}
	if tlsCfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want TLS 1.2 (%d)", tlsCfg.MinVersion, tls.VersionTLS12)
	}
}

func TestBuildTLSConfig_DefaultMode(t *testing.T) {
	// Empty/unknown mode falls through to self-signed
	cfg := TLSConfig{Mode: ""}
	tlsCfg, err := BuildTLSConfig(cfg, nil)
	if err != nil {
		t.Fatalf("BuildTLSConfig(default): %v", err)
	}
	if tlsCfg == nil {
		t.Fatal("expected non-nil tls.Config for default mode")
	}
}

func TestBuildTLSConfig_UnknownMode(t *testing.T) {
	cfg := TLSConfig{Mode: "unknown_mode"}
	tlsCfg, err := BuildTLSConfig(cfg, nil)
	if err != nil {
		t.Fatalf("BuildTLSConfig(unknown): %v", err)
	}
	if tlsCfg == nil {
		t.Fatal("expected non-nil tls.Config for unknown mode (falls back to self-signed)")
	}
}

func TestBuildTLSConfig_ACME(t *testing.T) {
	cfg := TLSConfig{
		Mode:   TLSModeACME,
		Domain: "example.com",
	}
	tlsCfg, err := BuildTLSConfig(cfg, nil)
	if err != nil {
		t.Fatalf("BuildTLSConfig(acme): %v", err)
	}
	if tlsCfg == nil {
		t.Fatal("expected non-nil tls.Config for ACME")
	}
	// ACME config uses GetCertificate, so Certificates may be empty
	if tlsCfg.GetCertificate == nil {
		t.Error("expected GetCertificate to be set for ACME mode")
	}
}

func TestBuildTLSConfig_ACME_NoDomain(t *testing.T) {
	cfg := TLSConfig{
		Mode:   TLSModeACME,
		Domain: "",
	}
	_, err := BuildTLSConfig(cfg, nil)
	if err == nil {
		t.Fatal("expected error when ACME domain is empty")
	}
}

func TestBuildTLSConfig_Manual(t *testing.T) {
	tmpDir := t.TempDir()
	certFile := filepath.Join(tmpDir, "cert.pem")
	keyFile := filepath.Join(tmpDir, "key.pem")

	writeTestCertFiles(t, certFile, keyFile)

	cfg := TLSConfig{
		Mode:     TLSModeManual,
		CertFile: certFile,
		KeyFile:  keyFile,
	}
	tlsCfg, err := BuildTLSConfig(cfg, nil)
	if err != nil {
		t.Fatalf("BuildTLSConfig(manual): %v", err)
	}
	if tlsCfg == nil {
		t.Fatal("expected non-nil tls.Config for manual mode")
	}
	if len(tlsCfg.Certificates) == 0 {
		t.Fatal("expected certificates in manual mode")
	}
	if tlsCfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want TLS 1.2 (%d)", tlsCfg.MinVersion, tls.VersionTLS12)
	}
}

func TestBuildTLSConfig_Manual_BadFiles(t *testing.T) {
	cfg := TLSConfig{
		Mode:     TLSModeManual,
		CertFile: "/nonexistent/cert.pem",
		KeyFile:  "/nonexistent/key.pem",
	}
	_, err := BuildTLSConfig(cfg, nil)
	if err == nil {
		t.Fatal("expected error for missing cert files")
	}
}

func TestBuildSelfSignedTLS_WithLogger(t *testing.T) {
	tlsCfg, err := buildSelfSignedTLS(slog.Default())
	if err != nil {
		t.Fatalf("buildSelfSignedTLS: %v", err)
	}
	if tlsCfg == nil {
		t.Fatal("expected non-nil tls.Config")
	}
}

// writeTestCertFiles generates a self-signed ECDSA cert+key and writes PEM files.
func writeTestCertFiles(t *testing.T, certFile, keyFile string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test"},
			CommonName:   "localhost",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(1 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(certFile, certPEM, 0644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}
}
