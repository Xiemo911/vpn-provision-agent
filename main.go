package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Xiemo911/vpn-provision-agent/internal/agent"
	"github.com/Xiemo911/vpn-provision-agent/internal/config"
)

// version is overridable at build time:
//
//	go build -ldflags "-X main.version=1.0.0"
var version = "dev"

func main() {
	configPath := flag.String("config", config.DefaultPath, "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		log.Printf("vpn-provision-agent %s", version)
		return
	}

	log.SetFlags(log.LstdFlags | log.LUTC)

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config errorr: %v", err)
	}

	hostname, _ := os.Hostname()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a := agent.New(cfg, version, hostname)
	if err := a.Run(ctx); err != nil {
		log.Fatalf("agent error: %v", err)
	}
}
