package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// RelayConfig is the top-level relay configuration.
type RelayConfig struct {
	Listen   ListenConfig `yaml:"listen"`
	TLS      TLSConfig    `yaml:"tls"`
	Auth     AuthConfig   `yaml:"auth"`
	Tunnel   RelayTunnelConfig `yaml:"tunnel"`
	FakeData FakeDataConfig `yaml:"fake_data"`
	Exit     ExitConfig   `yaml:"exit"`
	Log      LogConfig    `yaml:"log"`
}

type ListenConfig struct {
	PostgreSQL string `yaml:"postgresql"`
	MySQL      string `yaml:"mysql"`
	REST       string `yaml:"rest"`
}

type TLSConfig struct {
	Mode     string `yaml:"mode"` // acme, manual, self_signed
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	Domain   string `yaml:"domain"`
}

type AuthConfig struct {
	Method   string            `yaml:"method"` // md5, scram-sha-256
	Users    map[string]string `yaml:"users"`  // username → password
}

type RelayTunnelConfig struct {
	SharedSecret string `yaml:"shared_secret"`
	MaxStreams   int    `yaml:"max_streams"`
	ForwardTo    string `yaml:"forward_to"` // for relay-to-relay mode
	Role         string `yaml:"role"`       // exit, relay, both
}

type FakeDataConfig struct {
	Scenario string `yaml:"scenario"`
	Seed     int64  `yaml:"seed"`
	Mutate   bool   `yaml:"mutate"`       // enable template mutations for uniqueness
	CustomDDL string `yaml:"custom_ddl"`  // user-provided DDL (inline SQL or file path)
	Mode     string `yaml:"mode"`         // template, random, custom (default: template)
}

type ExitConfig struct {
	MaxConns    int `yaml:"max_conns"`
	MaxIdleSecs int `yaml:"max_idle_secs"`
}

// LoadRelayConfig loads and validates the relay config from file.
func LoadRelayConfig(path string) (*RelayConfig, error) {
	cfg := &RelayConfig{}
	cfg.setDefaults()

	if path == "" {
		for _, p := range []string{"duman-relay.yaml", "configs/duman-relay.yaml", "/etc/duman/relay.yaml"} {
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

func (c *RelayConfig) setDefaults() {
	c.Listen.PostgreSQL = ":5432"
	c.TLS.Mode = "self_signed"
	c.Auth.Method = "md5"
	c.Auth.Users = map[string]string{"sensor_writer": "duman_default_pass"}
	c.Tunnel.MaxStreams = 1000
	c.Tunnel.Role = "exit"
	c.FakeData.Scenario = "ecommerce"
	c.FakeData.Seed = 42
	c.Exit.MaxConns = 1000
	c.Exit.MaxIdleSecs = 300
	c.Log.Level = "info"
	c.Log.Format = "text"
	c.Log.Output = "stderr"
}

// Validate checks relay config integrity.
func (c *RelayConfig) Validate() error {
	if c.Listen.PostgreSQL == "" && c.Listen.MySQL == "" && c.Listen.REST == "" {
		return errors.New("at least one listen address must be configured")
	}
	validModes := map[string]bool{"acme": true, "manual": true, "self_signed": true}
	if !validModes[c.TLS.Mode] {
		return fmt.Errorf("invalid tls.mode: %s", c.TLS.Mode)
	}
	if c.TLS.Mode == "manual" {
		if c.TLS.CertFile == "" || c.TLS.KeyFile == "" {
			return errors.New("tls.cert_file and tls.key_file required for manual mode")
		}
	}
	validAuth := map[string]bool{"md5": true, "scram-sha-256": true}
	if !validAuth[c.Auth.Method] {
		return fmt.Errorf("invalid auth.method: %s", c.Auth.Method)
	}
	if len(c.Auth.Users) == 0 {
		return errors.New("at least one auth.users entry required")
	}
	// Validate fake_data mode
	mode := c.FakeData.Mode
	if mode == "" {
		mode = "template"
	}
	validFDModes := map[string]bool{"template": true, "random": true, "custom": true}
	if !validFDModes[mode] {
		return fmt.Errorf("invalid fake_data.mode: %s", mode)
	}
	if mode == "custom" && c.FakeData.CustomDDL == "" {
		return errors.New("fake_data.custom_ddl is required when mode is 'custom'")
	}
	if mode == "template" {
		validScenarios := map[string]bool{"ecommerce": true, "iot": true, "saas": true, "blog": true, "project": true}
		if !validScenarios[c.FakeData.Scenario] {
			return fmt.Errorf("invalid fake_data.scenario: %s", c.FakeData.Scenario)
		}
	}
	validRoles := map[string]bool{"exit": true, "relay": true, "both": true}
	if !validRoles[c.Tunnel.Role] {
		return fmt.Errorf("invalid tunnel.role: %s", c.Tunnel.Role)
	}
	return nil
}
