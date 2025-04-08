// internal/arbitrage/arbitrage_test.go
package arbitrage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/raykavin/ArbiTron/internal/config"
	"github.com/raykavin/ArbiTron/pkg/exchange"
	"github.com/stretchr/testify/assert"
)

// MockExchange implements the Exchange interface for testing
type MockExchange struct {
	name      string
	orderBook *exchange.OrderBook
}

func (m *MockExchange) GetName() string {
	return m.name
}

func (m *MockExchange) GetOrderBook(symbol string) (*exchange.OrderBook, error) {
	return m.orderBook, nil
}

// Added Connect method to satisfy the Exchange interface
func (m *MockExchange) Connect(ctx context.Context) error {
	// Does nothing in a mock, just returns success
	return nil
}

// Added SubscribeToOrderBook method to satisfy the Exchange interface
func (m *MockExchange) SubscribeToOrderBook(symbol string) error {
	// Does nothing in a mock, just returns success
	return nil
}

func TestCalculateSpreadPercentage(t *testing.T) {
	tests := []struct {
		name     string
		askPrice float64
		bidPrice float64
		expected float64
	}{
		{
			name:     "Positive spread",
			askPrice: 100.0,
			bidPrice: 105.0,
			expected: 5.0,
		},
		{
			name:     "Zero spread",
			askPrice: 100.0,
			bidPrice: 100.0,
			expected: 0.0,
		},
		{
			name:     "Negative spread",
			askPrice: 105.0,
			bidPrice: 100.0,
			expected: -4.76190, // (100-105)/105*100
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := CalculateSpreadPercentage(tc.askPrice, tc.bidPrice)
			assert.InDelta(t, tc.expected, result, 0.0001, "The spread calculation should be correct")
		})
	}
}

func TestCalculateNetProfitPercentage(t *testing.T) {
	tests := []struct {
		name     string
		ask      float64
		bid      float64
		buyFee   float64
		sellFee  float64
		expected float64
	}{
		{
			name:     "Positive profit without fees",
			ask:      100.0,
			bid:      105.0,
			buyFee:   0.0,
			sellFee:  0.0,
			expected: 5.0,
		},
		{
			name:     "Positive profit with fees",
			ask:      100.0,
			bid:      110.0,
			buyFee:   0.1,
			sellFee:  0.1,
			expected: 9.7890, // ((110 * (1 - 0.001)) / (100 * (1 + 0.001))) - 1) * 100
		},
		{
			name:     "Break even with fees",
			ask:      100.0,
			bid:      100.2,
			buyFee:   0.1,
			sellFee:  0.1,
			expected: 0.0, // Approximately break-even
		},
		{
			name:     "Negative profit with fees",
			ask:      100.0,
			bid:      100.0,
			buyFee:   0.1,
			sellFee:  0.1,
			expected: -0.1998, // Slight loss due to fees
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := CalculateNetProfitPercentage(tc.ask, tc.bid, tc.buyFee, tc.sellFee)
			assert.InDelta(t, tc.expected, result, 0.01, "The net profit calculation should be correct")
		})
	}
}

func TestCalculateMaxTradeSize(t *testing.T) {
	tests := []struct {
		name       string
		lowestAsk  exchange.Order
		highestBid exchange.Order
		expected   float64
	}{
		{
			name:       "Ask has lower amount",
			lowestAsk:  exchange.Order{Price: 100.0, Amount: 1.5},
			highestBid: exchange.Order{Price: 105.0, Amount: 2.0},
			expected:   1.5,
		},
		{
			name:       "Bid has lower amount",
			lowestAsk:  exchange.Order{Price: 100.0, Amount: 2.0},
			highestBid: exchange.Order{Price: 105.0, Amount: 1.5},
			expected:   1.5,
		},
		{
			name:       "Equal amounts",
			lowestAsk:  exchange.Order{Price: 100.0, Amount: 1.5},
			highestBid: exchange.Order{Price: 105.0, Amount: 1.5},
			expected:   1.5,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := CalculateMaxTradeSize(tc.lowestAsk, tc.highestBid)
			assert.Equal(t, tc.expected, result, "The maximum trade size calculation should be the minimum value between orders")
		})
	}
}

