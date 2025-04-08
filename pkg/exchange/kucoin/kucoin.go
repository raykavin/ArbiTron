// internal/exchange/kucoin/kucoin.go
package kucoin

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

type WsOrderBookData struct {
	Asks      [][]string `json:"asks"`      // [price, size]
	Bids      [][]string `json:"bids"`      // [price, size]
	Timestamp int64      `json:"timestamp"` // milliseconds
}

// WsOrderBookResponse represents the structure for level2depth5 messages
type WsOrderBookResponse struct {
	Type    string          `json:"type"`
	Topic   string          `json:"topic"`
	Subject string          `json:"subject"`
	Data    WsOrderBookData `json:"data"`
}

// OrderBookData stores an order book with its timestamp
type OrderBookData struct {
	Book exchange.OrderBook
}

// KuCoinExchange handles WebSocket communication with KuCoin
type KuCoinExchange struct {
	socket       websocket.Client
	ctx          context.Context
	endpoint     string
	token        string
	orderBooks   map[string]*OrderBookData
	pingInterval time.Duration
	maxStaleData time.Duration
	mu           sync.RWMutex
	logger       ui.Logger
	apiKey       string
	apiSecret    string
	passphrase   string
	isPrivate    bool
}

// New creates a new KuCoin WebSocket client
func New(ctx context.Context, websocketClient websocket.Client, apiKey, apiSecret, passphrase string, isPrivate bool, maxStaleData time.Duration, logger ui.Logger) (
	*KuCoinExchange,
	error,
) {
	kucoin := &KuCoinExchange{
		ctx:          ctx,
		socket:       websocketClient,
		logger:       logger,
		maxStaleData: maxStaleData,
		orderBooks:   make(map[string]*OrderBookData),
		apiKey:       apiKey,
		apiSecret:    apiSecret,
		passphrase:   passphrase,
		isPrivate:    isPrivate,
	}

	// Get token and endpoint
	tokenResp, err := GetToken(apiKey, apiSecret, passphrase, isPrivate)
	if err != nil {
		return nil, fmt.Errorf("failed to get KuCoin token: %w", err)
	}

	if len(tokenResp.Data.InstanceServers) == 0 {
		return nil, fmt.Errorf("no instance servers returned from KuCoin")
	}

	kucoin.endpoint = tokenResp.Data.InstanceServers[0].Endpoint + "?token=" + tokenResp.Data.Token
	kucoin.token = tokenResp.Data.Token
	kucoin.pingInterval = time.Duration(tokenResp.Data.InstanceServers[0].PingInterval) * time.Millisecond

	return kucoin, kucoin.connect()
}

func (k *KuCoinExchange) GetName() string {
	return "KuCoin"
}

// Close closes the WebSocket connection and stops all goroutines
func (k *KuCoinExchange) Close() {
	if k.socket != nil && k.socket.IsConnected() {
		k.socket.Close()
	}
}

