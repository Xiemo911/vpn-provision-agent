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
		"install":          installScript("", false),
		"openrc install":   installScript("", true),
		"uninstall":        installerScript("remove --purge"),
		"openrc uninstall": OpenRCUninstallScript,
	} {
		if out, err := exec.Command("bash", "-n", "-c", script).CombinedOutput(); err != nil {
			t.Errorf("%s script doesn't parse: %v\n%s", name, err, out)
		}
	}
}

func TestInstallScriptPinsVersions(t *testing.T) {
	script := installScript("", false)
	for _, want := range []string{InstallScriptURL, "install --version '" + DefaultVersion + "'"} {
		if !strings.Contains(script, want) {
			t.Errorf("install script lacks %q", want)
		}
	}
	if strings.Contains(script, "/main/") {
		t.Error("install script still fetches the installer from main")
	}
	if got := installScript("v1.2.3", false); !strings.Contains(got, "--version 'v1.2.3'") {
		t.Error("an explicit version isn't passed to the installer")
	}
	if got := installScript("v1.2.3", true); !strings.Contains(got, `download/"'v1.2.3'"/$asset`) {
		t.Error("an explicit version isn't downloaded on OpenRC")
	}
}

func TestInstallScriptChecksAndMarks(t *testing.T) {
	for _, openrc := range []bool{false, true} {
		script := installScript("", openrc)
		for _, want := range []string{"need_disk 300", `need_hosts "Xray" raw.githubusercontent.com github.com`, "touch " + markerDir + "/.nullsify"} {
			if !strings.Contains(script, want) {
				t.Errorf("openrc=%v: install script lacks %q", openrc, want)
			}
		}
		if strings.Index(script, `need_hosts "Xray"`) < strings.Index(script, "pkg_install $missing") {
			t.Errorf("openrc=%v: reachability is checked before curl is installed", openrc)
		}
	}
}

func TestOpenRCInstallChecksDownloadAndWritesService(t *testing.T) {
	script := installScript("", true)
	for _, want := range []string{
		"Xray-linux-64.zip", "Xray-linux-arm64-v8a.zip",
		`"$url.dgst"`, "sha256sum", `"$want" != "$got"`,
		"/etc/init.d/xray", "#!/sbin/openrc-run", "supervisor=supervise-daemon",
		`command_user="nobody:nobody"`, "cap_net_bind_service",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("OpenRC install script lacks %q", want)
		}
	}
	if strings.Contains(script, InstallScriptURL) {
		t.Error("OpenRC install runs the systemd-only official installer")
	}
}
