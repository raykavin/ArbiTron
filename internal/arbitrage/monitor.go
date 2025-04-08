// internal/arbitrage/monitor.go
package arbitrage

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/raykavin/ArbiTron/internal/config"
	"github.com/raykavin/ArbiTron/internal/ui"
	"github.com/raykavin/ArbiTron/pkg/exchange"
)

// ArbitrageMonitor handles the monitoring of arbitrage opportunities
// It manages exchange connections and coordinates opportunity detection
type ArbitrageMonitor struct {
	ctx             context.Context
	logger          ui.Logger
	dashboard       *ui.ArbitrageDashboard // UI dashboard to display opportunities
	config          *config.Config         // System configuration
	exchangeClients []exchange.Exchange    // Connected exchange APIs
}

// NewArbitrageMonitor creates and initializes a new ArbitrageMonitor
func NewArbitrageMonitor(ctx context.Context, cfg *config.Config, dashboard *ui.ArbitrageDashboard, logger ui.Logger, exchanges ...exchange.Exchange) (
	*ArbitrageMonitor,
	error,
) {
	if len(exchanges) == 0 {
		return nil, fmt.Errorf("one or more exchanges is required for monitoring")
	}

	return &ArbitrageMonitor{
		ctx:             ctx,
		exchangeClients: exchanges,
		dashboard:       dashboard,
		config:          cfg,
		logger:          logger,
	}, nil
}

// subscribeToCoins subscribes to order book updates for all configured coins
// Sets up data streams for each coin on each exchange
func (am *ArbitrageMonitor) subscribeToCoins() error {
	for _, coin := range am.config.Coins {
		for _, client := range am.exchangeClients {
			exchangeName := client.GetName()

			am.logger.Debugf("Subscribing to %s on %s...", coin, exchangeName)

			if err := client.SubscribeToOrderBook(coin); err != nil {
				am.logger.Errorf("Error subscribing to %s on %s: %v", coin, exchangeName, err)
			}
		}
	}
	return nil
}

// verifyInitialData checks if we can get initial data before proceeding
// Ensures we have valid market data to work with
func (am *ArbitrageMonitor) verifyInitialData() error {
	opportunities, err := FindArbitrageOpportunities(am.exchangeClients, am.config)
	if err != nil {
		// Note: We continue despite errors to maintain operation
		am.logger.Warnf("Initial opportunity scan had issues: %v", err)
	} else {
		am.logInitialOpportunities(opportunities)
	}

	return am.verifyDataForEachCoin()
}

// logInitialOpportunities logs any initial arbitrage opportunities found
// Useful for debugging and confirming system operation
func (am *ArbitrageMonitor) logInitialOpportunities(opportunities []ArbitrageOpportunity) {
	if len(opportunities) > 0 {
		am.logger.Infof("Found %d initial arbitrage opportunities", len(opportunities))

		for _, opp := range opportunities {
			am.logger.Infof(
				"Initial opportunity for %s: Buy at %.8f (%s), Sell at %.8f (%s), Profit: %.2f%%, Potential: $%.2f",
				opp.Symbol,
				opp.BuyPrice,
				opp.BuyExchange,
				opp.SellPrice,
				opp.SellExchange,
				opp.NetProfit,
				opp.PotentialProfit,
			)
		}
	} else {
		am.logger.Debug("No initial arbitrage opportunities found")
	}
}

// verifyDataForEachCoin checks if we have data for each configured coin concurrently
// Confirms we can access market data for all monitored coins
func (am *ArbitrageMonitor) verifyDataForEachCoin() error {
	var wg sync.WaitGroup
	errChan := make(chan error, len(am.config.Coins))

	// Verify each coin concurrently
	for _, coin := range am.config.Coins {
		wg.Add(1)
		go func(symbol string) {
			defer wg.Done()

			lowestAsk, highestBid, err := FindBestPrices(
				am.exchangeClients,
				symbol,
				am.config.OrderBookDepth,
				am.config.MaxStaleDuration,
			)

			if err == nil && lowestAsk.Price > 0 && highestBid.Price > 0 {
				am.logger.Debugf(
					"Initial data received for %s: Best ask: %.8f (%s), Best bid: %.8f (%s)",
					symbol,
					lowestAsk.Price,
					lowestAsk.Exchange,
					highestBid.Price,
					highestBid.Exchange,
				)
			} else {
				am.logger.Debugf("No initial data yet for %s: %v", symbol, err)
				// Not failing on missing data for individual coins
			}
		}(coin)
	}

	// Wait for all verifications to complete
	wg.Wait()
	close(errChan)

	// Check if any errors occurred
	select {
	case err := <-errChan:
		return err
	default:
		return nil
	}
}

