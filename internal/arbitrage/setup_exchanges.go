package arbitrage

import (
	"context"

	"github.com/raykavin/ArbiTron/internal/config"
	"github.com/raykavin/ArbiTron/internal/ui"
	"github.com/raykavin/ArbiTron/pkg/exchange"
	"github.com/raykavin/ArbiTron/pkg/exchange/binance"
	"github.com/raykavin/ArbiTron/pkg/exchange/bybit"
	"github.com/raykavin/ArbiTron/pkg/exchange/hyperliquid"
	"github.com/raykavin/ArbiTron/pkg/exchange/kucoin"
	"github.com/raykavin/ArbiTron/pkg/exchange/okx"
	"github.com/raykavin/ArbiTron/pkg/http/websocket"
)

func SetupExchanges(ctx context.Context, cfg *config.Config, log ui.Logger) ([]exchange.Exchange, error) {
	type setupFn func(context.Context, *config.Config, ui.Logger) (exchange.Exchange, error)

	setups := []setupFn{
		setupHyperliquid,
		setupKuCoin,
		setupOKX,
		setupBybit,
		// setupBinance,
		// setupMEXC,
	}

	exchanges := make([]exchange.Exchange, len(setups))
	for i, setup := range setups {
		exch, err := setup(ctx, cfg, log)
		if err != nil {
			return nil, err
		}
		exchanges[i] = exch
	}

	return exchanges, nil
}

func newWebSocketClient() websocket.Client {
	return websocket.NewClient(
		websocket.WithSSL(true),
	)
}

func setupKuCoin(ctx context.Context, cfg *config.Config, log ui.Logger) (exchange.Exchange, error) {
	return kucoin.New(ctx, newWebSocketClient(), "", "", "", false, cfg.MaxStaleDuration, log)
}

func setupHyperliquid(ctx context.Context, cfg *config.Config, log ui.Logger) (exchange.Exchange, error) {
	return hyperliquid.New(ctx, cfg.UseMainnet, newWebSocketClient(), cfg.MaxStaleDuration, log)
}

func setupBinance(ctx context.Context, cfg *config.Config, log ui.Logger) (exchange.Exchange, error) {
	return binance.New(ctx, cfg.OrderBookDepth, cfg.QuotedAsset, newWebSocketClient(), cfg.MaxStaleDuration, log)
}

func setupOKX(ctx context.Context, cfg *config.Config, log ui.Logger) (exchange.Exchange, error) {
	return okx.New(ctx, newWebSocketClient(), cfg.MaxStaleDuration, cfg.QuotedAsset, log)
}

// func setupMEXC(ctx context.Context, cfg *config.Config, log ui.Logger) (exchange.Exchange, error) {
// 	return mexc.New(ctx, newWebSocketClient(), cfg.MaxStaleDuration, cfg.QuotedAsset, log)
// }

func setupBybit(ctx context.Context, cfg *config.Config, log ui.Logger) (exchange.Exchange, error) {
	return bybit.New(ctx, newWebSocketClient(), cfg.MaxStaleDuration, cfg.OrderBookDepth, cfg.QuotedAsset, log)
}
