package main

import (
	"bufio"
	"context"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"northstar/backend/internal/bootstrap"
	"northstar/backend/internal/domain"
	"northstar/backend/internal/httpapi"
	"northstar/backend/internal/seed"
	"northstar/backend/internal/store"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func loadEnv() {
	f, err := os.Open(".env")
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if ok {
			if _, exists := os.LookupEnv(key); !exists {
				_ = os.Setenv(key, strings.Trim(strings.TrimSpace(value), "\"'"))
			}
		}
	}
}
func main() {
	loadEnv()
	demo := flag.Bool("demo", false, "run an ephemeral seeded demo without PostgreSQL")
	seedFixtures := flag.Bool("seed-fixtures", false, "add missing payslip/environment fixtures to an existing demo database and exit")
	flag.Parse()
	if env("APP_ENV", "development") == "production" && (*demo || env("SEED_DEMO", "false") == "true") {
		slog.Error("production cannot use demo mode or demo fixtures")
		os.Exit(1)
	}
	if *seedFixtures && (*demo || env("SEED_DEMO", "false") != "true") {
		slog.Error("-seed-fixtures requires PostgreSQL and SEED_DEMO=true")
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var repo store.Store
	if *demo {
		state, err := seed.Demo()
		if err != nil {
			slog.Error("seed failed", "error", err)
			os.Exit(1)
		}
		repo = store.NewMemory(state)
		slog.Warn("DEMO MODE: ephemeral data and public demo passwords; never expose to the internet")
	} else {
		url := os.Getenv("DATABASE_URL")
		if url == "" {
			slog.Error("DATABASE_URL is required; use -demo for a local ephemeral preview")
			os.Exit(1)
		}
		connectCtx, connectCancel := context.WithTimeout(ctx, 15*time.Second)
		pg, err := store.NewPostgres(connectCtx, url)
		connectCancel()
		if err != nil {
			slog.Error("PostgreSQL unavailable", "error", err)
			os.Exit(1)
		}
		repo = pg
		err = repo.Update(ctx, func(s *domain.State) error {
			if len(s.Employees) > 0 {
				return nil
			}
			if env("SEED_DEMO", "false") == "true" {
				seeded, err := seed.Demo()
				if err != nil {
					return err
				}
				*s = seeded
				return nil
			}
			return bootstrap.Owner(s, os.Getenv("ADMIN_EMAIL"), os.Getenv("ADMIN_PASSWORD"), env("ADMIN_NAME", "Superadmin"), env("COMPANY_NAME", "Nexilar"), env("COMPANY_SLUG", "northstar"))
		})
		if err != nil {
			repo.Close()
			slog.Error("initialization failed", "error", err)
			os.Exit(1)
		}
	}
	defer repo.Close()
	if *seedFixtures {
		if err := repo.Update(ctx, seed.MergeFixtures); err != nil {
			slog.Error("fixture upgrade failed", "error", err)
			os.Exit(1)
		}
	}
	if *demo || env("SEED_DEMO", "false") == "true" {
		state, err := repo.Read(ctx)
		if err == nil {
			err = seed.InstallFiles(env("UPLOAD_DIR", "./uploads"), state)
		}
		if err != nil {
			slog.Error("sample payslip installation failed", "error", err)
			os.Exit(1)
		}
	}
	if *seedFixtures {
		slog.Info("demo fixtures upgraded; existing data preserved")
		return
	}
	origins := strings.Split(env("ALLOWED_ORIGINS", "http://localhost:3000"), ",")
	for i := range origins {
		origins[i] = strings.TrimSpace(origins[i])
	}
	config, err := httpapi.Configuration(ctx, origins, env("UPLOAD_DIR", "./uploads"))
	if err != nil {
		slog.Error("service configuration failed", "error", err)
		os.Exit(1)
	}
	if *demo && config.Production {
		slog.Error("production cannot run in demo mode")
		os.Exit(1)
	}
	service, api := httpapi.NewService(repo, config)
	go service.RunWorkers(ctx)
	server := &http.Server{Addr: ":" + env("PORT", "8080"), Handler: api, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1024 * 1024}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		repo.Close()
		slog.Error("cannot bind API port", "error", err)
		os.Exit(1)
	}
	slog.Info("Northstar API ready", "address", server.Addr)
	serverError := make(chan error, 1)
	go func() { serverError <- server.Serve(listener) }()
	select {
	case err := <-serverError:
		if err != nil && err != http.ErrServerClosed {
			repo.Close()
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
	}
	shutdownCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	_ = server.Shutdown(shutdownCtx)
}
