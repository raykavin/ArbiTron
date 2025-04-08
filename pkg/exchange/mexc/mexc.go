package mexc

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/raykavin/ArbiTron/internal/ui"
	"github.com/raykavin/ArbiTron/pkg/exchange"
	"github.com/raykavin/ArbiTron/pkg/http/websocket"
)

const (
	// Channel types
	channelOrderBook = "spot@public.deals.v3.api.pb"

	// WebSocket URLs
	wsUrl = "wss://wbs.mexc.com/ws"
)

// SubscriptionMessage represents the WebSocket subscription request
type SubscriptionMessage struct {
	Method string   `json:"method"`
	Params []string `json:"params"`
	ID     int      `json:"id"`
}

// WsResponse represents the WebSocket response
type WsResponse struct {
	C string          `json:"c"` // Channel
	D json.RawMessage `json:"d"` // Data
	S string          `json:"s"` // Symbol
}

// OrderBookUpdate represents order book update data
type OrderBookUpdate struct {
	Asks      [][2]string `json:"asks"`
	Bids      [][2]string `json:"bids"`
	Timestamp int64       `json:"timestamp"`
}

// OrderBookData stores an order book with its timestamp
type OrderBookData struct {
	Book exchange.OrderBook
}

// MexcExchange handles WebSocket communication with MEXC
type MexcExchange struct {
	socket       websocket.Client
	ctx          context.Context
	quotedAsset  string
	orderBooks   map[string]*OrderBookData
	maxStaleData time.Duration
	mu           sync.RWMutex
	logger       ui.Logger
	nextID       int
}

// New creates a new MEXC WebSocket client
func New(ctx context.Context, websocketClient websocket.Client, maxStaleData time.Duration, quotedAsset string, logger ui.Logger) (
	*MexcExchange,
	error,
) {
	mexc := &MexcExchange{
		ctx:          ctx,
		socket:       websocketClient,
		maxStaleData: maxStaleData,
		quotedAsset:  quotedAsset,
		logger:       logger,
		orderBooks:   make(map[string]*OrderBookData),
		nextID:       1,
	}

	return mexc, mexc.connect()
}

func (m *MexcExchange) GetName() string {
	return "MEXC"
}

// Close closes the WebSocket connection and stops all goroutines
func (m *MexcExchange) Close() {
	if m.socket != nil && m.socket.IsConnected() {
		m.socket.Close()
	}
}

