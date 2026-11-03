package amneziawg

import (
	"os/exec"
	"strings"
	"testing"
)

func TestInstallScriptParses(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	if out, err := exec.Command("bash", "-n", "-c", installScript()).CombinedOutput(); err != nil {
		t.Fatalf("install script doesn't parse: %v\n%s", err, out)
	}
}

func TestInstallScriptPinsVersions(t *testing.T) {
	script := installScript()
	for _, want := range []string{
		"amneziawg-go@" + amneziaWGGoVersion,
		"--branch " + amneziaWGToolsVersion,
		goVersion + ".linux-",
		"GOTOOLCHAIN=local",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("install script lacks %q", want)
		}
	}
	if strings.Contains(script, "@latest") {
		t.Error("install script still installs @latest")
	}
}
