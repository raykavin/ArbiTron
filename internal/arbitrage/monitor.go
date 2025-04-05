package arbitrage

import (
	"context"
	"fmt"
	"log"
	"math"
	"notlelouch/ArbiBot/internal/config"
	"notlelouch/ArbiBot/internal/exchange"
	"notlelouch/ArbiBot/internal/exchange/hyperliquid"
	"notlelouch/ArbiBot/internal/exchange/kucoin"
	"notlelouch/ArbiBot/internal/ui"
	"runtime"
	"sync"
	"time"
)

// ArbitrageMonitor handles the monitoring of arbitrage opportunities
type ArbitrageMonitor struct {
	exchangeClients []exchange.Exchange
	dashboard       *ui.ArbitrageDashboard
	config          *config.Config
}

// NewArbitrageMonitor creates and initializes a new ArbitrageMonitor
func NewArbitrageMonitor(dashboard *ui.ArbitrageDashboard, cfg *config.Config) (*ArbitrageMonitor, error) {
	// Get public token for KuCoin
	tokenResp, err := kucoin.GetToken("", "", "", false)
	if err != nil {
		return nil, err
	}

	hyperliquidClient := hyperliquid.NewHyperliquidWS(cfg.UseMainnet)
	kucoinClient := kucoin.NewKuCoinWS(tokenResp)

	return &ArbitrageMonitor{
		dashboard:       dashboard,
		config:          cfg,
		exchangeClients: []exchange.Exchange{hyperliquidClient, kucoinClient},
	}, nil
}

// Connect establishes connections with all exchanges and ensures they are operational
func (am *ArbitrageMonitor) Connect(ctx context.Context) error {
	log.Println("Starting connections with exchanges...")

	for i, client := range am.exchangeClients {
		exchangeName := "unknown"
		if i == 0 {
			exchangeName = "Hyperliquid"
		} else if i == 1 {
			exchangeName = "KuCoin"
		}

		log.Printf("Connecting to %s...", exchangeName)
		if err := client.Connect(ctx); err != nil {
			return fmt.Errorf("failed to connect to %s: %w", exchangeName, err)
		}
		log.Printf("Successfully connected to %s", exchangeName)
	}

	// Subscribe to coins before starting monitoring
	for _, coin := range am.config.Coins {
		for i, client := range am.exchangeClients {
			exchangeName := "unknown"
			if i == 0 {
				exchangeName = "Hyperliquid"
			} else if i == 1 {
				exchangeName = "KuCoin"
			}

			log.Printf("Subscribing to %s on %s...", coin, exchangeName)
			if err := client.SubscribeToOrderBook(coin); err != nil {
				log.Printf("Error subscribing to %s on %s: %v",
					coin, exchangeName, err)
				continue
			}
		}
	}

	log.Println("Waiting for initial order book updates...")

	// Longer wait time to allow websocket connections
	// to start receiving data before monitoring begins
	time.Sleep(am.config.InitialWaitTime)

	// Verify if we can get any data before proceeding
	opportunities, err := FindArbitrageOpportunities(am.exchangeClients, am.config)
	if err != nil {
		log.Printf("Error finding initial arbitrage opportunities: %v", err)
	}

	if len(opportunities) > 0 {
		log.Printf("Found %d initial arbitrage opportunities", len(opportunities))
		for _, opp := range opportunities {
			log.Printf("Initial opportunity for %s: Buy at %.8f (%s), Sell at %.8f (%s), Profit: %.2f%%, Potential: $%.2f",
				opp.Symbol, opp.BuyPrice, opp.BuyExchange, opp.SellPrice, opp.SellExchange, opp.NetProfit, opp.PotentialProfit)
		}
	} else {
		log.Println("No initial arbitrage opportunities found")
	}

	// Para garantir que temos dados para todas as moedas, verificamos individualmente
	for _, coin := range am.config.Coins {
		lowestAsk, highestBid, err := FindBestPrices(
			am.exchangeClients,
			coin,
			am.config.OrderBookDepth,
			am.config.MaxStaleDuration,
		)

		if err == nil && lowestAsk.Price > 0 && highestBid.Price > 0 {
			log.Printf("Initial data received for %s: Best ask: %.8f (%s), Best bid: %.8f (%s)",
				coin, lowestAsk.Price, lowestAsk.Exchange, highestBid.Price, highestBid.Exchange)
		} else {
			log.Printf("Warning: No initial data yet for %s: %v", coin, err)
		}
	}

	return nil
}

