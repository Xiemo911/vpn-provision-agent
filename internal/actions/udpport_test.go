package actions

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xiemo911/vpn-provision-agent/internal/system"
)

func udpTables(t *testing.T, udp, udp6 string) []string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	return []string{write("udp", udp), write("udp6", udp6), filepath.Join(dir, "missing")}
}

const udpHeader = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops\n"

func pick(t *testing.T, tables []string, candidates ...int) (int, error) {
	t.Helper()
	old := udpSocketTables
	udpSocketTables = tables
	t.Cleanup(func() { udpSocketTables = old })
	params, _ := json.Marshal(pickUDPPortParams{Candidates: candidates})
	out, err := (&Dispatcher{}).pickUDPPort(context.Background(), params)
	if err != nil {
		return 0, err
	}
	return out.(map[string]int)["port"], nil
}

func TestPickUDPPortSkipsBoundPorts(t *testing.T) {
	// 51820 (0xCA6C) is bound over IPv4 (kernel WireGuard), 51821 (0xCA6D)
	// over IPv6 only; 53 (0x0035) is unrelated.
	tables := udpTables(t,
		udpHeader+"  12: 00000000:CA6C 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 0 2 0000000000000000 0\n"+
			" 344: 3500007F:0035 00000000:0000 07 00000000:00000000 00:00000000 00000000   101        0 1 2 0000000000000000 0\n",
		udpHeader+"  13: 00000000000000000000000000000000:CA6D 00000000000000000000000000000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 0 2 0000000000000000 0\n")
	port, err := pick(t, tables, 51820, 51821, 51822)
	if err != nil || port != 51822 {
		t.Fatalf("got %d, %v; want 51822", port, err)
	}
	if port, err := pick(t, tables, 51820, 53, 41820); err != nil || port != 41820 {
		t.Fatalf("got %d, %v; want 41820", port, err)
	}
}

func TestPickUDPPortFailsForTheUserWhenAllAreBound(t *testing.T) {
	tables := udpTables(t, udpHeader+"  12: 00000000:CA6C 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 0 2 0000000000000000 0\n", udpHeader)
	_, err := pick(t, tables, 51820)
	if err == nil || !strings.HasPrefix(err.Error(), system.UserErrorMarker) {
		t.Fatalf("err = %v; want a message for the user", err)
	}
	if _, err := pick(t, []string{filepath.Join(t.TempDir(), "none")}, 51820); err == nil {
		t.Error("picked a port without any socket table to check")
	}
}

// On Linux, a socket this test binds shows up in the real socket tables.
func TestBoundUDPPortsSeesARealSocket(t *testing.T) {
	if _, err := os.Stat(udpSocketTables[0]); err != nil {
		t.Skip("no /proc/net/udp here")
	}
	for _, network := range []string{"udp4", "udp6"} {
		conn, err := net.ListenPacket(network, ":0")
		if err != nil {
			t.Logf("%s: %v", network, err)
			continue
		}
		port := conn.LocalAddr().(*net.UDPAddr).Port
		busy, err := boundUDPPorts(udpSocketTables)
		conn.Close()
		if err != nil || !busy[port] {
			t.Errorf("%s port %d not seen as bound (err %v)", network, port, err)
		}
	}
}