func TestCalculatePotentialProfit(t *testing.T) {
	tests := []struct {
		name             string
		askPrice         float64
		maxTradeSize     float64
		netProfitPercent float64
		expected         float64
	}{
		{
			name:             "Standard calculation",
			askPrice:         100.0,
			maxTradeSize:     2.0,
			netProfitPercent: 5.0,
			expected:         10.0, // (2.0 * 100.0) * (5.0 / 100)
		},
		{
			name:             "Zero profit percentage",
			askPrice:         100.0,
			maxTradeSize:     2.0,
			netProfitPercent: 0.0,
			expected:         0.0,
		},
		{
			name:             "Zero trade size",
			askPrice:         100.0,
			maxTradeSize:     0.0,
			netProfitPercent: 5.0,
			expected:         0.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := CalculatePotentialProfit(tc.askPrice, tc.maxTradeSize, tc.netProfitPercent)
			assert.Equal(t, tc.expected, result, "The potential profit calculation should be correct")
		})
	}
}

func TestFindBestPrices(t *testing.T) {
	// Create mock exchanges
	exchange1 := &MockExchange{
		name: "Exchange1",
		orderBook: &exchange.OrderBook{
			Bids:      []exchange.Order{{Price: 105.0, Amount: 1.0, Exchange: "Exchange1"}},
			Asks:      []exchange.Order{{Price: 100.0, Amount: 1.0, Exchange: "Exchange1"}},
			Timestamp: time.Now(),
		},
	}

	exchange2 := &MockExchange{
		name: "Exchange2",
		orderBook: &exchange.OrderBook{
			Bids:      []exchange.Order{{Price: 106.0, Amount: 0.5, Exchange: "Exchange2"}},
			Asks:      []exchange.Order{{Price: 101.0, Amount: 0.5, Exchange: "Exchange2"}},
			Timestamp: time.Now(),
		},
	}

	exchange3 := &MockExchange{
		name: "Exchange3",
		orderBook: &exchange.OrderBook{
			Bids:      []exchange.Order{{Price: 104.0, Amount: 2.0, Exchange: "Exchange3"}},
			Asks:      []exchange.Order{{Price: 99.0, Amount: 2.0, Exchange: "Exchange3"}},
			Timestamp: time.Now(),
		},
	}

	exchanges := []exchange.Exchange{exchange1, exchange2, exchange3}

	t.Run("Find best cross-exchange prices", func(t *testing.T) {
		lowestAsk, highestBid, err := FindBestPrices(exchanges, "BTC", 1, 5*time.Minute)

		assert.NoError(t, err, "There should be no error when finding the best prices")
		assert.Equal(t, "Exchange3", lowestAsk.Exchange, "The lowest ask should come from Exchange3")
		assert.Equal(t, 99.0, lowestAsk.Price, "The lowest ask price should be 99.0")
		assert.Equal(t, "Exchange2", highestBid.Exchange, "The highest bid should come from Exchange2")
		assert.Equal(t, 106.0, highestBid.Price, "The highest bid price should be 106.0")
	})
}

