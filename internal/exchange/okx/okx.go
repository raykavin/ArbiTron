// internal/exchange/okx/okx.go
package okx

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/raykavin/ArbiTron/internal/exchange"
)

const (
	okxWSURL      = "wss://ws.okx.com:8443/ws/v5/public"
	staleDuration = 2 * time.Second
)

// OKXWS gerencia a comunicação WebSocket com a OKX
type OKXWS struct {
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	orderBooks map[string]*exchange.OrderBook
	mu         sync.RWMutex
}

// wsSubscriptionMessage representa a mensagem de inscrição no WebSocket
type wsSubscriptionMessage struct {
	Op   string              `json:"op"`
	Args []wsSubscriptionArg `json:"args"`
}

// wsSubscriptionArg representa os argumentos para a inscrição no WebSocket
type wsSubscriptionArg struct {
	Channel string `json:"channel"`
	InstID  string `json:"instId"`
}

// wsOrderBookResponse representa a resposta do WebSocket para o livro de ofertas
type wsOrderBookResponse struct {
	Arg  wsSubscriptionArg `json:"arg"`
	Data []struct {
		Asks      [][]string `json:"asks"`
		Bids      [][]string `json:"bids"`
		Timestamp string     `json:"ts"`
	} `json:"data"`
}

// NewOKXWS cria uma nova instância do cliente WebSocket da OKX
func NewOKXWS() *OKXWS {
	ctx, cancel := context.WithCancel(context.Background())
	return &OKXWS{
		ctx:        ctx,
		cancel:     cancel,
		orderBooks: make(map[string]*exchange.OrderBook),
	}
}

// GetName retorna o nome da exchange
func (o *OKXWS) GetName() string {
	return "OKX"
}

// Connect estabelece a conexão WebSocket com a OKX
func (o *OKXWS) Connect(ctx context.Context) error {
	conn, _, err := websocket.DefaultDialer.Dial(okxWSURL, nil)
	if err != nil {
		return fmt.Errorf("falha ao conectar ao WebSocket da OKX: %w", err)
	}

	o.conn = conn
	go o.handleMessages()

	return nil
}

// SubscribeToOrderBook inscreve-se para atualizações do livro de ofertas de um símbolo específico
func (o *OKXWS) SubscribeToOrderBook(symbol string) error {
	if o.conn == nil {
		return fmt.Errorf("WebSocket não conectado")
	}

	subscription := wsSubscriptionMessage{
		Op: "subscribe",
		Args: []wsSubscriptionArg{
			{
				Channel: "books",
				InstID:  symbol,
			},
		},
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	o.orderBooks[symbol] = &exchange.OrderBook{}

	return o.conn.WriteJSON(subscription)
}

// GetOrderBook retorna o livro de ofertas atual para um símbolo
func (o *OKXWS) GetOrderBook(symbol string) (*exchange.OrderBook, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	orderBook, exists := o.orderBooks[symbol]
	if !exists {
		return nil, fmt.Errorf("nenhum livro de ofertas para %s", symbol)
	}

	if time.Since(orderBook.Timestamp) > staleDuration {
		return nil, fmt.Errorf("livro de ofertas obsoleto para %s (última atualização há %s)", symbol, time.Since(orderBook.Timestamp))
	}

	return orderBook, nil
}

// Close encerra a conexão WebSocket e cancela todas as goroutines
func (o *OKXWS) Close() {
	o.cancel()
	if o.conn != nil {
		o.conn.Close()
	}
}

// handleMessages processa as mensagens recebidas do WebSocket
func (o *OKXWS) handleMessages() {
	for {
		select {
		case <-o.ctx.Done():
			return
		default:
			_, message, err := o.conn.ReadMessage()
			if err != nil {
				log.Printf("erro de leitura do WebSocket da OKX: %v", err)
				return
			}

			var response wsOrderBookResponse
			if err := json.Unmarshal(message, &response); err != nil {
				log.Printf("erro ao decodificar mensagem do WebSocket da OKX: %v", err)
				continue
			}

			if len(response.Data) == 0 {
				continue
			}

			o.mu.Lock()
			book := &exchange.OrderBook{
				Bids:      parseLevels(response.Data[0].Bids, "OKX"),
				Asks:      parseLevels(response.Data[0].Asks, "OKX"),
				Timestamp: time.Now(),
			}
			o.orderBooks[response.Arg.InstID] = book
			o.mu.Unlock()
		}
	}
}

// parseLevels converte os níveis de preço do formato da OKX para o formato interno
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
