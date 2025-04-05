// internal/execution/executor.go
package execution

import (
	"context"
	"fmt"
	"log"
	"math"
	"notlelouch/ArbiBot/internal/arbitrage"
	"notlelouch/ArbiBot/internal/config"
	"sync"
	"time"
)

// TradeExecutor defines the interface for executing trades on exchanges
type TradeExecutor interface {
	PlaceMarketBuyOrder(ctx context.Context, symbol string, amount float64) (string, error)
	PlaceMarketSellOrder(ctx context.Context, symbol string, amount float64) (string, error)
	GetBalance(ctx context.Context, asset string) (float64, error)
	GetOrderStatus(ctx context.Context, orderID string) (string, error)
}

// ExchangeClient combines the order book data with trading capabilities
type ExchangeClient interface {
	TradeExecutor
	GetName() string
	Connect(ctx context.Context) error
	Close() error
}

// ArbitrageExecutor handles the execution of arbitrage opportunities
type ArbitrageExecutor struct {
	exchangeClients map[string]ExchangeClient
	config          *config.Config
	mu              sync.RWMutex
	executing       map[string]bool // Track which symbols are currently being executed
	profitHistory   []ProfitRecord
}

// ProfitRecord tracks the results of executed arbitrage
type ProfitRecord struct {
	Timestamp      time.Time
	Symbol         string
	BuyExchange    string
	SellExchange   string
	AmountBought   float64
	BuyPrice       float64
	SellPrice      float64
	ExpectedProfit float64
	ActualProfit   float64
	Status         string
}

// NewArbitrageExecutor creates a new arbitrage executor
func NewArbitrageExecutor(clients map[string]ExchangeClient, cfg *config.Config) *ArbitrageExecutor {
	return &ArbitrageExecutor{
		exchangeClients: clients,
		config:          cfg,
		executing:       make(map[string]bool),
		profitHistory:   make([]ProfitRecord, 0),
	}
}

// IsExecuting checks if arbitrage is already being executed for a symbol
func (ae *ArbitrageExecutor) IsExecuting(symbol string) bool {
	ae.mu.RLock()
	defer ae.mu.RUnlock()
	return ae.executing[symbol]
}

// ExecuteArbitrage executes an arbitrage opportunity
func (ae *ArbitrageExecutor) ExecuteArbitrage(ctx context.Context, opp arbitrage.ArbitrageOpportunity) error {
	// Prevent multiple executions for the same symbol
	ae.mu.Lock()
	if ae.executing[opp.Symbol] {
		ae.mu.Unlock()
		return fmt.Errorf("already executing arbitrage for %s", opp.Symbol)
	}
	ae.executing[opp.Symbol] = true
	ae.mu.Unlock()

	// Make sure to mark as not executing when done
	defer func() {
		ae.mu.Lock()
		ae.executing[opp.Symbol] = false
		ae.mu.Unlock()
	}()

	// Get exchange clients
	buyClient, ok := ae.exchangeClients[opp.BuyExchange]
	if !ok {
		return fmt.Errorf("buy exchange client not found: %s", opp.BuyExchange)
	}

	sellClient, ok := ae.exchangeClients[opp.SellExchange]
	if !ok {
		return fmt.Errorf("sell exchange client not found: %s", opp.SellExchange)
	}

	// Check balances
	baseAsset := opp.Symbol
	quoteAsset := "USDT"

	// Check if we have enough USDT on buy exchange
	buyBalance, err := buyClient.GetBalance(ctx, quoteAsset)
	if err != nil {
		return fmt.Errorf("failed to get balance on %s: %w", opp.BuyExchange, err)
	}

	// Calculate amount to buy (considering maxTradeSize and available balance)
	maxAmount := math.Min(opp.MaxTradeSize, buyBalance/opp.BuyPrice)

	// Don't proceed if amount is too small
	if maxAmount*opp.BuyPrice < ae.config.MinProfitUSD*2 {
		return fmt.Errorf("available trading amount too small: $%.2f", maxAmount*opp.BuyPrice)
	}

	log.Printf("Executing arbitrage for %s: Buy %.6f on %s at $%.2f, Sell on %s at $%.2f",
		opp.Symbol, maxAmount, opp.BuyExchange, opp.BuyPrice, opp.SellExchange, opp.SellPrice)

	// Place buy order
	buyOrderID, err := buyClient.PlaceMarketBuyOrder(ctx, opp.Symbol+quoteAsset, maxAmount)
	if err != nil {
		return fmt.Errorf("failed to place buy order: %w", err)
	}

	// Wait for buy order to complete
	for i := 0; i < 10; i++ {
		status, err := buyClient.GetOrderStatus(ctx, buyOrderID)
		if err != nil {
			log.Printf("Error checking buy order status: %v", err)
		} else if status == "FILLED" {
			break
		}

		// If last attempt and still not filled
		if i == 9 {
			return fmt.Errorf("buy order not filled in time")
		}

		time.Sleep(500 * time.Millisecond)
	}

	// Get actual balance after buy (might be less due to fees)
	actualBalance, err := buyClient.GetBalance(ctx, baseAsset)
	if err != nil {
		return fmt.Errorf("failed to get actual balance after buy: %w", err)
	}

	// Place sell order
	sellOrderID, err := sellClient.PlaceMarketSellOrder(ctx, opp.Symbol+quoteAsset, actualBalance)
	if err != nil {
		return fmt.Errorf("failed to place sell order: %w", err)
	}

	// Wait for sell order to complete
	for i := 0; i < 10; i++ {
		status, err := sellClient.GetOrderStatus(ctx, sellOrderID)
		if err != nil {
			log.Printf("Error checking sell order status: %v", err)
		} else if status == "FILLED" {
			break
		}

		// If last attempt and still not filled
		if i == 9 {
			return fmt.Errorf("sell order not filled in time")
		}

		time.Sleep(500 * time.Millisecond)
	}

	// Calculate expected vs actual profit
	expectedProfit := actualBalance*opp.SellPrice*(1-ae.config.TradeFees[opp.SellExchange]/100) -
		maxAmount*opp.BuyPrice*(1+ae.config.TradeFees[opp.BuyExchange]/100)

	// Get final USDT balance on sell exchange
	// finalBalance, err := sellClient.GetBalance(ctx, quoteAsset)
	_, err = sellClient.GetBalance(ctx, quoteAsset)
	if err != nil {
		log.Printf("Failed to get final balance: %v", err)
	}

	// Record the profit
	record := ProfitRecord{
		Timestamp:      time.Now(),
		Symbol:         opp.Symbol,
		BuyExchange:    opp.BuyExchange,
		SellExchange:   opp.SellExchange,
		AmountBought:   actualBalance,
		BuyPrice:       opp.BuyPrice,
		SellPrice:      opp.SellPrice,
		ExpectedProfit: expectedProfit,
		ActualProfit:   0, // Will need to calculate based on before/after balance
		Status:         "COMPLETED",
	}

	ae.mu.Lock()
	ae.profitHistory = append(ae.profitHistory, record)
	ae.mu.Unlock()

	log.Printf("Arbitrage execution complete for %s. Expected profit: $%.2f", opp.Symbol, expectedProfit)
	return nil
}

// GetProfitHistory returns the history of executed arbitrage opportunities
func (ae *ArbitrageExecutor) GetProfitHistory() []ProfitRecord {
	ae.mu.RLock()
	defer ae.mu.RUnlock()

	// Return a copy to avoid race conditions
	history := make([]ProfitRecord, len(ae.profitHistory))
	copy(history, ae.profitHistory)
	return history
}
