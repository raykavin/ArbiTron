// internal/arbitrage/arbitrage.go
package arbitrage

import (
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/raykavin/ArbiTron/internal/config"
	"github.com/raykavin/ArbiTron/pkg/exchange"
)

// ArbitrageOpportunity represents a complete arbitrage opportunity across exchanges
type ArbitrageOpportunity struct {
	Symbol          string    // Trading pair symbol
	BuyExchange     string    // Exchange to buy from
	SellExchange    string    // Exchange to sell on
	BuyPrice        float64   // Price to buy at
	SellPrice       float64   // Price to sell at
	Spread          float64   // Percentage spread between buy and sell prices
	NetProfit       float64   // Net profit percentage after fees
	MaxTradeSize    float64   // Maximum trade size based on available liquidity
	PotentialProfit float64   // Potential profit in USD
	Timestamp       time.Time // When the opportunity was identified
}

// LogPositiveOpportunity logs profitable arbitrage opportunities to a log file
// It creates dated log files and records detailed information about each opportunity
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

	// Build log message
	var strBuilder strings.Builder
	fmt.Fprintln(&strBuilder, "PROFITABLE OPPORTUNITY!")
	fmt.Fprintf(&strBuilder, "Symbol:           %s\n", opportunity.Symbol)
	fmt.Fprintf(&strBuilder, "Buy:              %s @ %.8f\n", opportunity.BuyExchange, opportunity.BuyPrice)
	fmt.Fprintf(&strBuilder, "Sell:             %s @ %.8f\n", opportunity.SellExchange, opportunity.SellPrice)
	fmt.Fprintf(&strBuilder, "Spread:           %.6f%%\n", opportunity.Spread)
	fmt.Fprintf(&strBuilder, "Net Profit:       %.6f%%\n", opportunity.NetProfit)
	fmt.Fprintf(&strBuilder, "Maximum Volume:   %.8f\n", opportunity.MaxTradeSize)
	fmt.Fprintf(&strBuilder, "Potential Profit: $%.2f\n", opportunity.PotentialProfit)
	fmt.Fprintf(&strBuilder, "Timestamp:        %s\n", opportunity.Timestamp.Format("2006-01-02 15:04:05"))
	fmt.Fprintln(&strBuilder, strings.Repeat("-", 70))

	logger.Print(strBuilder.String())
	return nil
}

// FindBestPrices finds the lowest ask and highest bid across multiple exchanges.
// It returns the best orders to buy (lowest ask) and sell (highest bid) across exchanges.
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
		bidsToAdd := min(depth, len(orderBook.Bids))
		for i := range bidsToAdd {
			bid := orderBook.Bids[i]
			bid.Exchange = ex.GetName()
			bestBids = append(bestBids, bid)
		}

		asksToAdd := min(depth, len(orderBook.Asks))
		for i := range asksToAdd {
			ask := orderBook.Asks[i]
			ask.Exchange = ex.GetName()
			bestAsks = append(bestAsks, ask)
		}
	}

	// Check if we have any valid orders
	if len(bestBids) == 0 || len(bestAsks) == 0 {
		return exchange.Order{}, exchange.Order{}, fmt.Errorf("no valid bids or asks found")
	}

	// Default to first orders
	highestBid = bestBids[0]
	lowestAsk = bestAsks[0]

	// Find best prices across different exchanges
	for _, bid := range bestBids {
		// Update highest bid if higher
		if bid.Price > highestBid.Price {
			highestBid = bid
		}
	}

	for _, ask := range bestAsks {
		// Update lowest ask if lower
		if ask.Price < lowestAsk.Price {
			lowestAsk = ask
		}
	}

	// Ensure we're finding cross-exchange opportunities
	// If best bid and ask are from same exchange, this is likely not profitable
	if highestBid.Exchange == lowestAsk.Exchange {
		// Look for next best alternatives
		var nextBestBid, nextBestAsk exchange.Order
		var foundBid, foundAsk bool

		// Find next best bid from different exchange
		for _, bid := range bestBids {
			if bid.Exchange != lowestAsk.Exchange && (!foundBid || bid.Price > nextBestBid.Price) {
				nextBestBid = bid
				foundBid = true
			}
		}

		// Find next best ask from different exchange
		for _, ask := range bestAsks {
			if ask.Exchange != highestBid.Exchange && (!foundAsk || ask.Price < nextBestAsk.Price) {
				nextBestAsk = ask
				foundAsk = true
			}
		}

		// Choose the more promising opportunity
		if foundBid && foundAsk {
			// Calculate which alternative gives better spread
			spread1 := (highestBid.Price - nextBestAsk.Price) / nextBestAsk.Price
			spread2 := (nextBestBid.Price - lowestAsk.Price) / lowestAsk.Price

			if spread1 > spread2 {
				lowestAsk = nextBestAsk
			} else {
				highestBid = nextBestBid
			}
		} else if foundBid {
			highestBid = nextBestBid
		} else if foundAsk {
			lowestAsk = nextBestAsk
		}
	}

	return lowestAsk, highestBid, nil
}

