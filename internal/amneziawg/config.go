package amneziawg

import (
	"os"
	"path/filepath"
	"strings"
)

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func appendToFile(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(text)
	return err
}

// removePeerBlock strips the "[Peer]" block containing the given PublicKey
// from an awg0.conf-style INI text, preserving everything else.
func removePeerBlock(path, publicKey string) error {
	data, err := readFile(path)
	if err != nil {
		return err
	}

	type block struct {
		header string
		lines  []string
	}
	var blocks []block
	cur := block{}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			blocks = append(blocks, cur)
			cur = block{header: trimmed}
			continue
		}
		cur.lines = append(cur.lines, line)
	}
	blocks = append(blocks, cur)

	var out []string
	for _, b := range blocks {
		if b.header == "[Peer]" && blockHasPublicKey(b.lines, publicKey) {
			continue
		}
		if b.header != "" {
			out = append(out, b.header)
		}
		out = append(out, b.lines...)
	}

	result := strings.Join(out, "\n")
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return writeFileAtomic(path, []byte(result), 0o600)
}

// postDownCommands returns the PostDown hooks of an awg0.conf-style INI text,
// with %i expanded to the interface name as awg-quick does.
func postDownCommands(conf, iface string) []string {
	var cmds []string
	for _, line := range strings.Split(conf, "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "PostDown") {
			cmds = append(cmds, strings.ReplaceAll(strings.TrimSpace(val), "%i", iface))
		}
	}
	return cmds
}

func blockHasPublicKey(lines []string, publicKey string) bool {
	for _, line := range lines {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(key) == "PublicKey" && strings.TrimSpace(val) == publicKey {
			return true
		}
	}
	return false
}
