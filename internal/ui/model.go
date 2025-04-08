package ui

import (
	"time"
)

// ChartMode represents the current view mode of the line chart
type ChartMode int

const (
	ModeAll ChartMode = iota
	ModeSingle
)

// CoinData represents a single arbitrage opportunity data point
type CoinData struct {
	Timestamp       time.Time
	Symbol          string
	BuyExchange     string
	SellExchange    string
	BuyPrice        float64
	SellPrice       float64
	Profit          float64
	Spread          float64
	MaxTradeSize    float64
	PotentialProfit float64
	LiquidProfit    float64
	Profitable      bool
	FlashUntil      time.Time
}
