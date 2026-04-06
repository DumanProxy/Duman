package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestClientConfig_Defaults(t *testing.T) {
	cfg := &ClientConfig{}
	cfg.setDefaults()

	if cfg.Proxy.Listen != "127.0.0.1:1080" {
		t.Errorf("Proxy.Listen = %q, want %q", cfg.Proxy.Listen, "127.0.0.1:1080")
	}
	if cfg.Tunnel.ChunkSize != 16384 {
		t.Errorf("Tunnel.ChunkSize = %d, want 16384", cfg.Tunnel.ChunkSize)
	}
	if cfg.Scenario != "ecommerce" {
		t.Errorf("Scenario = %q, want %q", cfg.Scenario, "ecommerce")
	}
	if cfg.Tunnel.ResponseMode != "poll" {
		t.Errorf("Tunnel.ResponseMode = %q, want %q", cfg.Tunnel.ResponseMode, "poll")
	}
	if cfg.Tunnel.Cipher != "auto" {
		t.Errorf("Tunnel.Cipher = %q, want %q", cfg.Tunnel.Cipher, "auto")
	}
}

func TestClientConfig_Validate_OK(t *testing.T) {
	cfg := &ClientConfig{}
	cfg.setDefaults()

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestClientConfig_Validate_BadChunkSize(t *testing.T) {
	cfg := &ClientConfig{}
	cfg.setDefaults()
	cfg.Tunnel.ChunkSize = 100

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for small chunk size")
	}
}

func TestClientConfig_Validate_BadScenario(t *testing.T) {
	cfg := &ClientConfig{}
	cfg.setDefaults()
	cfg.Scenario = "invalid"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid scenario")
	}
}

func TestClientConfig_Validate_BadCipher(t *testing.T) {
	cfg := &ClientConfig{}
	cfg.setDefaults()
	cfg.Tunnel.Cipher = "des"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid cipher")
	}
}

func TestClientConfig_Validate_BadResponseMode(t *testing.T) {
	cfg := &ClientConfig{}
	cfg.setDefaults()
	cfg.Tunnel.ResponseMode = "invalid"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid response mode")
	}
}

func TestClientConfig_Validate_BadRelayProtocol(t *testing.T) {
	cfg := &ClientConfig{}
	cfg.setDefaults()
	cfg.Relays = []RelayEntry{{Address: "host:5432", Protocol: "ftp"}}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid relay protocol")
	}
}

func TestClientConfig_Validate_EmptyRelayAddress(t *testing.T) {
	cfg := &ClientConfig{}
	cfg.setDefaults()
	cfg.Relays = []RelayEntry{{Address: "", Protocol: "postgresql"}}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for empty relay address")
	}
}

func TestClientConfig_Validate_EmptyListen(t *testing.T) {
	cfg := &ClientConfig{}
	cfg.setDefaults()
	cfg.Proxy.Listen = ""

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for empty listen address")
	}
}

func TestClientConfig_LoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-client.yaml")
	data := []byte(`
proxy:
  listen: "127.0.0.1:9090"
tunnel:
  chunk_size: 8192
  cipher: chacha20
  response_mode: push
scenario: iot
log:
  level: debug
`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadClientConfig(path)
	if err != nil {
		t.Fatalf("LoadClientConfig: %v", err)
	}

	if cfg.Proxy.Listen != "127.0.0.1:9090" {
		t.Errorf("Listen = %q", cfg.Proxy.Listen)
	}
	if cfg.Tunnel.ChunkSize != 8192 {
		t.Errorf("ChunkSize = %d", cfg.Tunnel.ChunkSize)
	}
	if cfg.Scenario != "iot" {
		t.Errorf("Scenario = %q", cfg.Scenario)
	}
	if cfg.Tunnel.Cipher != "chacha20" {
		t.Errorf("Cipher = %q", cfg.Tunnel.Cipher)
	}
}

func TestClientConfig_LoadMissing(t *testing.T) {
	// Loading with no file should use defaults
	cfg, err := LoadClientConfig("")
	if err != nil {
		t.Fatalf("LoadClientConfig empty: %v", err)
	}
	if cfg.Proxy.Listen != "127.0.0.1:1080" {
		t.Errorf("expected default listen")
	}
}

