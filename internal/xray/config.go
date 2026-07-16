package xray

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Client is a single VLESS user entry inside an inbound's settings.clients.
type Client struct {
	ID    string `json:"id"`
	Email string `json:"email,omitempty"`
	Flow  string `json:"flow,omitempty"`
	Level int    `json:"level,omitempty"`
}

// NewUUID generates a random RFC 4122 v4 UUID for use as a VLESS client id.
func NewUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// readConfig loads the raw Xray config, preserving all unknown fields.
func readConfig(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read xray config: %w", err)
	}
	cfg := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse xray config: %w", err)
	}
	return cfg, nil
}

// writeConfig writes the config atomically (temp file + rename).
func writeConfig(path string, cfg map[string]json.RawMessage) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// inbounds unmarshals the top-level "inbounds" array as generic objects so we
// can locate and edit one without losing any fields we don't model.
func inbounds(cfg map[string]json.RawMessage) ([]map[string]json.RawMessage, error) {
	raw, ok := cfg["inbounds"]
	if !ok {
		return nil, fmt.Errorf("config has no inbounds")
	}
	var list []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("parse inbounds: %w", err)
	}
	return list, nil
}

func rawString(m map[string]json.RawMessage, key string) string {
	r, ok := m[key]
	if !ok {
		return ""
	}
	var s string
	_ = json.Unmarshal(r, &s)
	return s
}

// findVless returns the index of the target VLESS inbound. If tag is empty the
// first VLESS inbound is used.
func findVless(list []map[string]json.RawMessage, tag string) (int, error) {
	for i, in := range list {
		if rawString(in, "protocol") != "vless" {
			continue
		}
		if tag == "" || rawString(in, "tag") == tag {
			return i, nil
		}
	}
	return -1, fmt.Errorf("no matching vless inbound (tag=%q)", tag)
}

func clientsOf(in map[string]json.RawMessage) ([]Client, map[string]json.RawMessage, error) {
	settings := map[string]json.RawMessage{}
	if r, ok := in["settings"]; ok && len(r) > 0 {
		if err := json.Unmarshal(r, &settings); err != nil {
			return nil, nil, fmt.Errorf("parse inbound settings: %w", err)
		}
	}
	var clients []Client
	if r, ok := settings["clients"]; ok && len(r) > 0 {
		if err := json.Unmarshal(r, &clients); err != nil {
			return nil, nil, fmt.Errorf("parse clients: %w", err)
		}
	}
	return clients, settings, nil
}

// modifyClients loads the config, applies fn to the target inbound's client
// list, and writes the result back. All unmodeled fields are preserved.
func modifyClients(path, tag string, fn func([]Client) ([]Client, error)) error {
	cfg, err := readConfig(path)
	if err != nil {
		return err
	}
	list, err := inbounds(cfg)
	if err != nil {
		return err
	}
	idx, err := findVless(list, tag)
	if err != nil {
		return err
	}

	clients, settings, err := clientsOf(list[idx])
	if err != nil {
		return err
	}
	updated, err := fn(clients)
	if err != nil {
		return err
	}

	clientsRaw, err := json.Marshal(updated)
	if err != nil {
		return err
	}
	settings["clients"] = clientsRaw

	settingsRaw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	list[idx]["settings"] = settingsRaw

	inboundsRaw, err := json.Marshal(list)
	if err != nil {
		return err
	}
	cfg["inbounds"] = inboundsRaw

	return writeConfig(path, cfg)
}

// listClients returns the current clients of the target inbound.
func listClients(path, tag string) ([]Client, error) {
	cfg, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	list, err := inbounds(cfg)
	if err != nil {
		return nil, err
	}
	idx, err := findVless(list, tag)
	if err != nil {
		return nil, err
	}
	clients, _, err := clientsOf(list[idx])
	return clients, err
}
