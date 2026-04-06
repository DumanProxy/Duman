package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ClientConfig is the top-level client configuration.
type ClientConfig struct {
	Proxy    ProxyConfig    `yaml:"proxy"`
	Tunnel   TunnelConfig   `yaml:"tunnel"`
	Relays   []RelayEntry   `yaml:"relays"`
	Scenario  string         `yaml:"scenario"`
	SchemaCfg SchemaConfig   `yaml:"schema"`
	Noise    NoiseConfig    `yaml:"noise"`
	Log      LogConfig      `yaml:"log"`
	Pool     PoolConfig     `yaml:"pool"`
	Routing  RoutingConfig  `yaml:"routing"`
}

type ProxyConfig struct {
	Listen    string `yaml:"listen"`
	Mode      string `yaml:"mode"` // socks5, tun
	KillSwitch bool  `yaml:"kill_switch"`
}

type TunnelConfig struct {
	SharedSecret string `yaml:"shared_secret"`
	ChunkSize    int    `yaml:"chunk_size"`
	ResponseMode string `yaml:"response_mode"` // push, poll
	Cipher       string `yaml:"cipher"`        // auto, chacha20, aes256gcm
	PFS          bool   `yaml:"pfs"`
	Padding      bool   `yaml:"padding"`
	JitterMs     int    `yaml:"jitter_ms"`
}

type RelayEntry struct {
	Address  string `yaml:"address"`
	Protocol string `yaml:"protocol"` // postgresql, mysql, rest
	Weight   int    `yaml:"weight"`
	Database string `yaml:"database"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	TLSPin   string `yaml:"tls_pin"`
}

type NoiseConfig struct {
	PhantomBrowser PhantomBrowserConfig `yaml:"phantom_browser"`
	SmokeScreen    SmokeScreenConfig    `yaml:"smoke_screen"`
	Decoy          DecoyConfig          `yaml:"decoy"`
}

type PhantomBrowserConfig struct {
	Enabled bool   `yaml:"enabled"`
	Region  string `yaml:"region"`
}

type SmokeScreenConfig struct {
	Enabled   bool     `yaml:"enabled"`
	PeerCount int      `yaml:"peer_count"`
	Profiles  []string `yaml:"profiles"`
}

type DecoyConfig struct {
	Enabled bool     `yaml:"enabled"`
	Targets []string `yaml:"targets"`
	Count   int      `yaml:"count"`
}

type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	Output string `yaml:"output"`
}

type PoolConfig struct {
	MaxActive          int `yaml:"max_active"`
	HealthCheckSeconds int `yaml:"health_check_seconds"`
}

type RoutingConfig struct {
	Mode  string        `yaml:"mode"` // socks5, tun, process
	Rules []RoutingRule `yaml:"rules"`
}

type RoutingRule struct {
	Match  string `yaml:"match"`
	Action string `yaml:"action"` // tunnel, direct, block
}

// SchemaConfig controls how the client generates cover query schemas.
type SchemaConfig struct {
	Mode      string `yaml:"mode"`       // template, random, custom (default: template)
	Mutate    bool   `yaml:"mutate"`     // enable template mutations for uniqueness
	CustomDDL string `yaml:"custom_ddl"` // user-provided DDL for custom mode
	Seed      int64  `yaml:"seed"`       // seed for deterministic generation
}

// LoadClientConfig loads and validates the client config from file.
func LoadClientConfig(path string) (*ClientConfig, error) {
	cfg := &ClientConfig{}
	cfg.setDefaults()

	if path == "" {
		// Try default locations
		for _, p := range []string{"duman-client.yaml", "configs/duman-client.yaml", "/etc/duman/client.yaml"} {
			if _, err := os.Stat(p); err == nil {
				path = p
				break
			}
		}
	}

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *ClientConfig) setDefaults() {
	c.Proxy.Listen = "127.0.0.1:1080"
	c.Proxy.Mode = "socks5"
	c.Tunnel.ChunkSize = 16384
	c.Tunnel.ResponseMode = "poll"
	c.Tunnel.Cipher = "auto"
	c.Scenario = "ecommerce"
	c.Log.Level = "info"
	c.Log.Format = "text"
	c.Log.Output = "stderr"
	c.Pool.MaxActive = 3
	c.Pool.HealthCheckSeconds = 30
	c.Routing.Mode = "socks5"
}

// Validate checks config integrity.
func (c *ClientConfig) Validate() error {
	if c.Proxy.Listen == "" {
		return errors.New("proxy.listen is required")
	}
	if c.Tunnel.ChunkSize < 1024 || c.Tunnel.ChunkSize > 65536 {
		return errors.New("tunnel.chunk_size must be between 1024 and 65536")
	}
	schemaMode := c.SchemaCfg.Mode
	if schemaMode == "" {
		schemaMode = "template"
	}
	validSchemaModes := map[string]bool{"template": true, "random": true, "custom": true}
	if !validSchemaModes[schemaMode] {
		return fmt.Errorf("invalid schema.mode: %s", schemaMode)
	}
	if schemaMode == "custom" && c.SchemaCfg.CustomDDL == "" {
		return errors.New("schema.custom_ddl is required when mode is 'custom'")
	}
	if schemaMode == "template" {
		validScenarios := map[string]bool{"ecommerce": true, "iot": true, "saas": true, "blog": true, "project": true}
		if !validScenarios[c.Scenario] {
			return fmt.Errorf("invalid scenario: %s (must be one of: ecommerce, iot, saas, blog, project)", c.Scenario)
		}
	}
	validCiphers := map[string]bool{"auto": true, "chacha20": true, "aes256gcm": true}
	if !validCiphers[c.Tunnel.Cipher] {
		return fmt.Errorf("invalid cipher: %s", c.Tunnel.Cipher)
	}
	validModes := map[string]bool{"poll": true, "push": true}
	if !validModes[c.Tunnel.ResponseMode] {
		return fmt.Errorf("invalid response_mode: %s", c.Tunnel.ResponseMode)
	}
	for i, r := range c.Relays {
		if r.Address == "" {
			return fmt.Errorf("relay[%d].address is required", i)
		}
		validProtos := map[string]bool{"postgresql": true, "mysql": true, "rest": true}
		if r.Protocol != "" && !validProtos[r.Protocol] {
			return fmt.Errorf("relay[%d].protocol invalid: %s", i, r.Protocol)
		}
	}
	return nil
}

// GenerateSharedSecret generates a cryptographically random 32-byte secret as base64.
func GenerateSharedSecret() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}
