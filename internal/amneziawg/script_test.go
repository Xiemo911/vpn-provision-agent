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

func TestUninstallScriptParses(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	if out, err := exec.Command("bash", "-n", "-c", uninstallScript).CombinedOutput(); err != nil {
		t.Fatalf("uninstall script doesn't parse: %v\n%s", err, out)
	}
}

// /usr/local/go may be the VPS owner's own Go; only the pinned version the
// install put there may be removed.
func TestUninstallRemovesOnlyOurGo(t *testing.T) {
	guard := `if [ "$(/usr/local/go/bin/go env GOVERSION 2>/dev/null)" = "` + goVersion + `" ]; then`
	i, j := strings.Index(uninstallScript, guard), strings.Index(uninstallScript, "rm -rf /usr/local/go")
	if i < 0 || j < i {
		t.Fatalf("uninstall script doesn't remove /usr/local/go behind the %s version check:\n%s", goVersion, uninstallScript)
	}
}

func TestInstallUsesThrowawayGoCaches(t *testing.T) {
	script := installScript()
	for _, want := range []string{`GOPATH="$build_dir/gopath"`, `GOCACHE="$build_dir/cache"`, `trap 'rm -rf "$build_dir"' EXIT`} {
		if !strings.Contains(script, want) {
			t.Errorf("install script lacks %q", want)
		}
	}
}