// Start begins monitoring for arbitrage opportunities
func (am *ArbitrageMonitor) Start(ctx context.Context) {
	var wg sync.WaitGroup

	// Create worker pool for processing arbitrage opportunities
	arbOpportunities := make(chan string, len(am.config.Coins))

	// Reduce the number of workers to avoid overload
	workers := max(runtime.GOMAXPROCS(0)/2, 1)

	// Start workers
	for range workers {
		wg.Add(1)
		go am.processArbitrageWorker(ctx, arbOpportunities, &wg)
	}

	// Start producer goroutine
	go am.produceArbitrageOpportunities(ctx, arbOpportunities)

	wg.Wait()
}

// processArbitrageWorker handles processing of arbitrage opportunities
func (am *ArbitrageMonitor) processArbitrageWorker(ctx context.Context, arbOpportunities <-chan string, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case coin, ok := <-arbOpportunities:
			if !ok {
				return // channel closed
			}
			am.checkArbitrageOpportunity(coin)
			// Brief pause between processing
			time.Sleep(50 * time.Millisecond)
		case <-ctx.Done():
			return
		}
	}
}

// produceArbitrageOpportunities generates arbitrage opportunities to check
func (am *ArbitrageMonitor) produceArbitrageOpportunities(ctx context.Context, arbOpportunities chan<- string) {
	// Use a longer interval to reduce load
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	defer close(arbOpportunities)

	for {
		select {
		case <-ticker.C:
			for _, coin := range am.config.Coins {
				select {
				case arbOpportunities <- coin:
					// successfully sent
				default:
					// channel full, skip to avoid blocking
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

// checkArbitrageOpportunity looks for arbitrage opportunities for a symbol
func (am *ArbitrageMonitor) checkArbitrageOpportunity(symbol string) {
	// Primeiro, pegamos os preços básicos para garantir que temos dados para a UI
	lowestAsk, highestBid, err := FindBestPrices(
		am.exchangeClients,
		symbol,
		am.config.OrderBookDepth,
		am.config.MaxStaleDuration,
	)

	if err != nil {
		log.Printf("Error finding best prices for %s: %v\n", symbol, err)
		return
	}

	// Calcular o spread básico (pode ser negativo se não houver oportunidade)
	spread := 0.0
	if lowestAsk.Price > 0 {
		spread = (highestBid.Price - lowestAsk.Price) / lowestAsk.Price * 100
	}

	maxTradeSize := math.Min(lowestAsk.Amount, highestBid.Amount)

	// Valores padrão
	netProfit := 0.0
	potentialProfit := 0.0
	liquidProfit := 0.0

	// Criar uma configuração temporária apenas para este símbolo específico
	tempConfig := *am.config
	tempConfig.Coins = []string{symbol}

	// Agora tentamos encontrar oportunidades de arbitragem usando FindArbitrageOpportunities
	opportunities, _ := FindArbitrageOpportunities(am.exchangeClients, &tempConfig)

	// Se encontramos oportunidades, usamos os dados dela (que já tem todos os cálculos feitos)
	if len(opportunities) > 0 {
		opp := opportunities[0]

		// Atualizamos os valores com os dados da oportunidade
		lowestAsk.Exchange = opp.BuyExchange
		lowestAsk.Price = opp.BuyPrice
		highestBid.Exchange = opp.SellExchange
		highestBid.Price = opp.SellPrice
		spread = opp.Spread
		netProfit = opp.NetProfit
		maxTradeSize = opp.MaxTradeSize
		potentialProfit = opp.PotentialProfit

		// Calcular o liquid profit (específico desta função)
		if spread != 0 {
			liquidProfit = netProfit / spread
		}
	}

	// Sempre enviar dados para o dashboard
	am.dashboard.SendCoinData(ui.CoinData{
		Symbol:          symbol,
		BuyExchange:     lowestAsk.Exchange,
		BuyPrice:        lowestAsk.Price,
		SellExchange:    highestBid.Exchange,
		SellPrice:       highestBid.Price,
		Profit:          netProfit,
		Spread:          spread,
		LiquidProfit:    liquidProfit,
		MaxTradeSize:    maxTradeSize,
		PotentialProfit: potentialProfit,
		Timestamp:       time.Now(),
	})
}
