package amneziawg

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPostDownCommands(t *testing.T) {
	conf := `[Interface]
PrivateKey = abc=
ListenPort = 51820
PostUp = iptables -A FORWARD -i %i -j ACCEPT
PostDown = iptables -D INPUT -p udp --dport 51820 -j ACCEPT; iptables -D FORWARD -i %i -j ACCEPT
postdown=iptables -D FORWARD -o %i -j ACCEPT

[Peer]
PublicKey = xyz=
AllowedIPs = 10.29.29.2/32
`
	got := postDownCommands(conf, "awg0")
	want := []string{
		"iptables -D INPUT -p udp --dport 51820 -j ACCEPT; iptables -D FORWARD -i awg0 -j ACCEPT",
		"iptables -D FORWARD -o awg0 -j ACCEPT",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("postDownCommands = %q, want %q", got, want)
	}
	if got := postDownCommands("[Interface]\nListenPort = 51820\n", "awg0"); len(got) != 0 {
		t.Errorf("config without PostDown gave %q", got)
	}
}

func TestInterfaceUp(t *testing.T) {
	orig := sysClassNet
	t.Cleanup(func() { sysClassNet = orig })
	sysClassNet = t.TempDir()

	if interfaceUp() {
		t.Fatal("interfaceUp with no awg0 entry")
	}
	if err := os.Mkdir(filepath.Join(sysClassNet, ifaceName), 0o755); err != nil {
		t.Fatal(err)
	}
	if !interfaceUp() {
		t.Fatal("interfaceUp missed the awg0 entry")
	}
}
