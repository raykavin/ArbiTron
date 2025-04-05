package main

import (
	"context"
	"flag"
	"log"
	"notlelouch/ArbiBot/internal/arbitrage"
	"notlelouch/ArbiBot/internal/config"
	"notlelouch/ArbiBot/internal/ui"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// Parse command line flags
	configPath := flag.String("config", "config.json", "Path to configuration file")
	flag.Parse()

	// Load configuration
	cfg, err := config.LoadFromFile(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create a context that can be canceled
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	setupSignalHandler(cancel)

	// Create and initialize the arbitrage dashboard
	dashboard := ui.NewArbitrageDashboard(cfg.Coins)
	if err := dashboard.Init(); err != nil {
		log.Fatalf("Failed to initialize widgets: %v", err)
	}

	// Start update listener for the dashboard
	dashboard.StartUpdateListener(ctx)

	// Create and start the arbitrage monitor
	monitor, err := arbitrage.NewArbitrageMonitor(dashboard, cfg)
	if err != nil {
		log.Fatalf("Failed to create arbitrage monitor: %v", err)
	}

	if err := monitor.Connect(ctx); err != nil {
		log.Fatalf("Failed to connect to exchanges: %v", err)
	}

	// Run arbitrage monitoring in parallel with terminal dashboard
	go monitor.Start(ctx)

	// Run the terminal dashboard
	if err := ui.RunDashboard(ctx, dashboard); err != nil {
		log.Fatalf("Failed to run dashboard: %v", err)
	}

	// Wait for program to exit
	<-ctx.Done()
}

// setupSignalHandler configures system signal handling for graceful shutdown
func setupSignalHandler(cancel context.CancelFunc) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Shutdownting...")
		cancel()
	}()
}
