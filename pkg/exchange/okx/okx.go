// internal/exchange/okx/okx.go
package okx

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

const okxWSURL = "wss://ws.okx.com:8443/ws/v5/public"

// OKXExchange manages WebSocket communication with OKX
type OKXExchange struct {
	socket       websocket.Client
	ctx          context.Context
	quotedAsset  string
	orderBooks   map[string]*OrderBookData
	maxStaleData time.Duration
	mu           sync.RWMutex
	logger       ui.Logger
}

// OrderBookData stores an order book with its timestamp
type OrderBookData struct {
	Book exchange.OrderBook
}

// wsSubscriptionMessage represents the WebSocket subscription message
type WsSubscriptionMessage struct {
	Op   string              `json:"op"`
	Args []WsSubscriptionArg `json:"args"`
}

// wsSubscriptionArg represents arguments for WebSocket subscription
type WsSubscriptionArg struct {
	Channel string `json:"channel"`
	InstID  string `json:"instId"`
}

type WsOrderBookData struct {
	Asks      [][]string `json:"asks"`
	Bids      [][]string `json:"bids"`
	Timestamp string     `json:"ts"`
}

// wsOrderBookResponse represents the WebSocket response for order book
type WsOrderBookResponse struct {
	Arg  WsSubscriptionArg `json:"arg"`
	Data []WsOrderBookData `json:"data"`
}

// New creates a new instance of OKX WebSocket client
func New(ctx context.Context, websocketClient websocket.Client, maxStaleData time.Duration, quotedAsset string, logger ui.Logger) (
	*OKXExchange,
	error,
) {
	okx := &OKXExchange{
		ctx:          ctx,
		quotedAsset:  quotedAsset,
		socket:       websocketClient,
		maxStaleData: maxStaleData,
		logger:       logger,
		orderBooks:   make(map[string]*OrderBookData),
	}

	return okx, okx.connect()
}

// GetName returns the exchange name
func (o *OKXExchange) GetName() string {
	return "OKX"
}

