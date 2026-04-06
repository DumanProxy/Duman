package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoSharedSecretInLogs verifies that shared secrets are never logged.
func TestNoSharedSecretInLogs(t *testing.T) {
	dangerousPatterns := []string{
		"SharedSecret",
		"sharedSecret",
		"shared_secret",
	}

	err := filepath.Walk("../../internal", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		if strings.Contains(path, "_test.go") {
			return nil // skip tests
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := string(data)

		for _, pattern := range dangerousPatterns {
			// Check for logging of secrets: slog.*shared_secret, fmt.*SharedSecret, etc.
			lines := strings.Split(content, "\n")
			for i, line := range lines {
				lower := strings.ToLower(line)
				if strings.Contains(lower, "sharedsecret") || strings.Contains(lower, "shared_secret") {
					// Allow: field declarations, function params, config struct fields
					if strings.Contains(line, "slog.") && strings.Contains(lower, pattern) {
						t.Errorf("%s:%d potential secret leak in log: %s", path, i+1, strings.TrimSpace(line))
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestSOCKS5BindsToLocalhost verifies SOCKS5 defaults to localhost binding.
func TestSOCKS5BindsToLocalhost(t *testing.T) {
	data, err := os.ReadFile("../../internal/config/client.go")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// The default listen address should be localhost
	if !strings.Contains(content, "127.0.0.1") {
		t.Error("SOCKS5 default listen should include 127.0.0.1")
	}
}

// TestTLSMinVersion verifies TLS minimum version is 1.2.
func TestTLSMinVersion(t *testing.T) {
	files := []string{
		"../../internal/phantom/browser.go",
		"../../internal/smokescreen/peer.go",
		"../../internal/relay/acme.go",
	}

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue // file may not exist in all configs
		}
		content := string(data)

		if strings.Contains(content, "tls.Config") && !strings.Contains(content, "tls.VersionTLS12") {
			t.Errorf("%s: TLS config should set MinVersion to tls.VersionTLS12", path)
		}
	}
}

// TestConstantTimeHMACComparison verifies HMAC uses constant-time comparison.
func TestConstantTimeHMACComparison(t *testing.T) {
	data, err := os.ReadFile("../../internal/crypto/keys.go")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if strings.Contains(content, "ValidateAuthToken") {
		if !strings.Contains(content, "hmac.Equal") && !strings.Contains(content, "subtle.ConstantTimeCompare") {
			t.Error("HMAC validation should use constant-time comparison")
		}
	}
}

// TestNoHardcodedSecrets verifies no hardcoded secrets in source.
func TestNoHardcodedSecrets(t *testing.T) {
	suspiciousPatterns := []string{
		"password123",
		"secret123",
		"AKIA", // AWS key prefix
	}

	err := filepath.Walk("../../internal", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		if strings.Contains(path, "_test.go") {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		content := string(data)

		for _, pattern := range suspiciousPatterns {
			if strings.Contains(content, pattern) {
				t.Errorf("%s contains suspicious pattern %q", path, pattern)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
