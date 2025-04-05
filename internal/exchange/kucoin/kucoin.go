// internal/exchange/kucoin/kucoin.go
package kucoin

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"notlelouch/ArbiBot/internal/exchange"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// OrderBookEntry contains an order book and its last update timestamp
type OrderBookEntry struct {
	Book      exchange.OrderBook
	Timestamp time.Time
}

// Level2Depth5Message represents the structure for level2depth5 messages
type Level2Depth5Message struct {
	Type    string `json:"type"`
	Topic   string `json:"topic"`
	Subject string `json:"subject"`
	Data    struct {
		Asks      [][]string `json:"asks"`      // [price, size]
		Bids      [][]string `json:"bids"`      // [price, size]
		Timestamp int64      `json:"timestamp"` // milliseconds
	} `json:"data"`
}

// KuCoinWS handles the WebSocket connection to KuCoin exchange
type KuCoinWS struct {
	conn          *websocket.Conn
	orderBooks    map[string]*OrderBookEntry
	endpoint      string
	token         string
	pingInterval  time.Duration
	mu            sync.RWMutex
	done          chan struct{}
	reconnectCh   chan struct{}
	subscriptions []string
	maxStaleData  time.Duration
}

// NewKuCoinWS creates a new KuCoin WebSocket connection handler
func NewKuCoinWS(tokenResp *TokenResponse) *KuCoinWS {
	return &KuCoinWS{
		endpoint:      tokenResp.Data.InstanceServers[0].Endpoint,
		token:         tokenResp.Data.Token,
		pingInterval:  time.Duration(tokenResp.Data.InstanceServers[0].PingInterval) * time.Millisecond,
		orderBooks:    make(map[string]*OrderBookEntry),
		done:          make(chan struct{}),
		reconnectCh:   make(chan struct{}, 1),
		subscriptions: make([]string, 0),
		maxStaleData:  2 * time.Second,
	}
}

// Connect establishes a WebSocket connection to KuCoin
func (k *KuCoinWS) Connect(ctx context.Context) error {
	if err := k.dial(ctx); err != nil {
		return err
	}

	// Start management goroutines
	go k.reconnectManager(ctx)
	go k.pingLoop(ctx)
	go k.handleMessages(ctx)

	return nil
}

// dial establishes the actual WebSocket connection
func (k *KuCoinWS) dial(ctx context.Context) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
	}

	connURL := fmt.Sprintf("%s?token=%s", k.endpoint, k.token)
	log.Printf("Connecting to KuCoin WebSocket at %s", k.endpoint)

	conn, _, err := dialer.DialContext(ctx, connURL, nil)
	if err != nil {
		return fmt.Errorf("WebSocket connection failed: %w", err)
	}

	k.conn = conn
	log.Println("Successfully connected to KuCoin WebSocket")

	// Resubscribe to previous subscriptions if any
	for _, topic := range k.subscriptions {
		if err := k.Subscribe(topic, false); err != nil {
			log.Printf("Failed to resubscribe to %s: %v", topic, err)
		}
	}

	return nil
}

// SubscribeToOrderBook subscribes to order book updates for a specific coin
func (k *KuCoinWS) SubscribeToOrderBook(coin string) error {
	topic := fmt.Sprintf("/spotMarket/level2Depth5:%s-USDT", coin)

	k.mu.Lock()
	// Initialize the order book entry
	k.orderBooks[coin] = &OrderBookEntry{
		Book:      exchange.OrderBook{},
		Timestamp: time.Time{}, // Zero time indicates no data yet
	}
	k.mu.Unlock()

	if err := k.Subscribe(topic, false); err != nil {
		return fmt.Errorf("failed to subscribe to KuCoin order book: %w", err)
	}

	// Add to subscriptions list for reconnection purposes
	k.mu.Lock()
	k.subscriptions = append(k.subscriptions, topic)
	k.mu.Unlock()

	log.Printf("Successfully subscribed to KuCoin level2Depth5 for %s", coin)
	return nil
}

// GetOrderBook retrieves the current order book for a specific coin
func (k *KuCoinWS) GetOrderBook(coin string) (exchange.OrderBook, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()

	entry, exists := k.orderBooks[coin]
	if !exists {
		return exchange.OrderBook{}, fmt.Errorf("no order book available for %s", coin)
	}

	// Check if data is stale
	if entry.Timestamp.IsZero() || time.Since(entry.Timestamp) > k.maxStaleData {
		return exchange.OrderBook{}, fmt.Errorf("stale order book data for %s (last update: %v)",
			coin, entry.Timestamp)
	}

	return entry.Book, nil
}

// Close properly closes the WebSocket connection
func (k *KuCoinWS) Close() error {
	close(k.done)
	if k.conn != nil {
		return k.conn.Close()
	}
	return nil
}