// Start begins monitoring for arbitrage opportunities
// Sets up workers to continuously check for opportunities
func (am *ArbitrageMonitor) Start(ctx context.Context) error {
	am.logger.Debugf("Starting subscriptions with exchanges...")

	if err := am.subscribeToCoins(); err != nil {
		return err
	}

	am.logger.Debug("Waiting for initial order book updates...")

	if err := am.verifyInitialData(); err != nil {
		return err
	}

	var wg sync.WaitGroup

	// Create worker pool for processing arbitrage opportunities
	arbOpportunities := make(chan string, len(am.config.Coins))

	// Calculate optimal number of workers based on system resources
	workers := max(runtime.NumCPU()/2, 1)

	am.logger.Debugf("Starting %d arbitrage processing workers", workers)

	// Start workers
	for range workers {
		wg.Add(1)
		go am.processArbitrageWorker(am.ctx, arbOpportunities, &wg)
	}

	// Start producer goroutine
	go am.produceArbitrageOpportunities(am.ctx, arbOpportunities)

	// Wait for all workers to finish (triggered by context cancellation)
	wg.Wait()

	return nil
}

// processArbitrageWorker handles processing of arbitrage opportunities
// Worker goroutine that processes coins from the channel
func (am *ArbitrageMonitor) processArbitrageWorker(ctx context.Context, arbOpportunities <-chan string, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case coin, ok := <-arbOpportunities:
			if !ok {
				return // channel closed
			}
			am.checkArbitrageOpportunity(coin)
			// Brief pause between processing to avoid overloading exchanges
			time.Sleep(50 * time.Millisecond)
		case <-ctx.Done():
			return // context cancelled
		}
	}
}

// produceArbitrageOpportunities generates arbitrage opportunities to check
// Continuously feeds coins to check into the channel
func (am *ArbitrageMonitor) produceArbitrageOpportunities(ctx context.Context, arbOpportunities chan<- string) {
	// Use an appropriate interval to reduce load on exchanges
	ticker := time.NewTicker(am.config.CheckInterval)
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
					// this prevents backpressure if processing is slow
				}
			}
		case <-ctx.Done():
			return // context cancelled
		}
	}
}

// checkArbitrageOpportunity looks for arbitrage opportunities for a symbol
// Analyzes market data for a single coin and updates the dashboard
func (am *ArbitrageMonitor) checkArbitrageOpportunity(symbol string) {
	// First, get basic prices to ensure we have data for the UI
	lowestAsk, highestBid, err := FindBestPrices(
		am.exchangeClients,
		symbol,
		am.config.OrderBookDepth,
		am.config.MaxStaleDuration,
	)

	if err != nil {
		// Silently handle errors to avoid log spam
		return
	}

	// Initialize default values
	spread := 0.0
	netProfit := 0.0
	potentialProfit := 0.0
	liquidProfit := 0.0
	maxTradeSize := 0.0

	// Calculate the basic spread if we have valid prices
	if lowestAsk.Price > 0 && highestBid.Price > 0 {
		spread = CalculateSpreadPercentage(lowestAsk.Price, highestBid.Price)
		maxTradeSize = CalculateMaxTradeSize(lowestAsk, highestBid)
	}

	// Create a temporary configuration only for this specific symbol
	// This allows us to check for profitable opportunities without modifying the main config
	tempConfig := *am.config
	tempConfig.Coins = []string{symbol}

	// Now try to find arbitrage opportunities that meet profit requirements
	opportunities, _ := FindArbitrageOpportunities(am.exchangeClients, &tempConfig)

	// If we find opportunities, use the data from the most profitable one
	if len(opportunities) > 0 {
		// Sort opportunities by net profit if there are multiple
		bestOpportunity := opportunities[0]
		for _, opp := range opportunities[1:] {
			if opp.NetProfit > bestOpportunity.NetProfit {
				bestOpportunity = opp
			}
		}

		// Update values with opportunity data
		lowestAsk.Exchange = bestOpportunity.BuyExchange
		lowestAsk.Price = bestOpportunity.BuyPrice
		highestBid.Exchange = bestOpportunity.SellExchange
		highestBid.Price = bestOpportunity.SellPrice
		spread = bestOpportunity.Spread
		netProfit = bestOpportunity.NetProfit
		maxTradeSize = bestOpportunity.MaxTradeSize
		potentialProfit = bestOpportunity.PotentialProfit

		// Calculate liquid profit (efficiency ratio)
		if spread > 0 {
			liquidProfit = netProfit / spread * 100
		}
	}

	// Set profitable flag if this opportunity meets the minimum requirements
	profitable := netProfit > am.config.MinProfitPercentage && potentialProfit > am.config.MinProfitAmount

	// Always send data to the dashboard, even if not profitable
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
		Profitable:      profitable,
		FlashUntil:      time.Time{}, // Will be set in updateCoinWidget if profitable
	})
}