func TestClientConfig_LoadInvalidPath(t *testing.T) {
	_, err := LoadClientConfig("/nonexistent/path.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestRelayConfig_Defaults(t *testing.T) {
	cfg := &RelayConfig{}
	cfg.setDefaults()

	if cfg.Listen.PostgreSQL != ":5432" {
		t.Errorf("Listen.PostgreSQL = %q", cfg.Listen.PostgreSQL)
	}
	if cfg.Auth.Method != "md5" {
		t.Errorf("Auth.Method = %q", cfg.Auth.Method)
	}
	if cfg.FakeData.Scenario != "ecommerce" {
		t.Errorf("FakeData.Scenario = %q", cfg.FakeData.Scenario)
	}
	if cfg.FakeData.Seed != 42 {
		t.Errorf("FakeData.Seed = %d", cfg.FakeData.Seed)
	}
}

func TestRelayConfig_Validate_OK(t *testing.T) {
	cfg := &RelayConfig{}
	cfg.setDefaults()

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestRelayConfig_Validate_NoListenAddress(t *testing.T) {
	cfg := &RelayConfig{}
	cfg.setDefaults()
	cfg.Listen.PostgreSQL = ""

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for no listen addresses")
	}
}

func TestRelayConfig_Validate_InvalidTLSMode(t *testing.T) {
	cfg := &RelayConfig{}
	cfg.setDefaults()
	cfg.TLS.Mode = "invalid"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid TLS mode")
	}
}

func TestRelayConfig_Validate_ManualTLS_MissingCert(t *testing.T) {
	cfg := &RelayConfig{}
	cfg.setDefaults()
	cfg.TLS.Mode = "manual"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for manual TLS without cert")
	}
}

func TestRelayConfig_Validate_InvalidAuth(t *testing.T) {
	cfg := &RelayConfig{}
	cfg.setDefaults()
	cfg.Auth.Method = "plain"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid auth method")
	}
}

func TestRelayConfig_Validate_NoUsers(t *testing.T) {
	cfg := &RelayConfig{}
	cfg.setDefaults()
	cfg.Auth.Users = map[string]string{}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for no users")
	}
}

func TestRelayConfig_Validate_InvalidScenario(t *testing.T) {
	cfg := &RelayConfig{}
	cfg.setDefaults()
	cfg.FakeData.Scenario = "mmorpg"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid scenario")
	}
}

func TestRelayConfig_Validate_InvalidRole(t *testing.T) {
	cfg := &RelayConfig{}
	cfg.setDefaults()
	cfg.Tunnel.Role = "proxy"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid role")
	}
}

func TestRelayConfig_LoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-relay.yaml")
	data := []byte(`
listen:
  postgresql: ":15432"
  mysql: ":13306"
auth:
  method: md5
  users:
    admin: secret
fake_data:
  scenario: iot
  seed: 99
log:
  level: warn
`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadRelayConfig(path)
	if err != nil {
		t.Fatalf("LoadRelayConfig: %v", err)
	}

	if cfg.Listen.PostgreSQL != ":15432" {
		t.Errorf("Listen.PostgreSQL = %q", cfg.Listen.PostgreSQL)
	}
	if cfg.Listen.MySQL != ":13306" {
		t.Errorf("Listen.MySQL = %q", cfg.Listen.MySQL)
	}
	if cfg.FakeData.Seed != 99 {
		t.Errorf("FakeData.Seed = %d", cfg.FakeData.Seed)
	}
}

