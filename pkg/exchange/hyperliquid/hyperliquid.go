// internal/exchange/hyperliquid/hyperliquid.go
package hyperliquid

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/raykavin/ArbiTron/pkg/exchange"
	"github.com/raykavin/ArbiTron/pkg/http/websocket"
	"github.com/raykavin/ArbiTron/pkg/logger"
)

const (
	// Channel types
	channelL2Book = "l2Book"

	// Subscription types
	subscriptionL2Book = "l2Book"

	// WebSocket URLs
	testnetURL = "wss://api.hyperliquid-testnet.xyz/ws"
	mainnetURL = "wss://api.hyperliquid.xyz/ws"
)

// WsResponse represents the WebSocket response
type WsResponse struct {
	Channel string          `json:"channel"`
	Data    json.RawMessage `json:"data"`
}

// SubscriptionMessage represents the WebSocket subscription request
type SubscriptionMessage struct {
	Method       string       `json:"method"`
	Subscription Subscription `json:"subscription"`
}

// Subscription defines what data to subscribe to
type Subscription struct {
	Type string `json:"type"`
	Coin string `json:"coin,omitempty"`
}

// WsTrade represents a trade from the WebSocket feed
type WsTrade struct {
	Coin  string   `json:"coin"`
	Side  string   `json:"side"`
	Px    string   `json:"px"`
	Sz    string   `json:"sz"`
	Hash  string   `json:"hash"`
	Users []string `json:"users"`
	Time  int64    `json:"time"`
	Tid   int64    `json:"tid"`
}

// WsLevel represents a price level in the order book
type WsLevel struct {
	Px string `json:"px"` // price
	Sz string `json:"sz"` // size
	N  int    `json:"n"`  // number of orders
}

// WsBook represents the full order book from WebSocket
type WsBook struct {
	Coin   string       `json:"coin"`
	Levels [2][]WsLevel `json:"levels"` // 0: asks, 1: bids
	Time   int64        `json:"time"`
}

// OrderBookData stores an order book with its timestamp
type OrderBookData struct {
	Book exchange.OrderBook
}

// HyperliquidExchange handles WebSocket communication with Hyperliquid
type HyperliquidExchange struct {
	socket       websocket.Client
	ctx          context.Context
	url          string
	orderBooks   map[string]*OrderBookData
	maxStaleData time.Duration
	mu           sync.RWMutex
	logger       logger.Logger
	isMainnet    bool
}

// NewHyperliquidWS creates a new Hyperliquid WebSocket client
func New(ctx context.Context, mainnet bool, websocketClient websocket.Client, maxStaleData time.Duration, logger logger.Logger) (
	*HyperliquidExchange,
	error,
) {
	url := testnetURL
	if mainnet {
		url = mainnetURL
	}

	hyperliquid := &HyperliquidExchange{
		url:          url,
		ctx:          ctx,
		socket:       websocketClient,
		logger:       logger,
		maxStaleData: maxStaleData,
		orderBooks:   make(map[string]*OrderBookData),
		isMainnet:    mainnet,
	}

	return hyperliquid, hyperliquid.connect()
}

func (h *HyperliquidExchange) GetName() string {
	return "Hyperliquid"
}

// Close closes the WebSocket connection and stops all goroutines
func (h *HyperliquidExchange) Close() {
	if h.socket != nil && h.socket.IsConnected() {
		h.socket.Close()
	}
}

