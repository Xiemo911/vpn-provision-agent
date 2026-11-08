package system

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// IsOpenRC reports whether the host's init system is OpenRC (Alpine)
// rather than systemd. The control server's bootstrap refuses hosts that
// run neither, so anything that isn't OpenRC is treated as systemd.
func IsOpenRC() bool {
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		return false
	}
	_, err := os.Stat("/sbin/openrc-run")
	return err == nil
}

// Service is a system service known by a name under each init system
// (systemd's awg-quick@awg0 is an OpenRC script named awg-quick, say).
type Service struct {
	Systemd string
	OpenRC  string
}

// Name returns the service's name under the host's init system.
func (s Service) Name() string {
	if IsOpenRC() {
		return s.OpenRC
	}
	return s.Systemd
}

// IsActive reports whether the service is running.
func (s Service) IsActive(ctx context.Context) bool {
	if IsOpenRC() {
		// Exit status 0 means started; stopped and crashed are non-zero.
		_, err := Run(ctx, "rc-service", s.OpenRC, "status")
		return err == nil
	}
	res, _ := Run(ctx, "systemctl", "is-active", s.Systemd)
	return strings.TrimSpace(res.Stdout) == "active"
}

// Recover restarts a service whose stop step fails, as awg-quick's does once
// its interface is gone. systemd starts it again regardless; OpenRC won't
// start a service it couldn't stop, so its state is reset (zapped) first.
// Callers should check the service came back rather than trust the result.
func (s Service) Recover(ctx context.Context) (Result, error) {
	if IsOpenRC() {
		_, _ = Run(ctx, "rc-service", s.OpenRC, "zap")
		return Run(ctx, "rc-service", s.OpenRC, "start")
	}
	return Run(ctx, "systemctl", "restart", s.Systemd)
}

// EnableAndRestart makes the service start at boot and (re)starts it now.
func (s Service) EnableAndRestart(ctx context.Context) (Result, error) {
	name := s.Name()
	restart := []string{"systemctl", "restart", name}
	if IsOpenRC() {
		// rc-update's exit status for a service already in the runlevel
		// differs between OpenRC versions, so it isn't treated as a failure:
		// a missing init script fails the restart below anyway.
		_, _ = Run(ctx, "rc-update", "add", name, "default")
		restart = []string{"rc-service", name, "restart"}
	} else if res, err := Run(ctx, "systemctl", "enable", name); err != nil {
		return res, fmt.Errorf("enable %s: %s", name, res.Combined())
	}
	res, err := Run(ctx, restart[0], restart[1:]...)
	if err != nil {
		return res, fmt.Errorf("restart %s: %s", name, res.Combined())
	}
	return res, nil
}
