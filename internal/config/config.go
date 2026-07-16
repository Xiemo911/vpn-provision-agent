// Package config loads and validates the agent's runtime configuration.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

// DefaultPath is where the agent looks for its config unless overridden.
const DefaultPath = "/etc/vpn-agent/config.json"

// Config holds everything the agent needs to run.
type Config struct {
	// ServerURL is the base URL of the control server, e.g. https://panel.example.com
	ServerURL string `json:"server_url"`
	// AgentID uniquely identifies this VPS. Defaults to the hostname if empty.
	AgentID string `json:"agent_id"`
	// Token authenticates the agent to the control server (Bearer token).
	Token string `json:"token"`
	// PollIntervalSeconds is how often to poll for actions. Defaults to 60.
	PollIntervalSeconds int `json:"poll_interval_seconds"`
	// XrayConfigPath is the path to the Xray config.json on this host.
	XrayConfigPath string `json:"xray_config_path"`
	// AllowRunCommand enables the run_command action. Off by default.
	AllowRunCommand bool `json:"allow_run_command"`
}

// PollInterval returns the poll interval as a duration.
func (c *Config) PollInterval() time.Duration {
	return time.Duration(c.PollIntervalSeconds) * time.Second
}

// Load reads config from path (may be empty/missing), applies environment
// overrides, fills in defaults, and validates the result.
func Load(path string) (*Config, error) {
	cfg := &Config{}

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("read config %s: %w", path, err)
			}
			// Missing file is fine as long as env vars supply the essentials.
		} else if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
	}

	applyEnvOverrides(cfg)
	applyDefaults(cfg)

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("AGENT_SERVER_URL"); v != "" {
		cfg.ServerURL = v
	}
	if v := os.Getenv("AGENT_ID"); v != "" {
		cfg.AgentID = v
	}
	if v := os.Getenv("AGENT_TOKEN"); v != "" {
		cfg.Token = v
	}
	if v := os.Getenv("AGENT_POLL_INTERVAL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.PollIntervalSeconds = n
		}
	}
	if v := os.Getenv("AGENT_XRAY_CONFIG_PATH"); v != "" {
		cfg.XrayConfigPath = v
	}
	if v := os.Getenv("AGENT_ALLOW_RUN_COMMAND"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.AllowRunCommand = b
		}
	}
}

func applyDefaults(cfg *Config) {
	if cfg.PollIntervalSeconds <= 0 {
		cfg.PollIntervalSeconds = 60
	}
	if cfg.XrayConfigPath == "" {
		cfg.XrayConfigPath = "/usr/local/etc/xray/config.json"
	}
	if cfg.AgentID == "" {
		if h, err := os.Hostname(); err == nil {
			cfg.AgentID = h
		}
	}
}

func (c *Config) validate() error {
	if c.ServerURL == "" {
		return fmt.Errorf("server_url is required (set in config or AGENT_SERVER_URL)")
	}
	if c.Token == "" {
		return fmt.Errorf("token is required (set in config or AGENT_TOKEN)")
	}
	if c.AgentID == "" {
		return fmt.Errorf("agent_id is required and hostname could not be determined")
	}
	return nil
}
