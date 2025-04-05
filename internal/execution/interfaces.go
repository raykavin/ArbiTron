package execution

import "context"

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
