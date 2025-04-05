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
	MinSpreadPercentage float64            `json:"min_spread_percentage"` // Minimum spread percentage
	MinProfitPercentage float64            `json:"min_profit_percentage"` // Minimum profit percentage after fees
	MinProfitAmount     float64            `json:"min_profit_amount"`     // Minimum profit in USD
	UseMainnet          bool               `json:"use_mainnet"`           // Use mainnet or testnet
	OrderBookDepth      int                `json:"order_book_depth"`      // Depth of order book to consider
	Coins               []string           `json:"coins"`                 // List of coins to monitor
	TradeFees           map[string]float64 `json:"trade_fees"`            // Fee by exchange
	CheckInterval       time.Duration      `json:"check_interval"`        // How often to check for arbitrage
	MaxStaleDuration    time.Duration      `json:"max_stale_duration"`    // Maximum age of order book data
	UpdateUIInterval    time.Duration      `json:"update_ui_interval"`    // Dashboard update interval
	InitialWaitTime     time.Duration      `json:"initial_wait_time"`     // Initial wait time after connecting
}

// DefaultConfig returns a default configuration with sensible values
func DefaultConfig() *Config {
	return &Config{
		Coins:               []string{"LINK", "ATOM", "BTC", "AVAX", "ADA"},
		MinSpreadPercentage: 0.5, // 0.5% minimum spread
		MinProfitPercentage: 0.3, // 0.3% minimum profit after fees
		MinProfitAmount:     5.0, // $5 minimum profit
		TradeFees: map[string]float64{
			"Hyperliquid": 0.1, // 0.1% fee
			"KuCoin":      0.1, // 0.1% fee
		},
		CheckInterval:    100 * time.Millisecond,
		MaxStaleDuration: 500 * time.Millisecond,
		UpdateUIInterval: 50 * time.Millisecond,
		InitialWaitTime:  2 * time.Second,
		UseMainnet:       true,
		OrderBookDepth:   5,
	}
}

// LoadFromFile loads configuration from a JSON file
// If the file doesn't exist, creates a default config file
func LoadFromFile(filePath string) (*Config, error) {
	// Start with default config
	config := DefaultConfig()

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return createDefaultConfigFile(config, filePath)
	}

	return loadExistingConfigFile(config, filePath)
}

// createDefaultConfigFile creates a default config file if none exists
func createDefaultConfigFile(config *Config, filePath string) (*Config, error) {
	if err := config.SaveToFile(filePath); err != nil {
		return nil, fmt.Errorf("failed to create default config: %w", err)
	}
	return config, nil
}

// loadExistingConfigFile loads and parses an existing config file
func loadExistingConfigFile(config *Config, filePath string) (*Config, error) {
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

// GetFee returns the trading fee for a specific exchange
func (c *Config) GetFee(exchangeName string) float64 {
	if fee, exists := c.TradeFees[exchangeName]; exists {
		return fee
	}

	return 0.1
}

// Validate checks if the config has valid values
func (c *Config) Validate() error {
	if len(c.Coins) == 0 {
		return fmt.Errorf("no coins specified in configuration")
	}

	if c.OrderBookDepth <= 0 {
		return fmt.Errorf("order book depth must be greater than 0")
	}

	if c.MaxStaleDuration <= 0 {
		return fmt.Errorf("max stale duration must be greater than 0")
	}

	return nil
}
