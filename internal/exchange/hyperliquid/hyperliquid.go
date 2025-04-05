// internal/exchange/hyperliquid/hyperliquid.go
package hyperliquid

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"notlelouch/ArbiBot/internal/exchange"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	wsHandshakeTimeout = 15 * time.Second
	staleDuration      = 2 * time.Second

	// Channel types
	channelL2Book = "l2Book"

	// Subscription types
	subscriptionL2Book = "l2Book"

	// WebSocket URLs
	testnetWSURL = "wss://api.hyperliquid-testnet.xyz/ws"
	mainnetWSURL = "wss://api.hyperliquid.xyz/ws"
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
	// Timestamp time.Time
	Book exchange.OrderBook
}

// HyperliquidWS handles WebSocket communication with Hyperliquid
type HyperliquidWS struct {
	url        string
	conn       *websocket.Conn
	handlers   map[string]func([]byte)
	orderBooks map[string]*OrderBookData
	mu         sync.RWMutex
	ctx        context.Context
	cancel     context.CancelFunc
}

// NewHyperliquidWS creates a new Hyperliquid WebSocket client
func NewHyperliquidWS(mainnet bool) *HyperliquidWS {
	url := testnetWSURL
	if mainnet {
		url = mainnetWSURL
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &HyperliquidWS{
		url:        url,
		handlers:   make(map[string]func([]byte)),
		orderBooks: make(map[string]*OrderBookData),
		ctx:        ctx,
		cancel:     cancel,
	}
}

func (h *HyperliquidWS) GetName() string {
	return "Hyperliquid"
}

// Connect establishes a WebSocket connection to Hyperliquid
func (h *HyperliquidWS) Connect(ctx context.Context) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: wsHandshakeTimeout,
	}

	conn, _, err := dialer.DialContext(ctx, h.url, nil)
	if err != nil {
		return fmt.Errorf("websocket connection failed: %w", err)
	}

	h.conn = conn
	go h.handleMessages(ctx)
	return nil
}

// Close closes the WebSocket connection and stops all goroutines
func (h *HyperliquidWS) Close() {
	h.cancel()
	if h.conn != nil {
		h.conn.Close()
	}
}

// SubscribeToOrderBook subscribes to order book updates for a specific coin
func (h *HyperliquidWS) SubscribeToOrderBook(coin string) error {
	if h.conn == nil {
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
		// Timestamp: time.Time{},
		Book: exchange.OrderBook{},
	}

	return h.conn.WriteJSON(subscription)
}

// GetOrderBook retrieves the current order book for a coin
func (h *HyperliquidWS) GetOrderBook(coin string) (*exchange.OrderBook, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	orderBookData, exists := h.orderBooks[coin]
	if !exists {
		return nil, fmt.Errorf("no order book for %s", coin)
	}

	// Check if data is stale
	if time.Since(orderBookData.Book.Timestamp) > staleDuration {
		return nil, fmt.Errorf("stale order book for %s (last updated %s ago)",
			coin, time.Since(orderBookData.Book.Timestamp).String())
	}

	return &orderBookData.Book, nil
}

// handleMessages processes incoming WebSocket messages
func (h *HyperliquidWS) handleMessages(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			_, message, err := h.conn.ReadMessage()
			if err != nil {
				log.Printf("read error: %v", err)
				// Try to reconnect
				h.attemptReconnect(ctx)
				return
			}

			var response WsResponse
			if err := json.Unmarshal(message, &response); err != nil {
				log.Printf("unmarshal error: %v", err)
				continue
			}

			switch response.Channel {
			case channelL2Book:
				h.handleOrderBookUpdate(response.Data)
			default:
			}
		}
	}
}

// handleOrderBookUpdate processes order book updates
func (h *HyperliquidWS) handleOrderBookUpdate(data []byte) {
	var orderbook WsBook
	if err := json.Unmarshal(data, &orderbook); err != nil {
		log.Printf("l2Book unmarshal error: %v", err)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	coin := orderbook.Coin
	if _, exists := h.orderBooks[coin]; !exists {
		h.orderBooks[coin] = &OrderBookData{
			Book: exchange.OrderBook{},
			// Timestamp: time.Time{},
		}
	}

	newBook := h.processOrderBookLevels(orderbook)

	newBook.Timestamp = time.Now()
	h.orderBooks[coin].Book = newBook
}

// processOrderBookLevels converts the raw order book data to our internal format
func (h *HyperliquidWS) processOrderBookLevels(orderbook WsBook) exchange.OrderBook {
	newBook := exchange.OrderBook{
		Bids: make([]exchange.Order, 0, len(orderbook.Levels[1])),
		Asks: make([]exchange.Order, 0, len(orderbook.Levels[0])),
	}

	// Process bids (index 1 in the Levels array)
	for _, level := range orderbook.Levels[1] {
		price, err := strconv.ParseFloat(level.Px, 64)
		if err != nil {
			log.Printf("Error parsing bid price %s: %v", level.Px, err)
			continue
		}

		amount, err := strconv.ParseFloat(level.Sz, 64)
		if err != nil {
			log.Printf("Error parsing bid size %s: %v", level.Sz, err)
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
			log.Printf("Error parsing ask price %s: %v", level.Px, err)
			continue
		}

		amount, err := strconv.ParseFloat(level.Sz, 64)
		if err != nil {
			log.Printf("Error parsing ask size %s: %v", level.Sz, err)
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
func (h *HyperliquidWS) attemptReconnect(ctx context.Context) {
	maxRetries := 5
	backoff := 1 * time.Second

	for retry := range maxRetries {
		log.Printf("Attempting to reconnect to Hyperliquid WebSocket (attempt %d/%d)", retry+1, maxRetries)

		time.Sleep(backoff)

		// Exponential backoff
		backoff *= 2

		err := h.Connect(ctx)
		if err == nil {
			log.Printf("Successfully reconnected to Hyperliquid WebSocket")

			// Resubscribe to all previous order books
			h.mu.RLock()
			coins := make([]string, 0, len(h.orderBooks))
			for coin := range h.orderBooks {
				coins = append(coins, coin)
			}
			h.mu.RUnlock()

			for _, coin := range coins {
				if err := h.SubscribeToOrderBook(coin); err != nil {
					log.Printf("Failed to resubscribe to %s: %v", coin, err)
				}
			}

			return
		}

		log.Printf("Failed to reconnect: %v", err)
	}

	log.Printf("Failed to reconnect after %d attempts", maxRetries)
}
