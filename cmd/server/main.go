package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/api"
	"codex/platform-demo/internal/seed"
	"codex/platform-demo/internal/service"
	"codex/platform-demo/internal/store"
	"codex/platform-demo/internal/ui"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	seedOnly := flag.Bool("seed-only", false, "seed the configured database and exit")
	resetDemo := flag.Bool("reset-demo", false, "remove demo data, reseed, and exit")
	flag.Parse()
	_ = loadDotEnv(".env")

	ctx := context.Background()
	databasePath := envOr("NEWPLATFORM_DB_PATH", "./data/newplatform.db")
	if databasePath != ":memory:" && !strings.HasPrefix(databasePath, "file:") {
		if err := os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
			return fmt.Errorf("create database directory: %w", err)
		}
	}
	database, err := store.Open(ctx, databasePath)
	if err != nil {
		return err
	}
	defer database.Close()
	if *resetDemo {
		if err := database.Reset(ctx); err != nil {
			return fmt.Errorf("reset demo: %w", err)
		}
	}
	if err := (seed.Seeder{Store: database}).Run(ctx); err != nil {
		return fmt.Errorf("seed demo: %w", err)
	}
	if *seedOnly || *resetDemo {
		log.Printf("demo data ready in %s", databasePath)
		return nil
	}

	allowedRoots := strings.Split(envOr("NEWPLATFORM_ALLOWED_ANSIBLE_ROOTS", "./examples/ansible"), ",")
	allowedRoot := strings.TrimSpace(allowedRoots[0])
	runner, err := ansible.NewRunner(allowedRoot)
	if err != nil {
		return fmt.Errorf("configure Ansible runner: %w", err)
	}
	runner.Binary = envOr("NEWPLATFORM_ANSIBLE_BIN", "ansible-playbook")
	runner.WorkRoot = envOr("NEWPLATFORM_RUN_ROOT", "./data/runs")
	runner.KillGrace = envDuration("NEWPLATFORM_KILL_GRACE", 3*time.Second)
	runner.MaxLogBytes = envInt("NEWPLATFORM_MAX_LOG_BYTES", 2<<20)

	platform := service.NewPlatform(database, runner, service.NewEventHub())
	defer platform.Close()
	if err := platform.Start(ctx); err != nil {
		return err
	}

	address := envOr("NEWPLATFORM_ADDR", "127.0.0.1:8080")
	server := &http.Server{
		Addr: address, Handler: api.NewHandler(platform, ui.Handler()),
		ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 75 * time.Second,
	}
	shutdownContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-shutdownContext.Done()
		grace, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(grace)
	}()

	log.Printf("NewPlatform Demo listening on http://%s", address)
	err = server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(key) == "" {
			continue
		}
		key = strings.TrimSpace(key)
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		_ = os.Setenv(key, value)
	}
	return scanner.Err()
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
