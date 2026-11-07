package amneziawg

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// checkParses syntax-checks a script with bash -n. The script goes through
// a file: passed as an argument, Windows' command-line quoting mangles a
// long script full of quotes before Git Bash sees it.
func checkParses(t *testing.T, script string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	path := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", "-n", filepath.ToSlash(path)).CombinedOutput(); err != nil {
		t.Fatalf("script doesn't parse: %v\n%s", err, out)
	}
}

func TestInstallScriptParses(t *testing.T) {
	checkParses(t, installScript())
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
	checkParses(t, uninstallScript)
}

// /usr/local/go may be the VPS owner's own Go: neither script may touch it.
func TestScriptsLeaveUsrLocalGoAlone(t *testing.T) {
	for name, script := range map[string]string{"install": installScript(), "uninstall": uninstallScript} {
		if strings.Contains(script, "/usr/local/go") {
			t.Errorf("%s script touches /usr/local/go", name)
		}
	}
	if !strings.Contains(installScript(), "GO="+goRoot+"/bin/go") {
		t.Errorf("install script doesn't build with the Go in %s", goRoot)
	}
	if !strings.Contains(uninstallScript, "rm -rf "+goRoot) {
		t.Errorf("uninstall script doesn't remove %s", goRoot)
	}
}

func TestInstallUsesThrowawayGoCaches(t *testing.T) {
	script := installScript()
	for _, want := range []string{`GOPATH="$build_dir/gopath"`, `GOCACHE="$build_dir/cache"`, `rm -rf "$build_dir"`, "trap cleanup EXIT"} {
		if !strings.Contains(script, want) {
			t.Errorf("install script lacks %q", want)
		}
	}
}

func TestInstallChecksTheVPSFirst(t *testing.T) {
	script := installScript()
	for _, want := range []string{
		"[ -c /dev/net/tun ] || fail",
		"sysctl -w net.ipv4.ip_forward=",
		"iptables -t nat -L -n >/dev/null 2>&1 ||",
		"need_disk 1200",
		"need_hosts \"AmneziaWG\" go.dev dl.google.com proxy.golang.org github.com",
		`build_flags="-p 1"`,
		"swapon \"$swap_file\"",
		"swapoff \"$swap_file\"",
		"touch " + markerDir + "/.nullsify",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("install script lacks %q", want)
		}
	}
}
