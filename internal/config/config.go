// internal/config/config.go
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Config holds the application configuration
type Config struct {
	Coins            []string           `json:"coins"`              // List of coins to monitor
	ProfitThreshold  float64            `json:"profit_threshold"`   // Minimum profit percentage
	MinProfitUSD     float64            `json:"min_profit_usd"`     // Minimum profit in USD
	TradeFees        map[string]float64 `json:"trade_fees"`         // Fee by exchange
	CheckInterval    time.Duration      `json:"check_interval"`     // How often to check for arbitrage
	UseMainnet       bool               `json:"use_mainnet"`        // Use mainnet or testnet
	OrderBookDepth   int                `json:"order_book_depth"`   // Depth of order book to consider
	MaxStaleDuration time.Duration      `json:"max_stale_duration"` // Maximum age of order book data
	UpdateUIInterval time.Duration      `json:"update_ui_interval"` // Dashboard update interval
	InitialWaitTime  time.Duration      `json:"initial_wait_time"`  // Initial wait time after connecting
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		Coins:           []string{"LINK", "ATOM", "BTC", "AVAX", "ADA"},
		ProfitThreshold: 1.0, // 1% minimum profit
		MinProfitUSD:    5.0, // $5 minimum profit
		TradeFees: map[string]float64{
			"Hyperliquid": 0.1, // 0.1% fee
			"KuCoin":      0.1, // 0.1% fee
		},
		CheckInterval:    400 * time.Millisecond,
		UseMainnet:       true,
		OrderBookDepth:   5,
		MaxStaleDuration: 2 * time.Second,
		UpdateUIInterval: 250 * time.Millisecond,
		InitialWaitTime:  2 * time.Second,
	}
}

// LoadFromFile loads configuration from a JSON file
func LoadFromFile(filePath string) (*Config, error) {
	// Start with default config
	config := DefaultConfig()

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		// Create default config file
		if err := config.SaveToFile(filePath); err != nil {
			return nil, fmt.Errorf("failed to create default config: %w", err)
		}
		return config, nil
	}

	// Read and parse file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return config, nil
}

// SaveToFile saves the configuration to a JSON file
func (c *Config) SaveToFile(filePath string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return os.WriteFile(filePath, data, 0644)
}
