package bybit

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/raykavin/ArbiTron/internal/ui"
	"github.com/raykavin/ArbiTron/pkg/exchange"
	"github.com/raykavin/ArbiTron/pkg/http/websocket"
)

const (
	// WebSocket URLs
	mainWSURL = "wss://stream.bybit.com/v5/public/spot"
)

// SubscriptionMessage represents the WebSocket subscription request
type SubscriptionMessage struct {
	Op    string   `json:"op"`
	Args  []string `json:"args"`
	ReqID string   `json:"req_id,omitempty"`
}

// WsResponse represents the WebSocket response
type WsResponse struct {
	Topic   string          `json:"topic"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data"`
	Ts      int64           `json:"ts"`
	Success bool            `json:"success,omitempty"`
}

// OrderBookUpdate represents the order book data structure
type OrderBookUpdate struct {
	Symbol    string      `json:"s"`
	Timestamp int64       `json:"t"`
	Bids      [][2]string `json:"b"`
	Asks      [][2]string `json:"a"`
}

// OrderBookData stores an order book with its timestamp
type OrderBookData struct {
	Book exchange.OrderBook
}

// BybitExchange handles WebSocket communication with Bybit
type BybitExchange struct {
	socket       websocket.Client
	ctx          context.Context
	quotedAsset  string
	depth        int
	orderBooks   map[string]*OrderBookData
	maxStaleData time.Duration
	mu           sync.RWMutex
	logger       ui.Logger
	reqID        int
}

// New creates a new Bybit WebSocket client
func New(ctx context.Context, websocketClient websocket.Client, maxStaleData time.Duration, depth int, quotedAsset string, logger ui.Logger) (
	*BybitExchange,
	error,
) {
	bybit := &BybitExchange{
		ctx:          ctx,
		socket:       websocketClient,
		quotedAsset:  quotedAsset,
		depth:        depth,
		logger:       logger,
		maxStaleData: maxStaleData,
		orderBooks:   make(map[string]*OrderBookData),
		reqID:        1,
	}

	return bybit, bybit.connect()
}

func (b *BybitExchange) GetName() string {
	return "Bybit"
}

// Close closes the WebSocket connection and stops all goroutines
func (b *BybitExchange) Close() {
	if b.socket != nil && b.socket.IsConnected() {
		b.socket.Close()
	}
}