// SubscribeToOrderBook subscribes to order book updates for a specific coin
func (k *KuCoinExchange) SubscribeToOrderBook(coin string) error {
	if k.socket == nil || !k.socket.IsConnected() {
		return fmt.Errorf("websocket not connected")
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	// Changed to use level2Depth5 topic
	topic := fmt.Sprintf("/spotMarket/level2Depth5:%s-USDT", coin)

	subscription := map[string]interface{}{
		"id":             "1",
		"type":           "subscribe",
		"topic":          topic,
		"privateChannel": k.isPrivate,
		"response":       true,
	}

	// Initialize the order book for this coin
	k.orderBooks[coin] = &OrderBookData{
		Book: exchange.OrderBook{},
	}

	subscriptionJSON, err := json.Marshal(subscription)
	if err != nil {
		return fmt.Errorf("error marshaling subscription: %w", err)
	}

	k.logger.Infof("Subscribing to KuCoin order book for %s", coin)
	return k.socket.SendText(string(subscriptionJSON))
}

// GetOrderBook retrieves the current order book for a coin
func (k *KuCoinExchange) GetOrderBook(coin string) (*exchange.OrderBook, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()

	orderBookData, exists := k.orderBooks[coin]
	if !exists {
		return nil, fmt.Errorf("no order book for %s", coin)
	}

	// Check if data is stale
	if time.Since(orderBookData.Book.Timestamp) > k.maxStaleData {
		return nil, fmt.Errorf("stale order book for %s (last updated %s ago)",
			coin, time.Since(orderBookData.Book.Timestamp).String())
	}

	return &orderBookData.Book, nil
}

// connect establishes a WebSocket connection to KuCoin
func (k *KuCoinExchange) connect() error {
	// Set up event handlers
	k.setupEventHandlers()

	// Establish connection
	var connErr error
	k.socket.On(websocket.EventConnectError, func(event websocket.Event) {
		if err, ok := event.Data.(error); ok && err != nil {
			connErr = err
			return
		}

		connErr = fmt.Errorf("establish connection with kucoin unknown error")
	})

	k.socket.Connect(k.endpoint)

	return connErr
}

// setupEventHandlers configures all WebSocket event handlers
func (k *KuCoinExchange) setupEventHandlers() {
	// Handle connection events
	k.socket.On(websocket.EventConnected, func(event websocket.Event) {
		k.logger.Infof("Connected to KuCoin WebSocket")

		// Start ping loop when connected
		go k.pingLoop()
	})

	k.socket.On(websocket.EventConnectError, func(event websocket.Event) {
		err := event.Data.(error)
		k.logger.Errorf("Failed to connect to KuCoin WebSocket: %v", err)
	})

	k.socket.On(websocket.EventDisconnected, func(event websocket.Event) {
		var errMsg string
		if err, ok := event.Data.(error); ok && err != nil {
			errMsg = err.Error()
		} else {
			errMsg = "unknown reason"
		}
		k.logger.Warnf("Disconnected from KuCoin WebSocket: %s", errMsg)

		// Try to reconnect if the context is still valid
		select {
		case <-k.ctx.Done():
			return
		default:
			go k.attemptReconnect()
		}
	})

	// Handle text messages
	k.socket.On(websocket.EventTextMessage, func(event websocket.Event) {
		message := event.Data.(string)
		k.handleMessage([]byte(message))
	})
}

// handleMessage processes incoming WebSocket messages
func (k *KuCoinExchange) handleMessage(message []byte) {
	// Try to parse as WsOrderBookResponse
	var l2Msg WsOrderBookResponse
	if err := json.Unmarshal(message, &l2Msg); err != nil {
		// Ignore non-orderbook messages
		return
	}

	// Check if it's a pong message
	if l2Msg.Type == "pong" {
		return
	}

	// Only process if it's a level2 message
	if l2Msg.Subject == "level2" {
		k.updateOrderBook(&l2Msg)
	}
}

// updateOrderBook processes order book updates
func (k *KuCoinExchange) updateOrderBook(msg *WsOrderBookResponse) {
	// Extract coin from topic
	parts := strings.Split(msg.Topic, ":")
	if len(parts) != 2 {
		k.logger.Errorf("Invalid topic format: %s", msg.Topic)
		return
	}

	coin := strings.TrimSuffix(parts[1], "-USDT")

	k.mu.Lock()
	defer k.mu.Unlock()

	// Initialize the order book entry if it doesn't exist
	if _, exists := k.orderBooks[coin]; !exists {
		k.orderBooks[coin] = &OrderBookData{
			Book: exchange.OrderBook{},
		}
	}

	newBook := k.processOrderBookLevels(msg)
	newBook.Timestamp = time.Now()
	k.orderBooks[coin].Book = newBook
}

// processOrderBookLevels converts the raw order book data to our internal format
func (k *KuCoinExchange) processOrderBookLevels(msg *WsOrderBookResponse) exchange.OrderBook {
	newBook := exchange.OrderBook{
		Bids: make([]exchange.Order, 0, len(msg.Data.Bids)),
		Asks: make([]exchange.Order, 0, len(msg.Data.Asks)),
	}

	// Process bids
	for _, bid := range msg.Data.Bids {
		if len(bid) < 2 {
			continue
		}

		price, err := strconv.ParseFloat(bid[0], 64)
		if err != nil {
			k.logger.Errorf("Error parsing bid price %s: %v", bid[0], err)
			continue
		}

		size, err := strconv.ParseFloat(bid[1], 64)
		if err != nil {
			k.logger.Errorf("Error parsing bid size %s: %v", bid[1], err)
			continue
		}

		newBook.Bids = append(newBook.Bids, exchange.Order{
			Price:    price,
			Amount:   size,
			Exchange: "KuCoin",
		})
	}

	// Process asks
	for _, ask := range msg.Data.Asks {
		if len(ask) < 2 {
			continue
		}

		price, err := strconv.ParseFloat(ask[0], 64)
		if err != nil {
			k.logger.Errorf("Error parsing ask price %s: %v", ask[0], err)
			continue
		}

		size, err := strconv.ParseFloat(ask[1], 64)
		if err != nil {
			k.logger.Errorf("Error parsing ask size %s: %v", ask[1], err)
			continue
		}

		newBook.Asks = append(newBook.Asks, exchange.Order{
			Price:    price,
			Amount:   size,
			Exchange: "KuCoin",
		})
	}

	return newBook
}

// pingLoop sends ping messages at the required interval
func (k *KuCoinExchange) pingLoop() {
	ticker := time.NewTicker(k.pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-k.ctx.Done():
			return
		case <-ticker.C:
			pingMsg := map[string]string{"id": "ping", "type": "ping"}
			pingJSON, err := json.Marshal(pingMsg)
			if err != nil {
				k.logger.Errorf("Failed to marshal ping message: %v", err)
				continue
			}

			if err := k.socket.SendText(string(pingJSON)); err != nil {
				k.logger.Errorf("Failed to send ping: %v", err)
				return
			}
		}
	}
}