// reconnectManager handles reconnection logic
func (k *KuCoinWS) reconnectManager(ctx context.Context) {
	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			return
		case <-k.done:
			return
		case <-k.reconnectCh:
			log.Println("Attempting to reconnect to KuCoin WebSocket...")

			// Close previous connection if exists
			if k.conn != nil {
				k.conn.Close()
				k.conn = nil
			}

			// Try to reconnect
			err := k.dial(ctx)
			if err != nil {
				log.Printf("Reconnection failed: %v, retrying in %v", err, backoff)
				time.Sleep(backoff)

				// Exponential backoff with a cap
				backoff *= 2
				if backoff > 30*time.Second {
					backoff = 30 * time.Second
				}

				// Try again
				k.reconnectCh <- struct{}{}
			} else {
				// Reset backoff on successful connection
				backoff = time.Second
			}
		}
	}
}

// pingLoop sends periodic ping messages to keep the connection alive
func (k *KuCoinWS) pingLoop(ctx context.Context) {
	ticker := time.NewTicker(k.pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-k.done:
			return
		case <-ticker.C:
			if k.conn == nil {
				continue
			}

			if err := k.conn.WriteJSON(map[string]string{"id": "ping", "type": "ping"}); err != nil {
				log.Printf("Ping failed: %v, triggering reconnection", err)
				select {
				case k.reconnectCh <- struct{}{}:
					// Signal sent
				default:
					// Channel buffer full, reconnection already in progress
				}
			}
		}
	}
}

// handleMessages processes incoming WebSocket messages
func (k *KuCoinWS) handleMessages(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-k.done:
			return
		default:
			if k.conn == nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}

			_, message, err := k.conn.ReadMessage()
			if err != nil {
				log.Printf("KuCoin WebSocket read error: %v", err)
				select {
				case k.reconnectCh <- struct{}{}:
					// Signal sent
				default:
					// Channel buffer full, reconnection already in progress
				}
				return
			}

			// Process the message
			if err := k.processMessage(message); err != nil {
				log.Printf("Error processing message: %v", err)
			}
		}
	}
}

// processMessage handles different types of messages received from the WebSocket
func (k *KuCoinWS) processMessage(message []byte) error {
	// Try to parse as Level2Depth5Message
	var l2Msg Level2Depth5Message
	if err := json.Unmarshal(message, &l2Msg); err != nil {
		// Not a level2depth5 message, might be another type we don't care about
		return nil
	}

	// Only process if it's a level2 message
	if l2Msg.Subject == "level2" {
		return k.updateOrderBook(&l2Msg)
	}

	return nil
}

// updateOrderBook processes order book updates from the WebSocket
func (k *KuCoinWS) updateOrderBook(msg *Level2Depth5Message) error {
	// Extract coin from topic
	parts := strings.Split(msg.Topic, ":")
	if len(parts) != 2 {
		return fmt.Errorf("invalid topic format: %s", msg.Topic)
	}

	coin := strings.TrimSuffix(parts[1], "-USDT")

	// Create new slices for bids and asks
	newBids := make([]exchange.Order, 0, len(msg.Data.Bids))
	newAsks := make([]exchange.Order, 0, len(msg.Data.Asks))

	// Process bids
	for _, bid := range msg.Data.Bids {
		if len(bid) < 2 {
			continue
		}

		price, err := strconv.ParseFloat(bid[0], 64)
		if err != nil {
			continue
		}

		size, err := strconv.ParseFloat(bid[1], 64)
		if err != nil {
			continue
		}

		newBids = append(newBids, exchange.Order{
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
			continue
		}

		size, err := strconv.ParseFloat(ask[1], 64)
		if err != nil {
			continue
		}

		newAsks = append(newAsks, exchange.Order{
			Price:    price,
			Amount:   size,
			Exchange: "KuCoin",
		})
	}

	// Update the order book with thread safety
	k.mu.Lock()
	defer k.mu.Unlock()

	entry, exists := k.orderBooks[coin]
	if !exists {
		// This can happen if we receive data for a coin we haven't explicitly subscribed to
		k.orderBooks[coin] = &OrderBookEntry{
			Book: exchange.OrderBook{
				Bids: newBids,
				Asks: newAsks,
			},
			Timestamp: time.Now(),
		}
		return nil
	}

	// Update existing entry
	entry.Book = exchange.OrderBook{
		Bids: newBids,
		Asks: newAsks,
	}
	entry.Timestamp = time.Now()

	return nil
}

// Subscribe sends a subscription request for a specific topic
func (k *KuCoinWS) Subscribe(topic string, privateChannel bool) error {
	if k.conn == nil {
		return fmt.Errorf("websocket connection not established")
	}

	subscription := map[string]interface{}{
		"id":             time.Now().UnixNano(), // Use unique ID for each subscription
		"type":           "subscribe",
		"topic":          topic,
		"privateChannel": privateChannel,
		"response":       true,
	}

	return k.conn.WriteJSON(subscription)
}