// FindArbitrageOpportunities finds all arbitrage opportunities across exchanges
// Returns a list of valid arbitrage opportunities that meet profit requirements
func FindArbitrageOpportunities(exchanges []exchange.Exchange, cfg *config.Config) ([]ArbitrageOpportunity, error) {
	var opportunities []ArbitrageOpportunity

	// Pre-compute all needed order books at once
	orderBooks := make(map[string]map[string]*exchange.OrderBook)

	for _, coin := range cfg.Coins {
		orderBooks[coin] = make(map[string]*exchange.OrderBook)

		for _, ex := range exchanges {
			exchangeName := ex.GetName()
			book, err := ex.GetOrderBook(coin)
			if err == nil && len(book.Asks) > 0 && len(book.Bids) > 0 {
				orderBooks[coin][exchangeName] = book
			}
		}

		// For each coin, analyze all valid exchange combinations
		for buyExName, buyBook := range orderBooks[coin] {
			if len(buyBook.Asks) == 0 {
				continue
			}

			lowestAsk := buyBook.Asks[0]

			for sellExName, sellBook := range orderBooks[coin] {
				// Skip same exchange
				if buyExName == sellExName {
					continue
				}

				if len(sellBook.Bids) == 0 {
					continue
				}

				highestBid := sellBook.Bids[0]

				// Check if arbitrage potential exists
				if highestBid.Price <= lowestAsk.Price {
					continue
				}

				netProfit := CalculateNetProfitPercentage(
					lowestAsk.Price,
					highestBid.Price,
					cfg.GetFee(buyExName),
					cfg.GetFee(sellExName),
				)

				if netProfit <= cfg.MinProfitPercentage {
					continue
				}

				spread := CalculateSpreadPercentage(lowestAsk.Price, highestBid.Price)
				maxTradeSize := CalculateMaxTradeSize(lowestAsk, highestBid)
				potentialProfit := CalculatePotentialProfit(lowestAsk.Price, maxTradeSize, netProfit)

				if potentialProfit < cfg.MinProfitAmount {
					continue
				}

				opportunity := ArbitrageOpportunity{
					Symbol:          coin,
					BuyExchange:     buyExName,
					SellExchange:    sellExName,
					BuyPrice:        lowestAsk.Price,
					SellPrice:       highestBid.Price,
					Spread:          spread,
					NetProfit:       netProfit,
					MaxTradeSize:    maxTradeSize,
					PotentialProfit: potentialProfit,
					Timestamp:       time.Now(),
				}

				LogPositiveOpportunity(opportunity)
				opportunities = append(opportunities, opportunity)
			}
		}
	}

	return opportunities, nil
}

// CalculateSpreadPercentage calculates the raw spread as a percentage
// Formula: (sellPrice - buyPrice) / buyPrice * 100
func CalculateSpreadPercentage(askPrice, bidPrice float64) float64 {
	return (bidPrice - askPrice) / askPrice * 100
}

// CalculatePotentialProfit calculates the potential profit in USD
// Formula: (initialInvestment) * (profitPercentage / 100)
func CalculatePotentialProfit(askPrice, maxTradeSize, netProfitPercentage float64) float64 {
	initialInvestment := maxTradeSize * askPrice
	return initialInvestment * (netProfitPercentage / 100)
}

// CalculateMaxTradeSize returns the maximum possible trade size based on available liquidity
// It's limited by the smaller of the ask amount and bid amount
func CalculateMaxTradeSize(lowestAsk, highestBid exchange.Order) float64 {
	return math.Min(lowestAsk.Amount, highestBid.Amount)
}

// CalculateNetProfitPercentage calculates the net profit percentage after fees
// Formula: [((sell_price * (1 - sell_fee)) / (buy_price * (1 + buy_fee))) - 1] * 100
func CalculateNetProfitPercentage(ask, bid, buyFee, sellFee float64) float64 {
	// Convert percentage to decimal
	buyFeeDecimal := buyFee / 100.0
	sellFeeDecimal := sellFee / 100.0

	// Calculate net profit ratio
	netProfit := ((bid * (1 - sellFeeDecimal)) / (ask * (1 + buyFeeDecimal))) - 1

	// Convert to percentage
	netProfitPercentage := netProfit * 100

	return netProfitPercentage
}
