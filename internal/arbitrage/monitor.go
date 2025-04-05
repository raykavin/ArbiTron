package arbitrage

import (
	"context"
	"fmt"
	"log"
	"math"
	"runtime"
	"sync"
	"time"

	"github.com/raykavin/ArbiTron/internal/config"
	"github.com/raykavin/ArbiTron/internal/exchange"
	"github.com/raykavin/ArbiTron/internal/ui"
)

// ArbitrageMonitor handles the monitoring of arbitrage opportunities
type ArbitrageMonitor struct {
	exchangeClients []exchange.Exchange
	dashboard       *ui.ArbitrageDashboard
	config          *config.Config
}

// NewArbitrageMonitor creates and initializes a new ArbitrageMonitor
func NewArbitrageMonitor(dashboard *ui.ArbitrageDashboard, cfg *config.Config, exchanges ...exchange.Exchange) (*ArbitrageMonitor, error) {
	if len(exchanges) == 0 {
		return nil, fmt.Errorf("one or more exchanges is required for monitoring")
	}

	return &ArbitrageMonitor{
		exchangeClients: exchanges,
		dashboard:       dashboard,
		config:          cfg,
	}, nil
}

// Connect establishes connections with all exchanges and ensures they are operational
func (am *ArbitrageMonitor) Connect(ctx context.Context) error {
	log.Println("Starting connections with exchanges...")

	if err := am.connectToExchanges(ctx); err != nil {
		return err
	}

	if err := am.subscribeToCoins(); err != nil {
		return err
	}

	log.Println("Waiting for initial order book updates...")
	time.Sleep(am.config.InitialWaitTime)

	return am.verifyInitialData()
}

// connectToExchanges connects to all configured exchanges
func (am *ArbitrageMonitor) connectToExchanges(ctx context.Context) error {
	for i, client := range am.exchangeClients {
		exchangeName := am.getExchangeName(i)

		log.Printf("Connecting to %s...", exchangeName)
		if err := client.Connect(ctx); err != nil {
			return fmt.Errorf("failed to connect to %s: %w", exchangeName, err)
		}
		log.Printf("Successfully connected to %s", exchangeName)
	}
	return nil
}

// subscribeToCoins subscribes to order book updates for all configured coins
func (am *ArbitrageMonitor) subscribeToCoins() error {
	for _, coin := range am.config.Coins {
		for i, client := range am.exchangeClients {
			exchangeName := am.getExchangeName(i)

			log.Printf("Subscribing to %s on %s...", coin, exchangeName)
			if err := client.SubscribeToOrderBook(coin); err != nil {
				log.Printf("Error subscribing to %s on %s: %v",
					coin, exchangeName, err)
			}
		}
	}
	return nil
}

// verifyInitialData checks if we can get initial data before proceeding
func (am *ArbitrageMonitor) verifyInitialData() error {
	opportunities, err := FindArbitrageOpportunities(am.exchangeClients, am.config)
	if err != nil {
		// log.Printf("Error finding initial arbitrage opportunities: %v", err)
		return nil
	}

	am.logInitialOpportunities(opportunities)
	return am.verifyDataForEachCoin()
}

// logInitialOpportunities logs any initial arbitrage opportunities found
func (am *ArbitrageMonitor) logInitialOpportunities(opportunities []ArbitrageOpportunity) {
	if len(opportunities) > 0 {
		log.Printf("Found %d initial arbitrage opportunities", len(opportunities))
		for _, opp := range opportunities {
			log.Printf("Initial opportunity for %s: Buy at %.8f (%s), Sell at %.8f (%s), Profit: %.2f%%, Potential: $%.2f",
				opp.Symbol, opp.BuyPrice, opp.BuyExchange, opp.SellPrice, opp.SellExchange, opp.NetProfit, opp.PotentialProfit)
		}
	} else {
		log.Println("No initial arbitrage opportunities found")
	}
}

// verifyDataForEachCoin checks if we have data for each configured coin
func (am *ArbitrageMonitor) verifyDataForEachCoin() error {
	// To ensure we have data for all coins, we check each one individually
	for _, coin := range am.config.Coins {
		lowestAsk, highestBid, err := FindBestPrices(
			am.exchangeClients,
			coin,
			am.config.OrderBookDepth,
			am.config.MaxStaleDuration,
		)

		if err == nil && lowestAsk.Price > 0 && highestBid.Price > 0 {
			log.Printf("Initial data received for %s: Best ask: %.8f (%s), Best bid: %.8f (%s)",
				coin, lowestAsk.Price, lowestAsk.Exchange, highestBid.Price, highestBid.Exchange)
		} else {
			log.Printf("Warning: No initial data yet for %s: %v", coin, err)
		}
	}
	return nil
}

