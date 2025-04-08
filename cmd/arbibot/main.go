package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/raykavin/ArbiTron/internal/arbitrage"
	"github.com/raykavin/ArbiTron/internal/config"
	"github.com/raykavin/ArbiTron/internal/ui"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	if err := run(*configPath); err != nil {
		log.Fatalf("Application error: %v", err)
	}
}

func run(configPath string) error {
	log.Println("Please wait a moment, the application is starting...")

	cfg, err := config.LoadFromFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	setupSignalHandler(cancel)

	dashboard, uiLogger, err := setupUI(ctx, cfg)
	if err != nil {
		return fmt.Errorf("UI initialization failed: %w", err)
	}

	exchanges, err := arbitrage.SetupExchanges(ctx, cfg, uiLogger)
	if err != nil {
		return fmt.Errorf("exchange setup failed: %w", err)
	}

	monitor, err := arbitrage.NewArbitrageMonitor(ctx, cfg, dashboard, uiLogger, exchanges...)
	if err != nil {
		return fmt.Errorf("failed to create monitor: %w", err)
	}

	go monitor.Start(ctx)

	if err := ui.RunDashboard(ctx, dashboard, cfg.UpdateUIInterval); err != nil {
		return fmt.Errorf("dashboard error: %w", err)
	}

	<-ctx.Done()
	return nil
}

func setupUI(ctx context.Context, cfg *config.Config) (*ui.ArbitrageDashboard, ui.Logger, error) {
	dashboard := ui.NewArbitrageDashboard(cfg)
	if err := dashboard.Init(); err != nil {
		return nil, nil, err
	}

	dashboard.StartUpdateListener(ctx)
	uiLogger := ui.NewUILogger(dashboard.GetLoggerWidget())
	return dashboard, uiLogger, nil
}

func setupSignalHandler(cancel context.CancelFunc) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Initiating graceful shutdown...")
		cancel()
	}()
}
