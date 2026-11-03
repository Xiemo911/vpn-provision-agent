package xray

import (
	"os/exec"
	"strings"
	"testing"
)

func TestInstallScriptsParse(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	for name, script := range map[string]string{
		"install":   installScript(""),
		"uninstall": installerScript("remove --purge"),
	} {
		if out, err := exec.Command("bash", "-n", "-c", script).CombinedOutput(); err != nil {
			t.Errorf("%s script doesn't parse: %v\n%s", name, err, out)
		}
	}
}

func TestInstallScriptPinsVersions(t *testing.T) {
	script := installScript("")
	for _, want := range []string{InstallScriptURL, "install --version '" + DefaultVersion + "'"} {
		if !strings.Contains(script, want) {
			t.Errorf("install script lacks %q", want)
		}
	}
	if strings.Contains(script, "/main/") {
		t.Error("install script still fetches the installer from main")
	}
	if got := installScript("v1.2.3"); !strings.Contains(got, "--version 'v1.2.3'") {
		t.Error("an explicit version isn't passed to the installer")
	}
}
