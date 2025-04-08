// internal/config/config.go
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds the application configuration
type Config struct {
	MinSpreadPercentage float64            `yaml:"min_spread_percentage"` // Minimum spread percentage
	MinProfitPercentage float64            `yaml:"min_profit_percentage"` // Minimum profit percentage after fees
	MinProfitAmount     float64            `yaml:"min_profit_amount"`     // Minimum profit in USD
	UseMainnet          bool               `yaml:"use_mainnet"`           // Use mainnet or testnet
	OrderBookDepth      int                `yaml:"order_book_depth"`      // Depth of order book to consider
	QuotedAsset         string             `yaml:"quoted_asset"`
	Coins               []string           `yaml:"coins"`              // List of coins to monitor
	TradeFees           map[string]float64 `yaml:"trade_fees"`         // Fee by exchange
	CheckInterval       time.Duration      `yaml:"check_interval"`     // How often to check for arbitrage
	MaxStaleDuration    time.Duration      `yaml:"max_stale_duration"` // Maximum age of order book data
	UpdateUIInterval    time.Duration      `yaml:"update_ui_interval"` // Dashboard update interval
}

// LoadFromFile loads configuration from a YAML file
// If the file doesn't exist, creates a default config file
func LoadFromFile(filePath string) (*Config, error) {
	config := &Config{}

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil, err
	}

	return loadExistingConfigFile(config, filePath)
}

// loadExistingConfigFile loads and parses an existing config file
func loadExistingConfigFile(config *Config, filePath string) (*Config, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return config, nil
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