// SubscribeToOrderBook subscribes to order book updates for a specific symbol
func (b *BybitExchange) SubscribeToOrderBook(symbol string) error {
	if b.socket == nil || !b.socket.IsConnected() {
		return fmt.Errorf("websocket not connected")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	channelName := fmt.Sprintf("orderbook.%d.%s%s", b.depth, symbol, b.quotedAsset)

	subscription := SubscriptionMessage{
		Op:    "subscribe",
		Args:  []string{channelName},
		ReqID: fmt.Sprintf("id%d", b.reqID),
	}
	b.reqID++

	// Initialize the order book for this symbol
	b.orderBooks[symbol] = &OrderBookData{
		Book: exchange.OrderBook{},
	}

	subscriptionJSON, err := json.Marshal(subscription)
	if err != nil {
		return fmt.Errorf("error marshaling subscription: %w", err)
	}

	return b.socket.SendText(string(subscriptionJSON))
}

// GetOrderBook retrieves the current order book for a symbol
func (b *BybitExchange) GetOrderBook(symbol string) (*exchange.OrderBook, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	orderBookData, exists := b.orderBooks[symbol]
	if !exists {
		return nil, fmt.Errorf("no order book for %s", symbol)
	}

	// Check if data is stale
	// if time.Since(orderBookData.Book.Timestamp) > b.maxStaleData {
	// 	return nil, fmt.Errorf("stale order book for %s (last updated %s ago)",
	// 		symbol, time.Since(orderBookData.Book.Timestamp).String())
	// }

	return &orderBookData.Book, nil
}

// connect establishes a WebSocket connection to Bybit
func (b *BybitExchange) connect() error {
	// Set up event handlers
	b.setupEventHandlers()

	// Establish connection
	var connErr error
	b.socket.On(websocket.EventConnectError, func(event websocket.Event) {
		if err, ok := event.Data.(error); ok && err != nil {
			connErr = err
			return
		}

		connErr = fmt.Errorf("establish connection with bybit unknown error")
	})

	b.socket.Connect(mainWSURL)

	return connErr
}

// setupEventHandlers configures all WebSocket event handlers
func (b *BybitExchange) setupEventHandlers() {
	// Handle connection events
	b.socket.On(websocket.EventConnected, func(event websocket.Event) {
		b.logger.Infof("Connected to Bybit WebSocket")
	})

	b.socket.On(websocket.EventConnectError, func(event websocket.Event) {
		err := event.Data.(error)
		b.logger.Errorf("Failed to connect to Bybit WebSocket: %v", err)
	})

	b.socket.On(websocket.EventDisconnected, func(event websocket.Event) {
		var errMsg string
		if err, ok := event.Data.(error); ok && err != nil {
			errMsg = err.Error()
		} else {
			errMsg = "unknown reason"
		}
		b.logger.Warnf("Disconnected from Bybit WebSocket: %s", errMsg)

		// Try to reconnect if the context is still valid
		select {
		case <-b.ctx.Done():
			return
		default:
			go b.attemptReconnect()
		}
	})

	// Handle text messages
	b.socket.On(websocket.EventTextMessage, func(event websocket.Event) {
		message := event.Data.(string)
		b.handleMessage([]byte(message))
	})
}

// handleMessage processes incoming WebSocket messages
func (b *BybitExchange) handleMessage(message []byte) {
	// Check if it's a ping message
	var pingMsg map[string]any
	if err := json.Unmarshal(message, &pingMsg); err == nil {
		if op, ok := pingMsg["op"]; ok && op == "ping" {
			// Send pong response
			pongMsg := map[string]string{"op": "pong"}
			pongJSON, err := json.Marshal(pongMsg)
			if err != nil {
				b.logger.Errorf("Failed to marshal pong message: %v", err)
				return
			}

			if err := b.socket.SendText(string(pongJSON)); err != nil {
				b.logger.Errorf("Failed to send pong: %v", err)
			}
			return
		}
	}

	var response WsResponse
	if err := json.Unmarshal(message, &response); err != nil {
		b.logger.Errorf("Unmarshal error: %v", err)
		return
	}

	// Check if it's an order book topic
	if len(response.Topic) >= 10 && response.Topic[:10] == "orderbook." {
		// Extract symbol from topic (format: "orderbook.50.BTCUSDT")
		parts := strings.Split(response.Topic, ".")
		if len(parts) < 3 {
			b.logger.Errorf("Invalid topic format: %s", response.Topic)
			return
		}
		symbol := parts[2]

		b.handleOrderBookUpdate(symbol, response.Data)
	}
}

// handleOrderBookUpdate processes order book updates
func (b *BybitExchange) handleOrderBookUpdate(symbol string, data []byte) {
	var orderbook OrderBookUpdate
	if err := json.Unmarshal(data, &orderbook); err != nil {
		b.logger.Errorf("Order book unmarshal error: %v", err)
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.orderBooks[symbol]; !exists {
		b.orderBooks[symbol] = &OrderBookData{
			Book: exchange.OrderBook{},
		}
	}

	symbol = strings.ReplaceAll(symbol, b.quotedAsset, "")

	newBook := b.processOrderBookLevels(orderbook)
	newBook.Timestamp = time.Now()
	b.orderBooks[symbol].Book = newBook
}

// processOrderBookLevels converts the raw order book data to our internal format
func (b *BybitExchange) processOrderBookLevels(orderbook OrderBookUpdate) exchange.OrderBook {
	newBook := exchange.OrderBook{
		Bids: make([]exchange.Order, 0, len(orderbook.Bids)),
		Asks: make([]exchange.Order, 0, len(orderbook.Asks)),
	}

	// Process bids
	for _, level := range orderbook.Bids {
		price, err := strconv.ParseFloat(level[0], 64)
		if err != nil {
			b.logger.Errorf("Error parsing bid price %s: %v", level[0], err)
			continue
		}

		amount, err := strconv.ParseFloat(level[1], 64)
		if err != nil {
			b.logger.Errorf("Error parsing bid size %s: %v", level[1], err)
			continue
		}

		newBook.Bids = append(newBook.Bids, exchange.Order{
			Price:    price,
			Amount:   amount,
			Exchange: "Bybit",
		})
	}

	// Process asks
	for _, level := range orderbook.Asks {
		price, err := strconv.ParseFloat(level[0], 64)
		if err != nil {
			b.logger.Errorf("Error parsing ask price %s: %v", level[0], err)
			continue
		}

		amount, err := strconv.ParseFloat(level[1], 64)
		if err != nil {
			b.logger.Errorf("Error parsing ask size %s: %v", level[1], err)
			continue
		}

		newBook.Asks = append(newBook.Asks, exchange.Order{
			Price:    price,
			Amount:   amount,
			Exchange: "Bybit",
		})
	}

	return newBook
}

// attemptReconnect tries to reestablish the WebSocket connection
func (b *BybitExchange) attemptReconnect() {
	maxRetries := 5
	backoff := 1 * time.Second

	for retry := 0; retry < maxRetries; retry++ {
		b.logger.Warnf("Attempting to reconnect to Bybit WebSocket (attempt %d/%d)", retry+1, maxRetries)

		time.Sleep(backoff)

		// Exponential backoff
		backoff *= 2

		err := b.connect()
		if err == nil {
			b.logger.Infof("Successfully reconnected to Bybit WebSocket")

			// Resubscribe to all previous order books
			b.mu.RLock()
			symbols := make([]string, 0, len(b.orderBooks))
			for symbol := range b.orderBooks {
				symbols = append(symbols, symbol)
			}
			b.mu.RUnlock()

			for _, symbol := range symbols {
				if err := b.SubscribeToOrderBook(symbol); err != nil {
					b.logger.Errorf("Failed to resubscribe to %s: %v", symbol, err)
				}
			}

			return
		}

		b.logger.Errorf("Failed to reconnect: %v", err)
	}

	b.logger.Errorf("Failed to reconnect after %d attempts", maxRetries)
}
