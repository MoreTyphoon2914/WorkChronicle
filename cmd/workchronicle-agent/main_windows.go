//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"worktracker/internal/config"
	"worktracker/internal/coreclient"
	"worktracker/internal/hostagent"
	wtlog "worktracker/internal/logging"
	wtwindows "worktracker/internal/windows"
)

func main() {
	configPath := flag.String("config", "config.json", "host acquisition configuration path")
	coreURL := flag.String("core-url", "http://127.0.0.1:8080", "WorkChronicle Core URL")
	tokenFile := flag.String("token-file", "secrets/agent-token.txt", "Core agent token file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fatal("configuration", err)
	}
	tokenBytes, err := os.ReadFile(*tokenFile)
	if err != nil {
		fatal("read Core token file", err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if len(token) < 16 {
		fatal("Core token", fmt.Errorf("token must contain at least 16 characters"))
	}

	mutex, err := wtwindows.AcquireMutex(cfg.ConfigPath)
	if err != nil {
		fatal("single-instance protection", err)
	}
	defer mutex.Close()
	logger, closer, err := wtlog.New(cfg.Logging, os.Stderr)
	if err != nil {
		fatal("logging", err)
	}
	defer closer.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	client := coreclient.New(*coreURL, token, cfg.HTTPTimeout())
	agent := hostagent.New(cfg, client, logger)
	fmt.Printf("WorkChronicle Host Agent running in %s acquisition mode; observations are forwarded to %s\n", cfg.AcquisitionMode(), *coreURL)
	if err := agent.Run(ctx); err != nil && err != context.Canceled {
		fatal("host agent stopped", err)
	}
}

func fatal(scope string, err error) {
	fmt.Fprintln(os.Stderr, scope+":", err)
	os.Exit(2)
}
