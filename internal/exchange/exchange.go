// internal/exchange/exchange.go
package exchange

import (
	"context"
	"time"
)

type Exchange interface {
	Connect(ctx context.Context) error
	SubscribeToOrderBook(symbol string) error
	GetOrderBook(symbol string) (*OrderBook, error)
	GetName() string
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
