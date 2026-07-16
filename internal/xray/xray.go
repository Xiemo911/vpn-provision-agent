// Package xray manages the installation, service state, and VLESS user list of
// an Xray-core instance on the local host.
package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Xiemo911/vpn-provision-agent/internal/system"
)

const defaultConfigPath = "/usr/local/etc/xray/config.json"

// Manager performs Xray operations against a single host.
type Manager struct {
	ConfigPath string
}

// New returns a Manager. An empty configPath falls back to the default.
func New(configPath string) *Manager {
	if configPath == "" {
		configPath = defaultConfigPath
	}
	return &Manager{ConfigPath: configPath}
}

// IsInstalled reports whether the xray binary is present.
func (m *Manager) IsInstalled() bool {
	if _, err := exec.LookPath("xray"); err == nil {
		return true
	}
	_, err := os.Stat("/usr/local/bin/xray")
	return err == nil
}

// IsRunning reports whether the xray systemd service is active.
func (m *Manager) IsRunning(ctx context.Context) bool {
	res, _ := system.Run(ctx, "systemctl", "is-active", "xray")
	return strings.TrimSpace(res.Stdout) == "active"
}

// Version returns the installed Xray version string, or "" if unavailable.
func (m *Manager) Version(ctx context.Context) string {
	res, err := system.Run(ctx, "xray", "version")
	if err != nil {
		return ""
	}
	if line := firstLine(res.Stdout); line != "" {
		return line
	}
	return ""
}

// Install runs the official Xray-install script. An empty version installs the
// latest release.
func (m *Manager) Install(ctx context.Context, version string) (system.Result, error) {
	script := `bash -c "$(curl -L https://github.com/XTLS/Xray-install/raw/main/install-release.sh)" @ install`
	if version != "" {
		script += " --version " + shellQuote(version)
	}
	return system.RunShell(ctx, script)
}

// Uninstall removes Xray via the official installer.
func (m *Manager) Uninstall(ctx context.Context) (system.Result, error) {
	script := `bash -c "$(curl -L https://github.com/XTLS/Xray-install/raw/main/install-release.sh)" @ remove --purge`
	return system.RunShell(ctx, script)
}

// Restart restarts (and enables) the xray service.
func (m *Manager) Restart(ctx context.Context) (system.Result, error) {
	if res, err := system.Run(ctx, "systemctl", "enable", "xray"); err != nil {
		return res, fmt.Errorf("enable xray: %s", res.Combined())
	}
	res, err := system.Run(ctx, "systemctl", "restart", "xray")
	if err != nil {
		return res, fmt.Errorf("restart xray: %s", res.Combined())
	}
	return res, nil
}

// AddUser adds a VLESS client to the target inbound (empty tag = first VLESS
// inbound). If client.ID is empty a new UUID is generated and returned.
func (m *Manager) AddUser(inboundTag string, client Client) (Client, error) {
	if client.ID == "" {
		id, err := NewUUID()
		if err != nil {
			return client, err
		}
		client.ID = id
	}
	err := modifyClients(m.ConfigPath, inboundTag, func(clients []Client) ([]Client, error) {
		for _, c := range clients {
			if c.ID == client.ID {
				return nil, fmt.Errorf("client id %s already exists", client.ID)
			}
			if client.Email != "" && c.Email == client.Email {
				return nil, fmt.Errorf("client email %s already exists", client.Email)
			}
		}
		return append(clients, client), nil
	})
	return client, err
}

// RemoveUser deletes clients matching idOrEmail from the target inbound.
// It returns the number of clients removed.
func (m *Manager) RemoveUser(inboundTag, idOrEmail string) (int, error) {
	removed := 0
	err := modifyClients(m.ConfigPath, inboundTag, func(clients []Client) ([]Client, error) {
		kept := clients[:0:0]
		for _, c := range clients {
			if c.ID == idOrEmail || (c.Email != "" && c.Email == idOrEmail) {
				removed++
				continue
			}
			kept = append(kept, c)
		}
		if removed == 0 {
			return nil, fmt.Errorf("no client matching %q", idOrEmail)
		}
		return kept, nil
	})
	return removed, err
}

// ListUsers returns the VLESS clients of the target inbound.
func (m *Manager) ListUsers(inboundTag string) ([]Client, error) {
	return listClients(m.ConfigPath, inboundTag)
}

// ApplyConfig validates and installs a full replacement Xray config. The
// previous config is backed up and restored if the new one fails validation.
func (m *Manager) ApplyConfig(ctx context.Context, raw json.RawMessage) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return fmt.Errorf("invalid config json: %w", err)
	}

	backup, hadOld := "", false
	if old, err := os.ReadFile(m.ConfigPath); err == nil {
		backup = m.ConfigPath + ".bak"
		hadOld = true
		if err := writeFileAtomic(backup, old); err != nil {
			return fmt.Errorf("backup existing config: %w", err)
		}
	}

	pretty, _ := json.MarshalIndent(probe, "", "  ")
	if err := writeFileAtomic(m.ConfigPath, pretty); err != nil {
		return err
	}

	res, err := system.Run(ctx, "xray", "-test", "-config", m.ConfigPath)
	if err != nil {
		if hadOld {
			if old, rerr := os.ReadFile(backup); rerr == nil {
				_ = writeFileAtomic(m.ConfigPath, old)
			}
		}
		return fmt.Errorf("config failed validation: %s", res.Combined())
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