func TestGenerateSharedSecret(t *testing.T) {
	secret, err := GenerateSharedSecret()
	if err != nil {
		t.Fatalf("GenerateSharedSecret: %v", err)
	}

	// Should be valid base64
	decoded, err := base64.StdEncoding.DecodeString(secret)
	if err != nil {
		t.Fatalf("not valid base64: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("decoded length = %d, want 32", len(decoded))
	}

	// Two calls should produce different secrets
	secret2, _ := GenerateSharedSecret()
	if secret == secret2 {
		t.Fatal("two calls should produce different secrets")
	}
}

func TestRelayConfig_ValidScenarios(t *testing.T) {
	for _, s := range []string{"ecommerce", "iot", "saas", "blog", "project"} {
		cfg := &RelayConfig{}
		cfg.setDefaults()
		cfg.FakeData.Scenario = s
		if err := cfg.Validate(); err != nil {
			t.Errorf("scenario %q should be valid: %v", s, err)
		}
	}
}

func TestClientConfig_ValidRelayProtocols(t *testing.T) {
	for _, p := range []string{"postgresql", "mysql", "rest"} {
		cfg := &ClientConfig{}
		cfg.setDefaults()
		cfg.Relays = []RelayEntry{{Address: "host:1234", Protocol: p}}
		if err := cfg.Validate(); err != nil {
			t.Errorf("protocol %q should be valid: %v", p, err)
		}
	}
}

func TestRelayConfig_LoadMissing(t *testing.T) {
	// Loading with no file should use defaults and validate OK
	cfg, err := LoadRelayConfig("")
	if err != nil {
		t.Fatalf("LoadRelayConfig empty: %v", err)
	}
	if cfg.Listen.PostgreSQL != ":5432" {
		t.Errorf("expected default listen addr, got %q", cfg.Listen.PostgreSQL)
	}
}

func TestRelayConfig_LoadInvalidPath(t *testing.T) {
	_, err := LoadRelayConfig("/nonexistent/path/relay.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestRelayConfig_LoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad-relay.yaml")
	if err := os.WriteFile(path, []byte("{{{{not yaml"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadRelayConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestClientConfig_LoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad-client.yaml")
	if err := os.WriteFile(path, []byte("{{{{not yaml"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadClientConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestRelayConfig_Validate_ManualTLS_WithCertAndKey(t *testing.T) {
	cfg := &RelayConfig{}
	cfg.setDefaults()
	cfg.TLS.Mode = "manual"
	cfg.TLS.CertFile = "/path/to/cert.pem"
	cfg.TLS.KeyFile = "/path/to/key.pem"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("manual TLS with cert and key should be valid: %v", err)
	}
}

func TestRelayConfig_Validate_AcmeTLS(t *testing.T) {
	cfg := &RelayConfig{}
	cfg.setDefaults()
	cfg.TLS.Mode = "acme"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("acme TLS should be valid: %v", err)
	}
}

func TestRelayConfig_Validate_ScramSHA256Auth(t *testing.T) {
	cfg := &RelayConfig{}
	cfg.setDefaults()
	cfg.Auth.Method = "scram-sha-256"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("scram-sha-256 auth should be valid: %v", err)
	}
}

func TestRelayConfig_Validate_AllListenAddresses(t *testing.T) {
	cfg := &RelayConfig{}
	cfg.setDefaults()
	cfg.Listen.PostgreSQL = ":5432"
	cfg.Listen.MySQL = ":3306"
	cfg.Listen.REST = ":8080"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("all listen addresses should be valid: %v", err)
	}
}

func TestRelayConfig_Validate_OnlyMySQL(t *testing.T) {
	cfg := &RelayConfig{}
	cfg.setDefaults()
	cfg.Listen.PostgreSQL = ""
	cfg.Listen.MySQL = ":3306"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("MySQL-only listen should be valid: %v", err)
	}
}

func TestRelayConfig_Validate_OnlyREST(t *testing.T) {
	cfg := &RelayConfig{}
	cfg.setDefaults()
	cfg.Listen.PostgreSQL = ""
	cfg.Listen.REST = ":8080"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("REST-only listen should be valid: %v", err)
	}
}

func TestRelayConfig_ValidRoles(t *testing.T) {
	for _, role := range []string{"exit", "relay", "both"} {
		cfg := &RelayConfig{}
		cfg.setDefaults()
		cfg.Tunnel.Role = role
		if err := cfg.Validate(); err != nil {
			t.Errorf("role %q should be valid: %v", role, err)
		}
	}
}

func TestClientConfig_Validate_LargeChunkSize(t *testing.T) {
	cfg := &ClientConfig{}
	cfg.setDefaults()
	cfg.Tunnel.ChunkSize = 100000
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for chunk size too large")
	}
}

func TestClientConfig_ValidScenarios(t *testing.T) {
	for _, s := range []string{"ecommerce", "iot", "saas", "blog", "project"} {
		cfg := &ClientConfig{}
		cfg.setDefaults()
		cfg.Scenario = s
		if err := cfg.Validate(); err != nil {
			t.Errorf("scenario %q should be valid: %v", s, err)
		}
	}
}

func TestClientConfig_ValidCiphers(t *testing.T) {
	for _, c := range []string{"auto", "chacha20", "aes256gcm"} {
		cfg := &ClientConfig{}
		cfg.setDefaults()
		cfg.Tunnel.Cipher = c
		if err := cfg.Validate(); err != nil {
			t.Errorf("cipher %q should be valid: %v", c, err)
		}
	}
}

