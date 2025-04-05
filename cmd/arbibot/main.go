package main

import (
	"context"
	"fmt"
	"log"
	"notlelouch/ArbiBot/internal/arbitrage"
	"notlelouch/ArbiBot/internal/exchange"
	"notlelouch/ArbiBot/internal/exchange/hyperliquid"
	"notlelouch/ArbiBot/internal/exchange/kucoin"
	"notlelouch/ArbiBot/internal/ui"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const (
	initialWaitTime      = 2 * time.Second
	arbitrageCheckPeriod = 400 * time.Millisecond
)

// ArbitrageMonitor handles the monitoring of arbitrage opportunities
type ArbitrageMonitor struct {
	dashboard       *ui.ArbitrageDashboard
	coins           []string
	exchangeClients []exchange.Exchange
}

// NewArbitrageMonitor creates and initializes a new ArbitrageMonitor
func NewArbitrageMonitor(dashboard *ui.ArbitrageDashboard, coins []string) (*ArbitrageMonitor, error) {
	// Get public token for KuCoin
	tokenResp, err := kucoin.GetToken("", "", "", false)
	if err != nil {
		return nil, err
	}

	// Initialize exchange clients
	hyperliquidClient := hyperliquid.NewHyperliquidWS(true)
	kucoinClient := kucoin.NewKuCoinWS(tokenResp)

	return &ArbitrageMonitor{
		dashboard:       dashboard,
		coins:           coins,
		exchangeClients: []exchange.Exchange{hyperliquidClient, kucoinClient},
	}, nil
}

// Connect establishes connections to all exchanges
func (am *ArbitrageMonitor) Connect(ctx context.Context) error {
	for i, client := range am.exchangeClients {
		if err := client.Connect(ctx); err != nil {
			exchangeName := "unknown"
			if i == 0 {
				exchangeName = "Hyperliquid"
			} else if i == 1 {
				exchangeName = "KuCoin"
			}
			return fmt.Errorf("failed to connect to %s: %w", exchangeName, err)
		}
	}

	log.Println("Waiting for order book updates...")
	time.Sleep(initialWaitTime)
	return nil
}

// Start begins the arbitrage monitoring for all coins
func (am *ArbitrageMonitor) Start(ctx context.Context) {
	var wg sync.WaitGroup

	for _, coin := range am.coins {
		wg.Add(1)
		go am.monitorCoin(ctx, coin, &wg)
	}

	wg.Wait()
}

// monitorCoin handles the arbitrage monitoring for a single coin
func (am *ArbitrageMonitor) monitorCoin(ctx context.Context, symbol string, wg *sync.WaitGroup) {
	defer wg.Done()

	// Subscribe to order books
	for i, client := range am.exchangeClients {
		if err := client.SubscribeToOrderBook(symbol); err != nil {
			exchangeName := "unknown"
			if i == 0 {
				exchangeName = "Hyperliquid"
			} else if i == 1 {
				exchangeName = "KuCoin"
			}
			log.Printf("Failed to subscribe to order book for %s on %s: %v\n",
				symbol, exchangeName, err)
			return
		}
	}

	ticker := time.NewTicker(arbitrageCheckPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			am.checkArbitrageOpportunity(symbol)
		case <-ctx.Done():
			return
		}
	}
}

// checkArbitrageOpportunity looks for arbitrage opportunities for a symbol
func (am *ArbitrageMonitor) checkArbitrageOpportunity(symbol string) {
	lowestAsk, highestBid, err := arbitrage.FindBestPrices(am.exchangeClients, symbol)
	if err != nil {
		log.Printf("Error finding best prices for %s: %v\n", symbol, err)
		return
	}

	// Only process valid arbitrage opportunities where sell price > buy price
	if highestBid.Price <= lowestAsk.Price {
		return
	}

	netProfit := arbitrage.CalculateNetProfitPercentage(lowestAsk.Price, highestBid.Price)
	spread := (highestBid.Price - lowestAsk.Price) / lowestAsk.Price * 100

	liquidProfit := 0.0
	if spread != 0 {
		liquidProfit = netProfit / spread
	}

	// Send update to dashboard
	am.dashboard.SendCoinData(ui.CoinData{
		Symbol:       symbol,
		BuyExchange:  lowestAsk.Exchange,
		BuyPrice:     lowestAsk.Price,
		SellExchange: highestBid.Exchange,
		SellPrice:    highestBid.Price,
		Profit:       netProfit,
		Spread:       spread,
		LiquidProfit: liquidProfit,
		Timestamp:    time.Now(),
	})
}

func main() {
	// Define the list of coins to monitor
	coins := []string{
		"LINK", "ATOM", "BTC", "AVAX", "ADA",
	}

	// Create a context that can be canceled
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	setupSignalHandler(cancel)

	// Create and initialize the arbitrage dashboard
	dashboard := ui.NewArbitrageDashboard(coins)
	if err := dashboard.InitWidgets(); err != nil {
		log.Fatalf("Failed to initialize widgets: %v", err)
	}

	// Start update listener for the dashboard
	dashboard.StartUpdateListener(ctx)

	// Create and start the arbitrage monitor
	monitor, err := NewArbitrageMonitor(dashboard, coins)
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
		log.Println("Encerrando...")
		cancel()
	}()
}
