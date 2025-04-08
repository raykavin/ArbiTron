package binance

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
	mainUrl = "wss://stream.binance.com:9443/ws"
)

// SubscriptionMessage represents the WebSocket subscription request
type SubscriptionMessage struct {
	Method string   `json:"method"`
	Params []string `json:"params"`
	ID     int      `json:"id"`
}

// OrderBookUpdate represents order book update data
type OrderBookUpdate struct {
	LastUpdateID int64       `json:"lastUpdateId"`
	Bids         [][2]string `json:"bids"`
	Asks         [][2]string `json:"asks"`
	EventTime    int64       `json:"E"`
}

// OrderBookData stores an order book with its timestamp
type OrderBookData struct {
	Book exchange.OrderBook
}

// BinanceExchange handles WebSocket communication with Binance
type BinanceExchange struct {
	socket       websocket.Client
	ctx          context.Context
	url          string
	quotedAsset  string
	maxStaleData time.Duration
	mu           sync.RWMutex
	logger       ui.Logger
	orderBooks   map[string]*OrderBookData
	nextID       int
	depth        int
}

// New creates a new Binance WebSocket client
func New(ctx context.Context, depth int, quotedAsset string, socket websocket.Client, maxStaleData time.Duration, logger ui.Logger) (
	*BinanceExchange,
	error,
) {
	binance := &BinanceExchange{
		url:          mainUrl,
		socket:       socket,
		ctx:          ctx,
		quotedAsset:  quotedAsset,
		logger:       logger,
		maxStaleData: maxStaleData,
		depth:        depth,
		orderBooks:   make(map[string]*OrderBookData),
		nextID:       1,
	}

	return binance, binance.connect()
}

func (b *BinanceExchange) GetName() string {
	return "Binance"
}

// Close closes the WebSocket connection and stops all goroutines
func (b *BinanceExchange) Close() {
	if b.socket != nil && b.socket.IsConnected() {
		b.socket.Close()
	}
}

// SubscribeToOrderBook subscribes to order book updates for a specific symbol
func (b *BinanceExchange) SubscribeToOrderBook(symbol string) error {
	if b.socket == nil || !b.socket.IsConnected() {
		return fmt.Errorf("websocket not connected")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// Convert symbol to lowercase for Binance
	streamName := fmt.Sprint(strings.ToLower(symbol), strings.ToLower(b.quotedAsset), fmt.Sprintf("@depth%d@100ms", b.depth))

	subscription := SubscriptionMessage{
		Method: "SUBSCRIBE",
		Params: []string{streamName},
		ID:     b.nextID,
	}
	b.nextID++

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
func (b *BinanceExchange) GetOrderBook(symbol string) (*exchange.OrderBook, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	orderBookData, exists := b.orderBooks[symbol]
	if !exists {
		return nil, fmt.Errorf("no order book for %s", symbol)
	}

	// Check if data is stale
	if time.Since(orderBookData.Book.Timestamp) > b.maxStaleData {
		return nil, fmt.Errorf("stale order book for %s (last updated %s ago)",
			symbol, time.Since(orderBookData.Book.Timestamp).String())
	}

	return &orderBookData.Book, nil
}

// connect establishes a WebSocket connection to Binance
func (b *BinanceExchange) connect() error {
	// Set up event handlers
	b.setupEventHandlers()

	// Establish connection
	var connErr error
	b.socket.On(websocket.EventConnectError, func(event websocket.Event) {
		if err, ok := event.Data.(error); ok && err != nil {
			connErr = err
			return
		}

		connErr = fmt.Errorf("establish connection with hyperliquid unknown error")
	})

	b.socket.Connect(b.url)

	return connErr
}

// setupEventHandlers configures all WebSocket event handlers
func (b *BinanceExchange) setupEventHandlers() {
	// Handle connection events
	b.socket.On(websocket.EventConnected, func(event websocket.Event) {
		b.logger.Infof("Connected to Binance WebSocket")
	})

	b.socket.On(websocket.EventConnectError, func(event websocket.Event) {
		err := event.Data.(error)
		b.logger.Errorf("Failed to connect to Binance WebSocket: %v", err)
	})

	b.socket.On(websocket.EventDisconnected, func(event websocket.Event) {
		var errMsg string
		if err, ok := event.Data.(error); ok && err != nil {
			errMsg = err.Error()
		} else {
			errMsg = "unknown reason"
		}
		b.logger.Warnf("Disconnected from Binance WebSocket: %s", errMsg)

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
func (b *BinanceExchange) handleMessage(message []byte) {
	var response map[string]any
	if err := json.Unmarshal(message, &response); err != nil {
		b.logger.Errorf("Unmarshal error: %v", err)
		return
	}

	// Check if it's an order book update
	if _, ok := response["e"]; ok && response["e"] == "depthUpdate" {
		// Extract symbol from the response
		symbol, ok := response["s"].(string)
		if !ok {
			b.logger.Errorf("Unable to extract symbol from message")
			return
		}

		b.handleOrderBookUpdate(symbol, message)
	}
}

// handleOrderBookUpdate processes order book updates
func (b *BinanceExchange) handleOrderBookUpdate(symbol string, data []byte) {
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

	newBook := b.processOrderBookLevels(orderbook)
	newBook.Timestamp = time.Now()
	b.orderBooks[symbol].Book = newBook
}

// processOrderBookLevels converts the raw order book data to our internal format
func (b *BinanceExchange) processOrderBookLevels(orderbook OrderBookUpdate) exchange.OrderBook {
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
			Exchange: "Binance",
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
			Exchange: "Binance",
		})
	}

	return newBook
}

// attemptReconnect tries to reestablish the WebSocket connection
func (b *BinanceExchange) attemptReconnect() {
	maxRetries := 5
	backoff := 1 * time.Second

	for retry := 0; retry < maxRetries; retry++ {
		b.logger.Warnf("Attempting to reconnect to Binance WebSocket (attempt %d/%d)", retry+1, maxRetries)

		time.Sleep(backoff)

		// Exponential backoff
		backoff *= 2

		err := b.connect()
		if err == nil {
			b.logger.Infof("Successfully reconnected to Binance WebSocket")

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
