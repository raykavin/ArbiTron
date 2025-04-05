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
	"time"
)

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

// LogPositiveOpportunity registra oportunidades de arbitragem lucrativas em um arquivo de log
func LogPositiveOpportunity(opportunity ArbitrageOpportunity) error {
	// Criar diretório logs se não existir
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	// Criar ou abrir arquivo de log (um arquivo por dia)
	today := time.Now().Format("2006-01-02")
	logFile := filepath.Join(logDir, fmt.Sprintf("positive_arbitrage_%s.log", today))

	file, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer file.Close()

	// Criar um logger
	logger := log.New(file, "", log.LstdFlags)

	// Formatar e gravar a oportunidade
	logEntry := fmt.Sprintf(
		"OPORTUNIDADE LUCRATIVA!\n"+
			"Symbol: %s\n"+
			"Buy: %s @ %.8f\n"+
			"Sell: %s @ %.8f\n"+
			"Spread: %.6f%%\n"+
			"Lucro Líquido: %.6f%%\n"+
			"Volume Máximo: %.8f\n"+
			"Lucro Potencial: $%.2f\n"+
			"Timestamp: %s\n"+
			"-------------------------------------\n",
		opportunity.Symbol,
		opportunity.BuyExchange, opportunity.BuyPrice,
		opportunity.SellExchange, opportunity.SellPrice,
		opportunity.Spread,
		opportunity.NetProfit,
		opportunity.MaxTradeSize,
		opportunity.PotentialProfit,
		opportunity.Timestamp.Format("2006-01-02 15:04:05"),
	)

	logger.Println(logEntry)
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

		// Check if order book is stale
		if time.Since(orderBook.Timestamp) > maxStaleDuration {
			continue // Skip stale data
		}

		// Extract bids and asks up to configured depth
		maxDepth := min(depth, len(orderBook.Bids))
		for i := range maxDepth {
			if i < len(orderBook.Bids) {
				bestBids = append(bestBids, orderBook.Bids[i])
			}
		}

		maxDepth = min(depth, len(orderBook.Asks))
		for i := range maxDepth {
			if i < len(orderBook.Asks) {
				bestAsks = append(bestAsks, orderBook.Asks[i])
			}
		}
	}

	// Find the highest bid and lowest ask across all the different exchanges
	if len(bestBids) == 0 || len(bestAsks) == 0 {
		return exchange.Order{}, exchange.Order{}, fmt.Errorf("no bids or asks found")
	}

	highestBid = bestBids[0]
	lowestAsk = bestAsks[0]

	// Eliminating the same exchange arbitrage entirely
	foundValidPair := false

	for _, bid := range bestBids {
		for _, ask := range bestAsks {
			if bid.Exchange == ask.Exchange {
				continue
			}

			// Update highest bid if higher and from different exchange than current lowest ask
			if bid.Exchange != lowestAsk.Exchange && bid.Price > highestBid.Price {
				highestBid = bid
				foundValidPair = true
			}

			// Update lowest ask if lower and from different exchange than current highest bid
			if ask.Exchange != highestBid.Exchange && ask.Price < lowestAsk.Price {
				lowestAsk = ask
				foundValidPair = true
			}
		}
	}

	if !foundValidPair {
		// return exchange.Order{}, exchange.Order{}, fmt.Errorf("no valid cross-exchange opportunities found")
	}

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
			lowestAsk.Price,
			highestBid.Price,
			cfg.TradeFees,
			lowestAsk.Exchange,
			highestBid.Exchange,
		)

		// Skip if profit below threshold
		if netProfit <= cfg.ProfitThreshold {
			continue
		}

		spread := (highestBid.Price - lowestAsk.Price) / lowestAsk.Price * 100
		maxTradeSize := CalculateMaxTradeSize(lowestAsk, highestBid)
		potentialProfit := (maxTradeSize * lowestAsk.Price) * (netProfit / 100)

		// Skip if potential profit too small
		if potentialProfit < cfg.MinProfitUSD {
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

		// Log
		if netProfit > 0 {
			LogPositiveOpportunity(opportunity)
		}

		opportunities = append(opportunities, opportunity)
	}

	return opportunities, nil
}

// CalculateMaxTradeSize returns the maximum possible trade size based on available liquidity
func CalculateMaxTradeSize(lowestAsk, highestBid exchange.Order) float64 {
	return math.Min(lowestAsk.Amount, highestBid.Amount)
}

// CalculateNetProfitPercentage calculates the net profit percentage considering fees
func CalculateNetProfitPercentage(ask, bid float64, fees map[string]float64, buyExchange, sellExchange string) float64 {
	// Use default fees if not specified
	buyFee := 0.1  // default 0.1%
	sellFee := 0.1 // default 0.1%

	// Get fees from map if available
	if fee, exists := fees[buyExchange]; exists {
		buyFee = fee
	}
	if fee, exists := fees[sellExchange]; exists {
		sellFee = fee
	}

	// Convert percentage to decimal
	buyFeeDecimal := buyFee / 100.0
	sellFeeDecimal := sellFee / 100.0

	netProfit := ((bid * (1 - sellFeeDecimal)) / (ask * (1 + buyFeeDecimal))) - 1

	// Convert to percentage
	netProfitPercentage := netProfit * 100

	return netProfitPercentage
}
