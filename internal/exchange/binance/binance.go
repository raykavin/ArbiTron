// internal/exchange/binance/binance.go
package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/raykavin/ArbiTron/internal/exchange"
)

const (
	binanceWSURL     = "wss://stream.binance.com:9443/ws"
	staleDuration    = 2 * time.Second
	depthUpdateEvent = "depthUpdate"
)

type BinanceWS struct {
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	orderBooks map[string]*exchange.OrderBook
	mu         sync.RWMutex
}

type wsDepthResponse struct {
	EventType string     `json:"e"`
	Symbol    string     `json:"s"`
	Bids      [][]string `json:"b"`
	Asks      [][]string `json:"a"`
	EventTime int64      `json:"E"`
}

func NewBinanceWS() *BinanceWS {
	ctx, cancel := context.WithCancel(context.Background())
	return &BinanceWS{
		ctx:        ctx,
		cancel:     cancel,
		orderBooks: make(map[string]*exchange.OrderBook),
	}
}

func (b *BinanceWS) GetName() string {
	return "Binance"
}

func (b *BinanceWS) Connect(ctx context.Context) error {
	// conexão por par, então Connect não precisa fazer nada
	return nil
}

func (b *BinanceWS) SubscribeToOrderBook(symbol string) error {
	stream := strings.ToLower(symbol) + "@depth10@100ms"
	url := fmt.Sprintf("%s/%s", binanceWSURL, stream)

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to Binance WS: %w", err)
	}

	b.conn = conn
	go b.handleMessages(symbol)

	return nil
}

func (b *BinanceWS) GetOrderBook(symbol string) (*exchange.OrderBook, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	ob, ok := b.orderBooks[symbol]
	if !ok {
		return nil, fmt.Errorf("no order book for %s", symbol)
	}

	if time.Since(ob.Timestamp) > staleDuration {
		return nil, fmt.Errorf("stale order book for %s", symbol)
	}

	return ob, nil
}

func (b *BinanceWS) Close() {
	b.cancel()
	if b.conn != nil {
		b.conn.Close()
	}
}

func (b *BinanceWS) handleMessages(symbol string) {
	for {
		select {
		case <-b.ctx.Done():
			return
		default:
			_, msg, err := b.conn.ReadMessage()
			if err != nil {
				log.Printf("Binance WS read error: %v", err)
				return
			}

			var resp wsDepthResponse
			if err := json.Unmarshal(msg, &resp); err != nil {
				log.Printf("Binance WS unmarshal error: %v", err)
				continue
			}

			b.mu.Lock()

			book := &exchange.OrderBook{
				Bids:      parseLevels(resp.Bids, "Binance"),
				Asks:      parseLevels(resp.Asks, "Binance"),
				Timestamp: time.Now(),
			}

			b.orderBooks[symbol] = book
			b.mu.Unlock()
		}
	}
}

func parseLevels(raw [][]string, exchangeName string) []exchange.Order {
	orders := make([]exchange.Order, 0, len(raw))
	for _, level := range raw {
		price, err1 := strconv.ParseFloat(level[0], 64)
		amount, err2 := strconv.ParseFloat(level[1], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		orders = append(orders, exchange.Order{
			Exchange: exchangeName,
			Price:    price,
			Amount:   amount,
		})
	}
	return orders
}