// SubscribeToOrderBook subscribes to order book updates for a specific symbol
func (m *MexcExchange) SubscribeToOrderBook(symbol string) error {
	if m.socket == nil || !m.socket.IsConnected() {
		return fmt.Errorf("websocket not connected")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	channelName := fmt.Sprintf("%s@%s-%s", channelOrderBook, symbol, m.quotedAsset)

	subscription := SubscriptionMessage{
		Method: "SUBSCRIPTION",
		Params: []string{channelName},
		ID:     m.nextID,
	}
	m.nextID++

	// Initialize the order book for this symbol
	m.orderBooks[symbol] = &OrderBookData{
		Book: exchange.OrderBook{},
	}

	subscriptionJSON, err := json.Marshal(subscription)
	if err != nil {
		return fmt.Errorf("error marshaling subscription: %w", err)
	}

	return m.socket.SendText(string(subscriptionJSON))
}

// GetOrderBook retrieves the current order book for a symbol
func (m *MexcExchange) GetOrderBook(symbol string) (*exchange.OrderBook, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	orderBookData, exists := m.orderBooks[symbol]
	if !exists {
		return nil, fmt.Errorf("no order book for %s", symbol)
	}

	// Check if data is stale
	if time.Since(orderBookData.Book.Timestamp) > m.maxStaleData {
		return nil, fmt.Errorf("stale order book for %s (last updated %s ago)",
			symbol, time.Since(orderBookData.Book.Timestamp).String())
	}

	return &orderBookData.Book, nil
}

// connect establishes a WebSocket connection to MEXC
func (m *MexcExchange) connect() error {
	// Set up event handlers
	m.setupEventHandlers()

	// Establish connection
	var connErr error
	m.socket.On(websocket.EventConnectError, func(event websocket.Event) {
		if err, ok := event.Data.(error); ok && err != nil {
			connErr = err
			return
		}

		connErr = fmt.Errorf("establish connection with MEXC unknown error")
	})

	m.socket.Connect(wsUrl)

	return connErr
}

// setupEventHandlers configures all WebSocket event handlers
func (m *MexcExchange) setupEventHandlers() {
	// Handle connection events
	m.socket.On(websocket.EventConnected, func(event websocket.Event) {
		m.logger.Infof("Connected to MEXC WebSocket")
	})

	m.socket.On(websocket.EventConnectError, func(event websocket.Event) {
		err := event.Data.(error)
		m.logger.Errorf("Failed to connect to MEXC WebSocket: %v", err)
	})

	m.socket.On(websocket.EventDisconnected, func(event websocket.Event) {
		var errMsg string
		if err, ok := event.Data.(error); ok && err != nil {
			errMsg = err.Error()
		} else {
			errMsg = "unknown reason"
		}
		m.logger.Warnf("Disconnected from MEXC WebSocket: %s", errMsg)

		// Try to reconnect if the context is still valid
		select {
		case <-m.ctx.Done():
			return
		default:
			go m.attemptReconnect()
		}
	})

	// Handle text messages
	m.socket.On(websocket.EventTextMessage, func(event websocket.Event) {
		message := event.Data.(string)
		m.handleMessage([]byte(message))
	})
}

// handleMessage processes incoming WebSocket messages
func (m *MexcExchange) handleMessage(message []byte) {
	var response WsResponse
	if err := json.Unmarshal(message, &response); err != nil {
		m.logger.Errorf("Unmarshal error: %v", err)
		return
	}

	if response.C == channelOrderBook {
		m.handleOrderBookUpdate(response.S, response.D)
	}
}

// handleOrderBookUpdate processes order book updates
func (m *MexcExchange) handleOrderBookUpdate(symbol string, data []byte) {
	var orderbook OrderBookUpdate
	if err := json.Unmarshal(data, &orderbook); err != nil {
		m.logger.Errorf("Order book unmarshal error: %v", err)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.orderBooks[symbol]; !exists {
		m.orderBooks[symbol] = &OrderBookData{
			Book: exchange.OrderBook{},
		}
	}

	newBook := m.processOrderBookLevels(orderbook)
	newBook.Timestamp = time.Now()
	m.orderBooks[symbol].Book = newBook
}

// processOrderBookLevels converts the raw order book data to our internal format
func (m *MexcExchange) processOrderBookLevels(orderbook OrderBookUpdate) exchange.OrderBook {
	newBook := exchange.OrderBook{
		Bids: make([]exchange.Order, 0, len(orderbook.Bids)),
		Asks: make([]exchange.Order, 0, len(orderbook.Asks)),
	}

	// Process bids
	for _, level := range orderbook.Bids {
		price, err := strconv.ParseFloat(level[0], 64)
		if err != nil {
			m.logger.Errorf("Error parsing bid price %s: %v", level[0], err)
			continue
		}

		amount, err := strconv.ParseFloat(level[1], 64)
		if err != nil {
			m.logger.Errorf("Error parsing bid size %s: %v", level[1], err)
			continue
		}

		newBook.Bids = append(newBook.Bids, exchange.Order{
			Price:    price,
			Amount:   amount,
			Exchange: "MEXC",
		})
	}

	// Process asks
	for _, level := range orderbook.Asks {
		price, err := strconv.ParseFloat(level[0], 64)
		if err != nil {
			m.logger.Errorf("Error parsing ask price %s: %v", level[0], err)
			continue
		}

		amount, err := strconv.ParseFloat(level[1], 64)
		if err != nil {
			m.logger.Errorf("Error parsing ask size %s: %v", level[1], err)
			continue
		}

		newBook.Asks = append(newBook.Asks, exchange.Order{
			Price:    price,
			Amount:   amount,
			Exchange: "MEXC",
		})
	}

	return newBook
}

// attemptReconnect tries to reestablish the WebSocket connection
func (m *MexcExchange) attemptReconnect() {
	maxRetries := 5
	backoff := 1 * time.Second

	for retry := 0; retry < maxRetries; retry++ {
		m.logger.Warnf("Attempting to reconnect to MEXC WebSocket (attempt %d/%d)", retry+1, maxRetries)

		time.Sleep(backoff)

		// Exponential backoff
		backoff *= 2

		err := m.connect()
		if err == nil {
			m.logger.Infof("Successfully reconnected to MEXC WebSocket")

			// Resubscribe to all previous order books
			m.mu.RLock()
			symbols := make([]string, 0, len(m.orderBooks))
			for symbol := range m.orderBooks {
				symbols = append(symbols, symbol)
			}
			m.mu.RUnlock()

			for _, symbol := range symbols {
				if err := m.SubscribeToOrderBook(symbol); err != nil {
					m.logger.Errorf("Failed to resubscribe to %s: %v", symbol, err)
				}
			}

			return
		}

		m.logger.Errorf("Failed to reconnect: %v", err)
	}

	m.logger.Errorf("Failed to reconnect after %d attempts", maxRetries)
}
