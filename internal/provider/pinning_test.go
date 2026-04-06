package provider

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

// generateSelfSignedCert creates a self-signed TLS certificate for testing.
func generateSelfSignedCert(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()

	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privKey.PublicKey, privKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	privBytes, err := x509.MarshalECPrivateKey(privKey)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("x509 key pair: %v", err)
	}

	return tlsCert, certDER
}

func TestComputeCertPin(t *testing.T) {
	_, certDER := generateSelfSignedCert(t)
	pin := ComputeCertPin(certDER)

	if !strings.HasPrefix(pin, "sha256/") {
		t.Fatalf("pin should start with \"sha256/\", got %q", pin)
	}

	// Pin should be deterministic
	pin2 := ComputeCertPin(certDER)
	if pin != pin2 {
		t.Fatalf("pin not deterministic: %q != %q", pin, pin2)
	}

	// Different cert should produce different pin
	_, certDER2 := generateSelfSignedCert(t)
	pin3 := ComputeCertPin(certDER2)
	if pin == pin3 {
		t.Fatal("different certs produced same pin")
	}
}

func TestPinnedTLSConfig_ValidPin(t *testing.T) {
	tlsCert, certDER := generateSelfSignedCert(t)
	pin := ComputeCertPin(certDER)

	serverTLS := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// Accept one connection in background
	done := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		tlsConn := conn.(*tls.Conn)
		done <- tlsConn.Handshake()
	}()

	clientTLS, err := PinnedTLSConfig(pin)
	if err != nil {
		t.Fatalf("PinnedTLSConfig: %v", err)
	}

	conn, err := tls.Dial("tcp", ln.Addr().String(), clientTLS)
	if err != nil {
		t.Fatalf("dial with valid pin should succeed: %v", err)
	}
	conn.Close()

	if err := <-done; err != nil {
		t.Fatalf("server handshake: %v", err)
	}
}

func TestPinnedTLSConfig_InvalidPin(t *testing.T) {
	tlsCert, _ := generateSelfSignedCert(t)

	// Use a pin from a different certificate
	_, otherDER := generateSelfSignedCert(t)
	wrongPin := ComputeCertPin(otherDER)

	serverTLS := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// Server goroutine: accept and attempt handshake (will fail on server
	// side too, but we keep the conn open long enough for the client to
	// receive the VerifyPeerCertificate error).
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			return
		}
		srv := tls.Server(raw, serverTLS)
		_ = srv.Handshake() // ignore server-side error
		srv.Close()
	}()

	clientTLS, err := PinnedTLSConfig(wrongPin)
	if err != nil {
		t.Fatalf("PinnedTLSConfig: %v", err)
	}

	// Use net.Dial + tls.Client so we can call Handshake() explicitly
	// and get the VerifyPeerCertificate error directly.
	rawConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer rawConn.Close()

	tlsConn := tls.Client(rawConn, clientTLS)
	err = tlsConn.Handshake()
	if err == nil {
		tlsConn.Close()
		t.Fatal("handshake with wrong pin should fail")
	}
	// On Windows the connection may be reset before the Go TLS layer
	// surfaces the VerifyPeerCertificate error, so accept either.
	errStr := err.Error()
	if !strings.Contains(errStr, "certificate pin mismatch") &&
		!strings.Contains(errStr, "wsarecv") &&
		!strings.Contains(errStr, "connection reset") &&
		!strings.Contains(errStr, "connection was aborted") {
		t.Fatalf("expected pin mismatch or connection error, got: %v", err)
	}
}

func TestPinnedTLSConfig_InvalidFormat(t *testing.T) {
	cases := []struct {
		name string
		pin  string
	}{
		{"no prefix", "notsha256/AAAA"},
		{"missing slash", "sha256AAAA"},
		{"empty hash", "sha256/"},
		{"bad base64", "sha256/not-valid-base64!!!"},
		{"wrong hash length", "sha256/AAAA"},
		{"empty string", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := PinnedTLSConfig(tc.pin)
			if err == nil {
				t.Fatalf("PinnedTLSConfig(%q) should return error", tc.pin)
			}
		})
	}
}
