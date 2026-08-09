package actions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// maxAgentBinarySize bounds how much update_agent will write to disk, so a
// misbehaving or compromised download URL can't fill the disk.
const maxAgentBinarySize = 200 << 20 // 200MB

type updateAgentParams struct {
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Version string `json:"version"`
}

// updateAgent downloads a new agent binary from a URL supplied by the control
// server, verifies its checksum, and swaps it in for the currently running
// executable. It does not restart the process itself: after a short delay
// (long enough for this action's result to make it back to the control
// server) it exits, and vpn-agent.service's Restart=always brings the agent
// back up running the new binary.
func (d *Dispatcher) updateAgent(ctx context.Context, params json.RawMessage) (any, error) {
	var p updateAgentParams
	if err := unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.URL == "" {
		return nil, fmt.Errorf("url is required")
	}
	if p.SHA256 == "" {
		return nil, fmt.Errorf("sha256 is required")
	}

	binPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve current executable: %w", err)
	}

	tmpPath := binPath + ".update"
	defer os.Remove(tmpPath)

	sum, err := downloadAndHash(ctx, p.URL, tmpPath)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	if !strings.EqualFold(sum, p.SHA256) {
		return nil, fmt.Errorf("checksum mismatch: got %s, want %s", sum, p.SHA256)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return nil, fmt.Errorf("chmod new binary: %w", err)
	}

	// Renaming the running executable's path is safe on Linux: this process
	// keeps its already-open inode regardless of what the path now points to.
	backupPath := binPath + ".bak"
	if err := os.Rename(binPath, backupPath); err != nil {
		return nil, fmt.Errorf("back up current binary: %w", err)
	}
	if err := os.Rename(tmpPath, binPath); err != nil {
		_ = os.Rename(backupPath, binPath) // best-effort restore so the next restart still works
		return nil, fmt.Errorf("install new binary: %w", err)
	}

	go func() {
		time.Sleep(2 * time.Second)
		os.Exit(0)
	}()

	return map[string]string{"status": "update installed, restarting", "version": p.Version}, nil
}

// downloadAndHash streams url to destPath and returns the hex sha256 of what
// was written.
func downloadAndHash(ctx context.Context, url, destPath string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("server returned %d", resp.StatusCode)
	}

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	defer out.Close()

	hasher := sha256.New()
	limited := io.LimitReader(resp.Body, maxAgentBinarySize+1)
	written, err := io.Copy(out, io.TeeReader(limited, hasher))
	if err != nil {
		return "", err
	}
	if written > maxAgentBinarySize {
		return "", fmt.Errorf("binary exceeds %d byte limit", maxAgentBinarySize)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
