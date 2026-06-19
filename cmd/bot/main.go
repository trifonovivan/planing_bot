package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"planing_bot/internal/config"
	"planing_bot/internal/logging"
	"planing_bot/internal/metrics"
	"planing_bot/internal/scheduler"
	"planing_bot/internal/service"
	"planing_bot/internal/storage/postgres"
	"planing_bot/internal/telegram"
)

func main() {
	logger := logging.New(os.Stdout)
	cfg, err := config.Load()
	if err != nil {
		logger.Error("config_load_failed", err, nil)
		os.Exit(1)
	}
	location, err := cfg.Location()
	if err != nil {
		logger.Error("config_location_failed", err, nil)
		os.Exit(1)
	}
	digestHour, digestMinute, err := cfg.DigestClock()
	if err != nil {
		logger.Error("config_digest_time_failed", err, nil)
		os.Exit(1)
	}

	db, err := sql.Open("postgres", cfg.DatabaseURL.Value())
	if err != nil {
		logger.Error("database_open_failed", err, nil)
		os.Exit(1)
	}
	defer db.Close()
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := db.PingContext(ctx); err != nil {
		logger.Error("database_ping_failed", err, nil)
		os.Exit(1)
	}

	postgresStore := postgres.New(db)
	var registry *metrics.Registry
	store := service.Store(postgresStore)
	if cfg.MetricsEnabled {
		registry = metrics.NewRegistry()
		registry.SetGaugeCollector(postgresStore.CollectMetrics)
		store = service.NewObservedStore(store, registry)
		metricsServer := metrics.NewServer(cfg.MetricsAddr, registry, logger)
		go metricsServer.Run(ctx)
	}

	planner := service.New(store, cfg.DefaultTimezone, location, service.WithMetrics(registry), service.WithLogger(logger))
	bot := telegram.New(cfg.BotToken.Value(), planner, telegram.WithBotUsername(cfg.BotUsername), telegram.WithMetrics(registry), telegram.WithLogger(logger))
	worker := scheduler.New(planner, bot, digestHour, digestMinute, scheduler.WithMetrics(registry), scheduler.WithLogger(logger))

	go worker.Run(ctx)

	logger.Info("planner_bot_started", logging.Fields{
		"env":             cfg.AppEnv,
		"metrics_enabled": cfg.MetricsEnabled,
		"metrics_addr":    cfg.MetricsAddr,
	})
	if err := bot.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("planner_bot_failed", err, nil)
		os.Exit(1)
	}
}
