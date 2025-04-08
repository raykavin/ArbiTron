// internal/exchange/exchange.go
package exchange

import (
	"time"
)

type Exchange interface {
	GetName() string
	SubscribeToOrderBook(symbol string) error
	GetOrderBook(symbol string) (*OrderBook, error)
}

type OrderBook struct {
	Bids      []Order
	Asks      []Order
	Timestamp time.Time
}

type Order struct {
	Exchange string
	Price    float64
	Amount   float64
}
