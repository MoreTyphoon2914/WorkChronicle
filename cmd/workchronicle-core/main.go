package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"worktracker/internal/core"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "generate-token" {
		if err := generateToken(os.Args[2]); err != nil {
			slog.Error("generate agent token", "error", err)
			os.Exit(2)
		}
		fmt.Println("Created Core agent token file:", os.Args[2])
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck())
	}
	config, err := core.ConfigFromEnv()
	if err != nil {
		slog.Error("invalid core configuration", "error", err)
		os.Exit(2)
	}
	store, err := core.OpenStore(config.DataDir, config.Retention)
	if err != nil {
		slog.Error("open core persistence", "error", err)
		os.Exit(2)
	}
	server, err := core.NewServer(config, store)
	if err != nil {
		slog.Error("create core server", "error", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.HTTPServer().ListenAndServe() }()
	slog.Info("WorkChronicle Core listening", "address", config.ListenAddress, "data_dir", config.DataDir)
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("core server stopped", "error", err)
			os.Exit(2)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.HTTPServer().Shutdown(shutdownCtx); err != nil {
			slog.Error("core shutdown failed", "error", err)
			os.Exit(2)
		}
	}
}

func generateToken(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(hex.EncodeToString(raw))
	return err
}

func runHealthcheck() int {
	config, err := core.ConfigFromEnv()
	if err != nil {
		return 1
	}
	_, port, err := net.SplitHostPort(config.ListenAddress)
	if err != nil {
		return 1
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get("http://127.0.0.1:" + port + "/health")
	if err != nil {
		return 1
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
