package actions

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSelfUninstallScript(t *testing.T) {
	for _, want := range []string{
		"systemctl disable --now nullsify-open-ports.path nullsify-open-ports.service",
		"/etc/systemd/system/nullsify-open-ports.path",
		"/etc/systemd/system/nullsify-open-ports.service",
		"/usr/local/sbin/nullsify-open-ports",
		"rm -f /usr/local/bin/vpn-agent",
	} {
		if !strings.Contains(selfUninstallScript, want) {
			t.Errorf("self-uninstall script lacks %q", want)
		}
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	if out, err := exec.Command("bash", "-n", "-c", selfUninstallScript).CombinedOutput(); err != nil {
		t.Fatalf("self-uninstall script doesn't parse: %v\n%s", err, out)
	}
}