// attemptReconnect tries to reestablish the WebSocket connection
func (k *KuCoinExchange) attemptReconnect() {
	maxRetries := 5
	backoff := 1 * time.Second

	for retry := 0; retry < maxRetries; retry++ {
		k.logger.Warnf("Attempting to reconnect to KuCoin WebSocket (attempt %d/%d)", retry+1, maxRetries)

		time.Sleep(backoff)

		// Exponential backoff
		backoff *= 2

		// Get a new token before reconnecting
		tokenResp, err := GetToken(k.apiKey, k.apiSecret, k.passphrase, k.isPrivate)
		if err != nil {
			k.logger.Errorf("Failed to get KuCoin token for reconnection: %v", err)
			continue
		}

		if len(tokenResp.Data.InstanceServers) == 0 {
			k.logger.Errorf("No instance servers returned from KuCoin for reconnection")
			continue
		}

		k.endpoint = tokenResp.Data.InstanceServers[0].Endpoint + "?token=" + tokenResp.Data.Token
		k.token = tokenResp.Data.Token
		k.pingInterval = time.Duration(tokenResp.Data.InstanceServers[0].PingInterval) * time.Millisecond

		err = k.connect()
		if err == nil {
			k.logger.Infof("Successfully reconnected to KuCoin WebSocket")

			// Resubscribe to all previous order books
			k.mu.RLock()
			coins := make([]string, 0, len(k.orderBooks))
			for coin := range k.orderBooks {
				coins = append(coins, coin)
			}
			k.mu.RUnlock()

			for _, coin := range coins {
				if err := k.SubscribeToOrderBook(coin); err != nil {
					k.logger.Errorf("Failed to resubscribe to %s: %v", coin, err)
				}
			}

			return
		}

		k.logger.Errorf("Failed to reconnect: %v", err)
	}

	k.logger.Errorf("Failed to reconnect after %d attempts", maxRetries)
}