func TestClientConfig_ValidResponseModes(t *testing.T) {
	for _, m := range []string{"poll", "push"} {
		cfg := &ClientConfig{}
		cfg.setDefaults()
		cfg.Tunnel.ResponseMode = m
		if err := cfg.Validate(); err != nil {
			t.Errorf("response mode %q should be valid: %v", m, err)
		}
	}
}

func TestClientConfig_Validate_RelayWithEmptyProtocol(t *testing.T) {
	cfg := &ClientConfig{}
	cfg.setDefaults()
	cfg.Relays = []RelayEntry{{Address: "host:1234", Protocol: ""}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("relay with empty protocol should be valid: %v", err)
	}
}

func TestRelayConfig_LoadFromFile_WithValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-relay-full.yaml")
	data := []byte(`
listen:
  postgresql: ":5432"
tls:
  mode: manual
  cert_file: /tmp/cert.pem
  key_file: /tmp/key.pem
auth:
  method: scram-sha-256
  users:
    admin: secretpass
tunnel:
  shared_secret: "dGVzdC1zZWNyZXQ="
  max_streams: 500
  role: relay
fake_data:
  scenario: saas
  seed: 123
exit:
  max_conns: 500
  max_idle_secs: 60
log:
  level: debug
  format: text
  output: stderr
`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadRelayConfig(path)
	if err != nil {
		t.Fatalf("LoadRelayConfig: %v", err)
	}
	if cfg.TLS.Mode != "manual" {
		t.Errorf("TLS.Mode = %q, want manual", cfg.TLS.Mode)
	}
	if cfg.Auth.Method != "scram-sha-256" {
		t.Errorf("Auth.Method = %q, want scram-sha-256", cfg.Auth.Method)
	}
	if cfg.Tunnel.Role != "relay" {
		t.Errorf("Tunnel.Role = %q, want relay", cfg.Tunnel.Role)
	}
	if cfg.FakeData.Scenario != "saas" {
		t.Errorf("FakeData.Scenario = %q, want saas", cfg.FakeData.Scenario)
	}
}

func TestRelayConfig_LoadFromFile_InvalidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-relay-bad.yaml")
	data := []byte(`
listen:
  postgresql: ""
  mysql: ""
  rest: ""
`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadRelayConfig(path)
	if err == nil {
		t.Fatal("expected error for config with no listen addresses")
	}
}

func TestClientConfig_LoadFromFile_InvalidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-client-bad.yaml")
	data := []byte(`
proxy:
  listen: ""
`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadClientConfig(path)
	if err == nil {
		t.Fatal("expected error for config with empty listen")
	}
}

func TestClientConfig_LoadDefaultPath(t *testing.T) {
	dir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	// Create duman-client.yaml in temp dir so default path search finds it
	data := []byte(`
proxy:
  listen: "127.0.0.1:2080"
scenario: saas
tunnel:
  chunk_size: 4096
  cipher: chacha20
  response_mode: push
`)
	if err := os.WriteFile(filepath.Join(dir, "duman-client.yaml"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadClientConfig("")
	if err != nil {
		t.Fatalf("LoadClientConfig default path: %v", err)
	}
	if cfg.Proxy.Listen != "127.0.0.1:2080" {
		t.Errorf("Listen = %q, expected 127.0.0.1:2080", cfg.Proxy.Listen)
	}
}

func TestRelayConfig_LoadDefaultPath(t *testing.T) {
	dir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	// Create duman-relay.yaml in temp dir so default path search finds it
	data := []byte(`
listen:
  postgresql: ":15432"
auth:
  method: md5
  users:
    admin: pass
fake_data:
  scenario: blog
`)
	if err := os.WriteFile(filepath.Join(dir, "duman-relay.yaml"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadRelayConfig("")
	if err != nil {
		t.Fatalf("LoadRelayConfig default path: %v", err)
	}
	if cfg.Listen.PostgreSQL != ":15432" {
		t.Errorf("Listen.PostgreSQL = %q, expected :15432", cfg.Listen.PostgreSQL)
	}
	if cfg.FakeData.Scenario != "blog" {
		t.Errorf("FakeData.Scenario = %q, expected blog", cfg.FakeData.Scenario)
	}
}
