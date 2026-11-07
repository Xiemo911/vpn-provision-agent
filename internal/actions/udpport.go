package actions

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Xiemo911/vpn-provision-agent/internal/system"
)

// udpSocketTables list every UDP socket on the VPS, including kernel ones
// (kernel WireGuard's has no process, so ss can't name its owner). A var so
// tests can point it at fixtures.
var udpSocketTables = []string{"/proc/net/udp", "/proc/net/udp6"}

type pickUDPPortParams struct {
	Candidates []int `json:"candidates"`
}

// pickUDPPort returns the first candidate UDP port nothing on the VPS is
// bound to, for a VPN about to be set up here (a protocol switch to
// WireGuard/AmneziaWG). It changes nothing: the VPN's config opens the port
// in the firewall when it comes up.
func (d *Dispatcher) pickUDPPort(_ context.Context, params json.RawMessage) (any, error) {
	var p pickUDPPortParams
	if err := unmarshal(params, &p); err != nil {
		return nil, err
	}
	if len(p.Candidates) == 0 {
		return nil, errors.New("no candidate ports given")
	}
	busy, err := boundUDPPorts(udpSocketTables)
	if err != nil {
		return nil, err
	}
	for _, port := range p.Candidates {
		if port >= 1 && port <= 65535 && !busy[port] {
			return map[string]int{"port": port}, nil
		}
	}
	list := make([]string, len(p.Candidates))
	for i, port := range p.Candidates {
		list[i] = strconv.Itoa(port)
	}
	return nil, fmt.Errorf("%sEvery UDP port the VPN can use (%s) is already in use on your VPS, so the switch was cancelled and your server keeps its current setup. Stop the program using UDP port %d (often another VPN), then try again.",
		system.UserErrorMarker, strings.Join(list, ", "), p.Candidates[0])
}

// boundUDPPorts reads the local ports of every UDP socket from the kernel's
// socket tables. A missing table (no IPv6) is skipped.
func boundUDPPorts(tables []string) (map[int]bool, error) {
	busy := map[int]bool{}
	read := 0
	for _, path := range tables {
		f, err := os.Open(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			// "  0: 00000000:CA6C 00000000:0000 07 ..." (IPv6 addresses
			// are longer); the header line has no hex port and is skipped.
			fields := strings.Fields(sc.Text())
			if len(fields) < 2 {
				continue
			}
			i := strings.LastIndexByte(fields[1], ':')
			if i < 0 {
				continue
			}
			if port, err := strconv.ParseUint(fields[1][i+1:], 16, 16); err == nil && port > 0 {
				busy[int(port)] = true
			}
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		read++
	}
	if read == 0 {
		return nil, errors.New("no UDP socket table to read")
	}
	return busy, nil
}