func TestLogPositiveOpportunity(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "arbitrage_test")
	assert.NoError(t, err, "Should create a temporary directory")
	defer os.RemoveAll(tempDir)

	// Save the original directory and restore after the test
	originalLogDir := "logs"
	os.Setenv("LOG_DIR", tempDir)
	defer os.Setenv("LOG_DIR", originalLogDir)

	// Create a test opportunity
	opportunity := ArbitrageOpportunity{
		Symbol:          "BTC",
		BuyExchange:     "Exchange1",
		SellExchange:    "Exchange2",
		BuyPrice:        100.0,
		SellPrice:       105.0,
		Spread:          5.0,
		NetProfit:       4.8,
		MaxTradeSize:    1.0,
		PotentialProfit: 4.8,
		Timestamp:       time.Now(),
	}

	// Test the log
	err = LogPositiveOpportunity(opportunity)
	assert.NoError(t, err, "The log should be created without errors")

	// Check if the log file was created
	today := time.Now().Format("2006-01-02")
	logFile := filepath.Join("logs", fmt.Sprintf("positive_arbitrage_%s.log", today))
	_, err = os.Stat(logFile)
	assert.False(t, os.IsNotExist(err), "The log file should exist")
}

func TestFindArbitrageOpportunities(t *testing.T) {
	// Test configuration
	cfg := &config.Config{
		Coins:               []string{"BTC"},
		MinProfitPercentage: 0.5,
		MinProfitAmount:     1.0,
		TradeFees: map[string]float64{
			"Exchange1": 0.1,
			"Exchange2": 0.1,
			"Exchange3": 0.1,
		},
	}

	// Create mock exchanges
	exchange1 := &MockExchange{
		name: "Exchange1",
		orderBook: &exchange.OrderBook{
			Bids:      []exchange.Order{{Price: 105.0, Amount: 1.0, Exchange: "Exchange1"}},
			Asks:      []exchange.Order{{Price: 100.0, Amount: 1.0, Exchange: "Exchange1"}},
			Timestamp: time.Now(),
		},
	}

	exchange2 := &MockExchange{
		name: "Exchange2",
		orderBook: &exchange.OrderBook{
			Bids:      []exchange.Order{{Price: 106.0, Amount: 0.5, Exchange: "Exchange2"}},
			Asks:      []exchange.Order{{Price: 101.0, Amount: 0.5, Exchange: "Exchange2"}},
			Timestamp: time.Now(),
		},
	}

	exchange3 := &MockExchange{
		name: "Exchange3",
		orderBook: &exchange.OrderBook{
			Bids:      []exchange.Order{{Price: 108.0, Amount: 2.0, Exchange: "Exchange3"}},
			Asks:      []exchange.Order{{Price: 99.0, Amount: 2.0, Exchange: "Exchange3"}},
			Timestamp: time.Now(),
		},
	}

	exchanges := []exchange.Exchange{exchange1, exchange2, exchange3}

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "arbitrage_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Save the original directory and restore after the test
	originalLogDir := "logs"
	os.Setenv("LOG_DIR", tempDir)
	defer os.Setenv("LOG_DIR", originalLogDir)

	// Run the test
	opportunities, err := FindArbitrageOpportunities(exchanges, cfg)
	assert.NoError(t, err, "There should be no error when looking for opportunities")

	// Check if opportunities were found
	assert.Greater(t, len(opportunities), 0, "Arbitrage opportunities should be found")

	// Check if the opportunities are correct
	if len(opportunities) > 0 {
		// Check for an opportunity from Exchange3 (buy) to Exchange2 (sell)
		foundOpp := false
		for _, opp := range opportunities {
			if opp.BuyExchange == "Exchange3" && opp.SellExchange == "Exchange2" {
				foundOpp = true
				assert.Equal(t, "BTC", opp.Symbol)
				assert.Equal(t, 99.0, opp.BuyPrice)
				assert.Equal(t, 106.0, opp.SellPrice)
				assert.InDelta(t, 7.07, opp.Spread, 0.1)
				assert.Greater(t, opp.NetProfit, cfg.MinProfitPercentage)
				assert.Equal(t, 0.5, opp.MaxTradeSize)
				assert.Greater(t, opp.PotentialProfit, cfg.MinProfitAmount)
				break
			}
		}
		assert.True(t, foundOpp, "Should find an opportunity from Exchange3 to Exchange2")
	}
}
