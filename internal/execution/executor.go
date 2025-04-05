// internal/execution/executor.go
package execution

import (
	"context"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/raykavin/ArbiTron/internal/arbitrage"
	"github.com/raykavin/ArbiTron/internal/config"
)

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

// ArbitrageExecutor handles the execution of arbitrage opportunities
type ArbitrageExecutor struct {
	exchangeClients map[string]ExchangeClient
	config          *config.Config
	mu              sync.RWMutex
	executing       map[string]bool // Track which symbols are currently being executed
	profitHistory   []ProfitRecord
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
	if err := ae.lockSymbolForExecution(opp.Symbol); err != nil {
		return err
	}
	defer ae.unlockSymbolExecution(opp.Symbol)

	// Get exchange clients
	buyClient, sellClient, err := ae.getExchangeClients(opp)
	if err != nil {
		return err
	}

	// Calculate trade amount based on balances and constraints
	maxAmount, err := ae.calculateTradeAmount(ctx, opp, buyClient)
	if err != nil {
		return err
	}

	log.Printf("Executing arbitrage for %s: Buy %.6f on %s at $%.2f, Sell on %s at $%.2f",
		opp.Symbol, maxAmount, opp.BuyExchange, opp.BuyPrice, opp.SellExchange, opp.SellPrice)

	// Execute the buy order
	actualBalance, err := ae.executeBuyOrder(ctx, opp, buyClient, maxAmount)
	if err != nil {
		return err
	}

	// Execute the sell order
	err = ae.executeSellOrder(ctx, opp, sellClient, actualBalance)
	if err != nil {
		return err
	}

	// Calculate and record profit
	expectedProfit := ae.calculateExpectedProfit(opp, actualBalance, maxAmount)
	ae.recordProfit(opp, actualBalance, expectedProfit)

	log.Printf("Arbitrage execution complete for %s. Expected profit: $%.2f", opp.Symbol, expectedProfit)
	return nil
}

// lockSymbolForExecution marks a symbol as being executed to prevent concurrent execution
func (ae *ArbitrageExecutor) lockSymbolForExecution(symbol string) error {
	ae.mu.Lock()
	defer ae.mu.Unlock()

	if ae.executing[symbol] {
		return fmt.Errorf("already executing arbitrage for %s", symbol)
	}
	ae.executing[symbol] = true
	return nil
}

// unlockSymbolExecution marks a symbol as no longer being executed
func (ae *ArbitrageExecutor) unlockSymbolExecution(symbol string) {
	ae.mu.Lock()
	ae.executing[symbol] = false
	ae.mu.Unlock()
}

// getExchangeClients retrieves the necessary exchange clients for execution
func (ae *ArbitrageExecutor) getExchangeClients(opp arbitrage.ArbitrageOpportunity) (ExchangeClient, ExchangeClient, error) {
	buyClient, ok := ae.exchangeClients[opp.BuyExchange]
	if !ok {
		return nil, nil, fmt.Errorf("buy exchange client not found: %s", opp.BuyExchange)
	}

	sellClient, ok := ae.exchangeClients[opp.SellExchange]
	if !ok {
		return nil, nil, fmt.Errorf("sell exchange client not found: %s", opp.SellExchange)
	}

	return buyClient, sellClient, nil
}

// calculateTradeAmount determines the appropriate amount to trade based on balances and constraints
func (ae *ArbitrageExecutor) calculateTradeAmount(
	ctx context.Context,
	opp arbitrage.ArbitrageOpportunity,
	buyClient ExchangeClient,
) (float64, error) {
	quoteAsset := "USDT"

	// Check if we have enough USDT on buy exchange
	buyBalance, err := buyClient.GetBalance(ctx, quoteAsset)
	if err != nil {
		return 0, fmt.Errorf("failed to get balance on %s: %w", opp.BuyExchange, err)
	}

	// Calculate amount to buy (considering maxTradeSize and available balance)
	maxAmount := math.Min(opp.MaxTradeSize, buyBalance/opp.BuyPrice)

	// Don't proceed if amount is too small
	if maxAmount*opp.BuyPrice < ae.config.MinProfitAmount*2 {
		return 0, fmt.Errorf("available trading amount too small: $%.2f", maxAmount*opp.BuyPrice)
	}

	return maxAmount, nil
}

// executeBuyOrder places a buy order and waits for it to complete
func (ae *ArbitrageExecutor) executeBuyOrder(
	ctx context.Context,
	opp arbitrage.ArbitrageOpportunity,
	buyClient ExchangeClient,
	amount float64,
) (float64, error) {
	// Place buy order
	buyOrderID, err := buyClient.PlaceMarketBuyOrder(ctx, opp.Symbol+"USDT", amount)
	if err != nil {
		return 0, fmt.Errorf("failed to place buy order: %w", err)
	}

	// Wait for buy order to complete
	if err := ae.waitForOrderCompletion(ctx, buyClient, buyOrderID, "buy"); err != nil {
		return 0, err
	}

	// Get actual balance after buy (might be less due to fees)
	actualBalance, err := buyClient.GetBalance(ctx, opp.Symbol)
	if err != nil {
		return 0, fmt.Errorf("failed to get actual balance after buy: %w", err)
	}

	return actualBalance, nil
}

// executeSellOrder places a sell order and waits for it to complete
func (ae *ArbitrageExecutor) executeSellOrder(
	ctx context.Context,
	opp arbitrage.ArbitrageOpportunity,
	sellClient ExchangeClient,
	amount float64,
) error {
	// Place sell order
	sellOrderID, err := sellClient.PlaceMarketSellOrder(ctx, opp.Symbol+"USDT", amount)
	if err != nil {
		return fmt.Errorf("failed to place sell order: %w", err)
	}

	// Wait for sell order to complete
	return ae.waitForOrderCompletion(ctx, sellClient, sellOrderID, "sell")
}

// waitForOrderCompletion polls for order status until it completes or times out
func (ae *ArbitrageExecutor) waitForOrderCompletion(
	ctx context.Context,
	client ExchangeClient,
	orderID string,
	orderType string,
) error {
	for i := range 10 {
		status, err := client.GetOrderStatus(ctx, orderID)
		if err != nil {
			log.Printf("Error checking %s order status: %v", orderType, err)
		} else if status == "FILLED" {
			return nil
		}

		// If last attempt and still not filled
		if i == 9 {
			return fmt.Errorf("%s order not filled in time", orderType)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
			// Continue waiting
		}
	}
	return nil
}

// calculateExpectedProfit calculates the expected profit based on trading parameters
func (ae *ArbitrageExecutor) calculateExpectedProfit(
	opp arbitrage.ArbitrageOpportunity,
	actualBalance float64,
	buyAmount float64,
) float64 {
	buyFee := ae.config.GetFee(opp.BuyExchange)
	sellFee := ae.config.GetFee(opp.SellExchange)

	// Calculate expected profit including fees
	return actualBalance*opp.SellPrice*(1-sellFee/100) - buyAmount*opp.BuyPrice*(1+buyFee/100)
}

// recordProfit records profit information in the history
func (ae *ArbitrageExecutor) recordProfit(
	opp arbitrage.ArbitrageOpportunity,
	actualBalance float64,
	expectedProfit float64,
) {
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
