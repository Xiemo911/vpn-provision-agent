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

func TestSelfUninstallScriptsParse(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	for name, script := range map[string]string{"systemd": selfUninstallScript, "openrc": openRCSelfUninstallScript} {
		if out, err := exec.Command("bash", "-n", "-c", script).CombinedOutput(); err != nil {
			t.Errorf("%s self-uninstall script doesn't parse: %v\n%s", name, err, out)
		}
	}
	if strings.Index(openRCSelfUninstallScript, "rc-service vpn-agent stop") < strings.Index(openRCSelfUninstallScript, "rm -rf /etc/vpn-agent") {
		t.Error("OpenRC self-uninstall stops the agent before cleaning up everything else")
	}
}
