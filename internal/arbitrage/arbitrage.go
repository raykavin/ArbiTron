// internal/arbitrage/arbitrage.go
package arbitrage

import (
	"fmt"
	"log"
	"math"
	"notlelouch/ArbiBot/internal/config"
	"notlelouch/ArbiBot/internal/exchange"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var strBuilder strings.Builder

// ArbitrageOpportunity represents a complete arbitrage opportunity
type ArbitrageOpportunity struct {
	Symbol          string
	BuyExchange     string
	SellExchange    string
	BuyPrice        float64
	SellPrice       float64
	Spread          float64
	NetProfit       float64
	MaxTradeSize    float64
	PotentialProfit float64
	Timestamp       time.Time
}

// LogPositiveOpportunity logs profitable arbitrage opportunities to a log file
func LogPositiveOpportunity(opportunity ArbitrageOpportunity) error {
	// Create logs directory if it doesn't exist
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	// Create or open log file (one file per day)
	today := time.Now().Format("2006-01-02")
	logFile := filepath.Join(logDir, fmt.Sprintf("positive_arbitrage_%s.log", today))

	file, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer file.Close()

	// Create a logger
	logger := log.New(file, "", log.LstdFlags)

	fmt.Fprintln(&strBuilder, "PROFITABLE OPPORTUNITY!")
	fmt.Fprintf(&strBuilder, "Symbol:           %s\n", opportunity.Symbol)
	fmt.Fprintf(&strBuilder, "Buy:              %s @ %.8f\n", opportunity.BuyExchange, opportunity.BuyPrice)
	fmt.Fprintf(&strBuilder, "Sell:             %s @ %.8f\n", opportunity.SellExchange, opportunity.SellPrice)
	fmt.Fprintf(&strBuilder, "Spread:           %.6f%%\n", opportunity.Spread)
	fmt.Fprintf(&strBuilder, "Net Profit:       %.6f%%\n", opportunity.NetProfit)
	fmt.Fprintf(&strBuilder, "Maximum Volume:   %.8f\n", opportunity.MaxTradeSize)
	fmt.Fprintf(&strBuilder, "Potential Profit: $%.2f\n", opportunity.PotentialProfit)
	fmt.Fprintf(&strBuilder, "Timestamp:        %s\n", opportunity.Timestamp.Format("2006-01-02 15:04:05"))
	fmt.Fprintln(&strBuilder, "-------------------------------------")

	logger.Print(strBuilder.String())
	strBuilder.Reset()

	return nil
}

// FindBestPrices finds the lowest ask and highest bid across multiple exchanges.
func FindBestPrices(exchanges []exchange.Exchange, coin string, depth int, maxStaleDuration time.Duration) (lowestAsk, highestBid exchange.Order, err error) {
	var bestBids, bestAsks []exchange.Order

	// Fetch order books from all exchanges
	for _, ex := range exchanges {
		orderBook, err := ex.GetOrderBook(coin)
		if err != nil {
			continue
		}

		// Skip stale data
		if time.Since(orderBook.Timestamp) > maxStaleDuration {
			continue
		}

		// Extract bids and asks up to configured depth
		maxBidDepth := min(depth, len(orderBook.Bids))
		for i := range maxBidDepth {
			bestBids = append(bestBids, orderBook.Bids[i])
		}

		maxAskDepth := min(depth, len(orderBook.Asks))
		for i := range maxAskDepth {
			bestAsks = append(bestAsks, orderBook.Asks[i])
		}
	}

	// Check if we have any valid orders
	if len(bestBids) == 0 || len(bestAsks) == 0 {
		return exchange.Order{}, exchange.Order{}, fmt.Errorf("no bids or asks found")
	}

	highestBid = bestBids[0]
	lowestAsk = bestAsks[0]

	// Find best prices across different exchanges
	// foundValidPair := false

	for _, bid := range bestBids {
		for _, ask := range bestAsks {
			// Skip same-exchange opportunities
			if bid.Exchange == ask.Exchange {
				continue
			}

			// Update highest bid if higher and from different exchange than current lowest ask
			if bid.Exchange != lowestAsk.Exchange && bid.Price > highestBid.Price {
				highestBid = bid
				// foundValidPair = true
			}

			// Update lowest ask if lower and from different exchange than current highest bid
			if ask.Exchange != highestBid.Exchange && ask.Price < lowestAsk.Price {
				lowestAsk = ask
				// foundValidPair = true
			}
		}
	}

	// Validation check is commented out to match original behavior
	// if !foundValidPair {
	//     return exchange.Order{}, exchange.Order{}, fmt.Errorf("no valid cross-exchange opportunities found")
	// }

	return lowestAsk, highestBid, nil
}

// FindArbitrageOpportunities finds all arbitrage opportunities across exchanges
func FindArbitrageOpportunities(exchanges []exchange.Exchange, cfg *config.Config) ([]ArbitrageOpportunity, error) {
	var opportunities []ArbitrageOpportunity

	for _, coin := range cfg.Coins {
		lowestAsk, highestBid, err := FindBestPrices(exchanges, coin, cfg.OrderBookDepth, cfg.MaxStaleDuration)
		if err != nil {
			continue
		}

		// Skip if no valid arbitrage (sell price <= buy price)
		if highestBid.Price <= lowestAsk.Price {
			continue
		}

		// Calculate profit metrics
		netProfit := CalculateNetProfitPercentage(
			cfg,
			lowestAsk.Price,
			highestBid.Price,
			lowestAsk.Exchange,
			highestBid.Exchange,
		)

		// Skip if profit below threshold
		if netProfit <= cfg.MinProfitPercentage {
			continue
		}

		// Calculate additional metrics
		spread := CalculateSpreadPercentage(lowestAsk.Price, highestBid.Price)
		maxTradeSize := CalculateMaxTradeSize(lowestAsk, highestBid)
		potentialProfit := CalculatePotentialProfit(lowestAsk.Price, maxTradeSize, netProfit)

		// Skip if potential profit too small
		if potentialProfit < cfg.MinProfitAmount {
			continue
		}

		// Create opportunity object
		opportunity := ArbitrageOpportunity{
			Symbol:          coin,
			BuyExchange:     lowestAsk.Exchange,
			SellExchange:    highestBid.Exchange,
			BuyPrice:        lowestAsk.Price,
			SellPrice:       highestBid.Price,
			Spread:          spread,
			NetProfit:       netProfit,
			MaxTradeSize:    maxTradeSize,
			PotentialProfit: potentialProfit,
			Timestamp:       time.Now(),
		}

		// Log profitable opportunities
		if netProfit > 0 {
			LogPositiveOpportunity(opportunity)
		}

		opportunities = append(opportunities, opportunity)
	}

	return opportunities, nil
}

// CalculateSpreadPercentage calculates the spread as a percentage
func CalculateSpreadPercentage(askPrice, bidPrice float64) float64 {
	return (bidPrice - askPrice) / askPrice * 100
}

// CalculatePotentialProfit calculates the potential profit in USD
func CalculatePotentialProfit(askPrice, maxTradeSize, netProfitPercentage float64) float64 {
	return (maxTradeSize * askPrice) * (netProfitPercentage / 100)
}

// CalculateMaxTradeSize returns the maximum possible trade size based on available liquidity
func CalculateMaxTradeSize(lowestAsk, highestBid exchange.Order) float64 {
	return math.Min(lowestAsk.Amount, highestBid.Amount)
}

// CalculateNetProfitPercentage calculates the net profit percentage considering fees
func CalculateNetProfitPercentage(cfg *config.Config, ask, bid float64, buyExchange, sellExchange string) float64 {
	// Get exchange-specific fees if available
	buyFee := cfg.GetFee(buyExchange)
	sellFee := cfg.GetFee(sellExchange)

	// Convert percentage to decimal
	buyFeeDecimal := buyFee / 100.0
	sellFeeDecimal := sellFee / 100.0

	// Calculate net profit ratio
	netProfit := ((bid * (1 - sellFeeDecimal)) / (ask * (1 + buyFeeDecimal))) - 1

	// Convert to percentage
	netProfitPercentage := netProfit * 100

	return netProfitPercentage
}