// Start begins monitoring for arbitrage opportunities
func (am *ArbitrageMonitor) Start(ctx context.Context) {
	var wg sync.WaitGroup

	// Create worker pool for processing arbitrage opportunities
	arbOpportunities := make(chan string, len(am.config.Coins))

	// Reduce the number of workers to avoid overload
	workers := max(runtime.GOMAXPROCS(0)/2, 1)

	// Start workers
	for range workers {
		wg.Add(1)
		go am.processArbitrageWorker(ctx, arbOpportunities, &wg)
	}

	// Start producer goroutine
	go am.produceArbitrageOpportunities(ctx, arbOpportunities)

	wg.Wait()
}

// processArbitrageWorker handles processing of arbitrage opportunities
func (am *ArbitrageMonitor) processArbitrageWorker(ctx context.Context, arbOpportunities <-chan string, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case coin, ok := <-arbOpportunities:
			if !ok {
				return // channel closed
			}
			am.checkArbitrageOpportunity(coin)
			// Brief pause between processing
			time.Sleep(50 * time.Millisecond)
		case <-ctx.Done():
			return
		}
	}
}

// produceArbitrageOpportunities generates arbitrage opportunities to check
func (am *ArbitrageMonitor) produceArbitrageOpportunities(ctx context.Context, arbOpportunities chan<- string) {
	// Use a longer interval to reduce load
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	defer close(arbOpportunities)

	for {
		select {
		case <-ticker.C:
			for _, coin := range am.config.Coins {
				select {
				case arbOpportunities <- coin:
					// successfully sent
				default:
					// channel full, skip to avoid blocking
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

// checkArbitrageOpportunity looks for arbitrage opportunities for a symbol
func (am *ArbitrageMonitor) checkArbitrageOpportunity(symbol string) {
	// First, get basic prices to ensure we have data for the UI
	lowestAsk, highestBid, err := FindBestPrices(
		am.exchangeClients,
		symbol,
		am.config.OrderBookDepth,
		am.config.MaxStaleDuration,
	)

	if err != nil {
		// log.Printf("Error finding best prices for %s: %v\n", symbol, err)
		return
	}

	// Calculate the basic spread (can be negative if there's no opportunity)
	spread := 0.0
	if lowestAsk.Price > 0 {
		spread = (highestBid.Price - lowestAsk.Price) / lowestAsk.Price * 100
	}

	maxTradeSize := math.Min(lowestAsk.Amount, highestBid.Amount)

	// Default values
	netProfit := 0.0
	potentialProfit := 0.0
	liquidProfit := 0.0

	// Create a temporary configuration only for this specific symbol
	tempConfig := *am.config
	tempConfig.Coins = []string{symbol}

	// Now try to find arbitrage opportunities using FindArbitrageOpportunities
	opportunities, _ := FindArbitrageOpportunities(am.exchangeClients, &tempConfig)

	// If we find opportunities, use its data (which already has all calculations done)
	if len(opportunities) > 0 {
		opp := opportunities[0]

		// Update values with opportunity data
		lowestAsk.Exchange = opp.BuyExchange
		lowestAsk.Price = opp.BuyPrice
		highestBid.Exchange = opp.SellExchange
		highestBid.Price = opp.SellPrice
		spread = opp.Spread
		netProfit = opp.NetProfit
		maxTradeSize = opp.MaxTradeSize
		potentialProfit = opp.PotentialProfit

		// Calculate liquid profit (specific to this function)
		if spread != 0 {
			liquidProfit = netProfit / spread
		}
	}

	// Always send data to the dashboard
	am.dashboard.SendCoinData(ui.CoinData{
		Symbol:          symbol,
		BuyExchange:     lowestAsk.Exchange,
		BuyPrice:        lowestAsk.Price,
		SellExchange:    highestBid.Exchange,
		SellPrice:       highestBid.Price,
		Profit:          netProfit,
		Spread:          spread,
		LiquidProfit:    liquidProfit,
		MaxTradeSize:    maxTradeSize,
		PotentialProfit: potentialProfit,
		Timestamp:       time.Now(),
	})
}

// getExchangeName returns a friendly name for an exchange by index
func (am *ArbitrageMonitor) getExchangeName(index int) string {
	if index == 0 {
		return "Hyperliquid"
	} else if index == 1 {
		return "KuCoin"
	}
	return "unknown"
}