// SubscribeToOrderBook subscribes to order book updates for a specific symbol
func (o *OKXExchange) SubscribeToOrderBook(symbol string) error {
	symbol = fmt.Sprintf("%s-%s", symbol, o.quotedAsset)

	if o.socket == nil || !o.socket.IsConnected() {
		return fmt.Errorf("websocket not connected")
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	// Initialize the order book for this symbol
	o.orderBooks[symbol] = &OrderBookData{
		Book: exchange.OrderBook{},
	}

	subscription := WsSubscriptionMessage{
		Op: "subscribe",
		Args: []WsSubscriptionArg{
			{
				Channel: "books",
				InstID:  symbol,
			},
		},
	}

	subscriptionJSON, err := json.Marshal(subscription)
	if err != nil {
		return fmt.Errorf("error marshaling subscription: %w", err)
	}

	return o.socket.SendText(string(subscriptionJSON))
}

// GetOrderBook returns the current order book for a symbol
func (o *OKXExchange) GetOrderBook(symbol string) (*exchange.OrderBook, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	symbol = fmt.Sprintf("%s-%s", strings.ToUpper(symbol), strings.ToUpper(o.quotedAsset))
	orderBookData, exists := o.orderBooks[symbol]
	if !exists {
		return nil, fmt.Errorf("no order book for %s", symbol)
	}

	// Check if data is stale
	if time.Since(orderBookData.Book.Timestamp) > o.maxStaleData {
		return nil, fmt.Errorf("stale order book for %s (last updated %s ago)",
			symbol, time.Since(orderBookData.Book.Timestamp).String())
	}

	return &orderBookData.Book, nil
}

// Close terminates the WebSocket connection
func (o *OKXExchange) Close() {
	if o.socket != nil && o.socket.IsConnected() {
		o.socket.Close()
	}
}

// connect establishes a WebSocket connection to OKX
func (o *OKXExchange) connect() error {
	// Set up event handlers
	o.setupEventHandlers()

	// Establish connection
	var connErr error
	o.socket.On(websocket.EventConnectError, func(event websocket.Event) {
		if err, ok := event.Data.(error); ok && err != nil {
			connErr = err
			return
		}

		connErr = fmt.Errorf("establish connection with okx unknown error")
	})

	o.socket.Connect(okxWSURL)

	return connErr
}

// setupEventHandlers configures all WebSocket event handlers
func (o *OKXExchange) setupEventHandlers() {
	// Handle connection events
	o.socket.On(websocket.EventConnected, func(event websocket.Event) {
		o.logger.Infof("Connected to OKX WebSocket")
	})

	o.socket.On(websocket.EventConnectError, func(event websocket.Event) {
		err := event.Data.(error)
		o.logger.Errorf("Failed to connect to OKX WebSocket: %v", err)
	})

	o.socket.On(websocket.EventDisconnected, func(event websocket.Event) {
		var errMsg string
		if err, ok := event.Data.(error); ok && err != nil {
			errMsg = err.Error()
		} else {
			errMsg = "unknown reason"
		}
		o.logger.Warnf("Disconnected from OKX WebSocket: %s", errMsg)

		// Try to reconnect if the context is still valid
		select {
		case <-o.ctx.Done():
			return
		default:
			go o.attemptReconnect()
		}
	})

	// Handle text messages
	o.socket.On(websocket.EventTextMessage, func(event websocket.Event) {
		message := event.Data.(string)
		o.handleMessage([]byte(message))
	})
}

// handleMessage processes incoming WebSocket messages
func (o *OKXExchange) handleMessage(message []byte) {
	var response WsOrderBookResponse
	if err := json.Unmarshal(message, &response); err != nil {
		// This might not be an order book message, so we don't log an error
		return
	}

	// Continue only if we have data
	if len(response.Data) == 0 {
		return
	}

	o.updateOrderBook(&response)
}

// updateOrderBook processes order book updates
func (o *OKXExchange) updateOrderBook(response *WsOrderBookResponse) {
	symbol := response.Arg.InstID

	// Parse asks and bids
	asks := parseLevels(response.Data[0].Asks, o.GetName())
	bids := parseLevels(response.Data[0].Bids, o.GetName())

	o.mu.Lock()
	defer o.mu.Unlock()

	bookData, exists := o.orderBooks[symbol]
	if !exists {
		// This can happen if we receive data for a symbol we haven't explicitly subscribed to
		o.orderBooks[symbol] = &OrderBookData{
			Book: exchange.OrderBook{
				Asks:      asks,
				Bids:      bids,
				Timestamp: time.Now(),
			},
		}
		return
	}

	// Update existing entry
	bookData.Book = exchange.OrderBook{
		Asks:      asks,
		Bids:      bids,
		Timestamp: time.Now(),
	}
}

// parseLevels converts price levels from OKX format to internal format
func parseLevels(raw [][]string, exchangeName string) []exchange.Order {
	orders := make([]exchange.Order, 0, len(raw))
	for _, level := range raw {
		if len(level) < 2 {
			continue
		}
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

// attemptReconnect tries to reestablish the WebSocket connection
func (o *OKXExchange) attemptReconnect() {
	maxRetries := 5
	backoff := 1 * time.Second

	for retry := 0; retry < maxRetries; retry++ {
		o.logger.Warnf("Attempting to reconnect to OKX WebSocket (attempt %d/%d)", retry+1, maxRetries)

		time.Sleep(backoff)

		// Exponential backoff
		backoff *= 2

		err := o.connect()
		if err == nil {
			o.logger.Infof("Successfully reconnected to OKX WebSocket")

			// Resubscribe to all previous order books
			o.mu.RLock()
			symbols := make([]string, 0, len(o.orderBooks))
			for symbol := range o.orderBooks {
				symbols = append(symbols, symbol)
			}
			o.mu.RUnlock()

			for _, symbol := range symbols {
				if err := o.SubscribeToOrderBook(symbol); err != nil {
					o.logger.Errorf("Failed to resubscribe to %s: %v", symbol, err)
				}
			}

			return
		}

		o.logger.Errorf("Failed to reconnect: %v", err)
	}

	o.logger.Errorf("Failed to reconnect after %d attempts", maxRetries)
}
