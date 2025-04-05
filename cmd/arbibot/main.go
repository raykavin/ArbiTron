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
	"github.com/raykavin/ArbiTron/internal/exchange"
	"github.com/raykavin/ArbiTron/internal/exchange/hyperliquid"
	"github.com/raykavin/ArbiTron/internal/exchange/kucoin"
	"github.com/raykavin/ArbiTron/internal/ui"
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

	// Setup exchanges for monitoring
	exchanges, err := setupExchanges(cfg)
	if err != nil {
		log.Fatalf(err.Error())
	}

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
	monitor, err := arbitrage.NewArbitrageMonitor(dashboard, cfg, exchanges...)
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

// setupExchanges initializes and returns a list of configured exchange instances.
func setupExchanges(cfg *config.Config) ([]exchange.Exchange, error) {
	var exchanges []exchange.Exchange

	// Setup Hyperliquid exchange
	hyperliquidEx := setupHyperliquidExchange(cfg.UseMainnet)
	exchanges = append(exchanges, hyperliquidEx)

	// Setup KuCoin exchange
	kuCoinEx, err := setupKuCoinExchange()
	if err != nil {
		return nil, fmt.Errorf("unable to setup KuCoin exchange: %v", err)
	}
	exchanges = append(exchanges, kuCoinEx)

	return exchanges, nil
}

// setupKuCoinExchange initializes a new KuCoin WebSocket client using an authentication token.
func setupKuCoinExchange() (*kucoin.KuCoinWS, error) {
	tokenResp, err := kucoin.GetToken("", "", "", false)
	if err != nil {
		return nil, err
	}

	return kucoin.NewKuCoinWS(tokenResp), nil
}

// setupHyperliquidExchange initializes a new Hyperliquid WebSocket client.
func setupHyperliquidExchange(useMainnet bool) *hyperliquid.HyperliquidWS {
	return hyperliquid.NewHyperliquidWS(useMainnet)
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