// SubscribeToOrderBook subscribes to order book updates for a specific coin
func (h *HyperliquidExchange) SubscribeToOrderBook(coin string) error {
	if h.socket == nil || !h.socket.IsConnected() {
		return fmt.Errorf("websocket not connected")
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	subscription := SubscriptionMessage{
		Method: "subscribe",
		Subscription: Subscription{
			Type: subscriptionL2Book,
			Coin: coin,
		},
	}

	// Initialize the order book for this coin
	h.orderBooks[coin] = &OrderBookData{
		Book: exchange.OrderBook{},
	}

	subscriptionJSON, err := json.Marshal(subscription)
	if err != nil {
		return fmt.Errorf("error marshaling subscription: %w", err)
	}

	return h.socket.SendText(string(subscriptionJSON))
}

// GetOrderBook retrieves the current order book for a coin
func (h *HyperliquidExchange) GetOrderBook(coin string) (*exchange.OrderBook, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	orderBookData, exists := h.orderBooks[coin]
	if !exists {
		return nil, fmt.Errorf("no order book for %s", coin)
	}

	// Check if data is stale
	if time.Since(orderBookData.Book.Timestamp) > h.maxStaleData {
		return nil, fmt.Errorf("stale order book for %s (last updated %s ago)",
			coin, time.Since(orderBookData.Book.Timestamp).String())
	}

	return &orderBookData.Book, nil
}

// connect establishes a WebSocket connection to Hyperliquid
func (h *HyperliquidExchange) connect() error {
	// Set up event handlers
	h.setupEventHandlers()

	// Establish connection
	var connErr error
	h.socket.On(websocket.EventConnectError, func(event websocket.Event) {
		if err, ok := event.Data.(error); ok && err != nil {
			connErr = err
			return
		}

		connErr = fmt.Errorf("establish connection with hyperliquid unknown error")
	})

	h.socket.Connect(h.url)

	return connErr
}

// setupEventHandlers configures all WebSocket event handlers
func (h *HyperliquidExchange) setupEventHandlers() {
	// Handle connection events
	h.socket.On(websocket.EventConnected, func(event websocket.Event) {
		h.logger.Infof("Connected to Hyperliquid WebSocket")
	})

	h.socket.On(websocket.EventConnectError, func(event websocket.Event) {
		err := event.Data.(error)
		h.logger.Errorf("Failed to connect to Hyperliquid WebSocket: %v", err)
	})

	h.socket.On(websocket.EventDisconnected, func(event websocket.Event) {
		var errMsg string
		if err, ok := event.Data.(error); ok && err != nil {
			errMsg = err.Error()
		} else {
			errMsg = "unknown reason"
		}
		h.logger.Warnf("Disconnected from Hyperliquid WebSocket: %s", errMsg)

		// Try to reconnect if the context is still valid
		select {
		case <-h.ctx.Done():
			return
		default:
			go h.attemptReconnect()
		}
	})

	// Handle text messages
	h.socket.On(websocket.EventTextMessage, func(event websocket.Event) {
		message := event.Data.(string)
		h.handleMessage([]byte(message))
	})
}

// handleMessage processes incoming WebSocket messages
func (h *HyperliquidExchange) handleMessage(message []byte) {
	var response WsResponse
	if err := json.Unmarshal(message, &response); err != nil {
		h.logger.Errorf("Unmarshal error: %v", err)
		return
	}

	switch response.Channel {
	case channelL2Book:
		h.handleOrderBookUpdate(response.Data)
	default:
		// Ignore other message types for now
	}
}

// handleOrderBookUpdate processes order book updates
func (h *HyperliquidExchange) handleOrderBookUpdate(data []byte) {
	var orderbook WsBook
	if err := json.Unmarshal(data, &orderbook); err != nil {
		h.logger.Errorf("l2Book unmarshal error: %v", err)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	coin := orderbook.Coin
	if _, exists := h.orderBooks[coin]; !exists {
		h.orderBooks[coin] = &OrderBookData{
			Book: exchange.OrderBook{},
		}
	}

	newBook := h.processOrderBookLevels(orderbook)

	newBook.Timestamp = time.Now()
	h.orderBooks[coin].Book = newBook
}

// processOrderBookLevels converts the raw order book data to our internal format
func (h *HyperliquidExchange) processOrderBookLevels(orderbook WsBook) exchange.OrderBook {
	newBook := exchange.OrderBook{
		Bids: make([]exchange.Order, 0, len(orderbook.Levels[1])),
		Asks: make([]exchange.Order, 0, len(orderbook.Levels[0])),
	}

	// Process bids (index 1 in the Levels array)
	for _, level := range orderbook.Levels[1] {
		price, err := strconv.ParseFloat(level.Px, 64)
		if err != nil {
			h.logger.Errorf("Error parsing bid price %s: %v", level.Px, err)
			continue
		}

		amount, err := strconv.ParseFloat(level.Sz, 64)
		if err != nil {
			h.logger.Errorf("Error parsing bid size %s: %v", level.Sz, err)
			continue
		}

		newBook.Bids = append(newBook.Bids, exchange.Order{
			Price:    price,
			Amount:   amount,
			Exchange: "Hyperliquid",
		})
	}

	// Process asks (index 0 in the Levels array)
	for _, level := range orderbook.Levels[0] {
		price, err := strconv.ParseFloat(level.Px, 64)
		if err != nil {
			h.logger.Errorf("Error parsing ask price %s: %v", level.Px, err)
			continue
		}

		amount, err := strconv.ParseFloat(level.Sz, 64)
		if err != nil {
			h.logger.Errorf("Error parsing ask size %s: %v", level.Sz, err)
			continue
		}

		newBook.Asks = append(newBook.Asks, exchange.Order{
			Price:    price,
			Amount:   amount,
			Exchange: "Hyperliquid",
		})
	}

	return newBook
}

// attemptReconnect tries to reestablish the WebSocket connection
func (h *HyperliquidExchange) attemptReconnect() {
	maxRetries := 5
	backoff := 1 * time.Second

	for retry := 0; retry < maxRetries; retry++ {
		h.logger.Warnf("Attempting to reconnect to Hyperliquid WebSocket (attempt %d/%d)", retry+1, maxRetries)

		time.Sleep(backoff)

		// Exponential backoff
		backoff *= 2

		err := h.connect()
		if err == nil {
			h.logger.Infof("Successfully reconnected to Hyperliquid WebSocket")

			// Resubscribe to all previous order books
			h.mu.RLock()
			coins := make([]string, 0, len(h.orderBooks))
			for coin := range h.orderBooks {
				coins = append(coins, coin)
			}
			h.mu.RUnlock()

			for _, coin := range coins {
				if err := h.SubscribeToOrderBook(coin); err != nil {
					h.logger.Errorf("Failed to resubscribe to %s: %v", coin, err)
				}
			}

			return
		}

		h.logger.Errorf("Failed to reconnect: %v", err)
	}

	h.logger.Errorf("Failed to reconnect after %d attempts", maxRetries)
}
