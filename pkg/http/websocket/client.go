package websocket

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ConnectionOptions holds configuration for the WebSocket connection
type ConnectionOptions struct {
	UseCompression bool
	UseSSL         bool
	Subprotocols   []string
	ProxyHandler   func(*http.Request) (*url.URL, error)
	RequestHeader  http.Header
	Timeout        time.Duration
}

// Option is a function that configures ConnectionOptions
type Option func(*ConnectionOptions)

// WithCompression enables WebSocket compression
func WithCompression() Option {
	return func(co *ConnectionOptions) {
		co.UseCompression = true
	}
}

// WithSSL configures SSL/TLS settings
func WithSSL(enabled bool) Option {
	return func(co *ConnectionOptions) {
		co.UseSSL = enabled
	}
}

// WithProxyHandler sets a proxy handler for the connection
func WithProxyHandler(handler func(*http.Request) (*url.URL, error)) Option {
	return func(co *ConnectionOptions) {
		co.ProxyHandler = handler
	}
}

// WithSubprotocols sets WebSocket subprotocols
func WithSubprotocols(subProtocols []string) Option {
	return func(co *ConnectionOptions) {
		co.Subprotocols = subProtocols
	}
}

// WithRequestHeader sets custom HTTP headers for the WebSocket handshake
func WithRequestHeader(header http.Header) Option {
	return func(co *ConnectionOptions) {
		co.RequestHeader = header
	}
}

// WithTimeout sets read timeout duration
func WithTimeout(timeout time.Duration) Option {
	return func(co *ConnectionOptions) {
		co.Timeout = timeout
	}
}

// Socket represents a WebSocket connection
type Socket struct {
	conn          *websocket.Conn
	dialer        *websocket.Dialer
	url           string
	options       ConnectionOptions
	eventHandlers map[EventType]EventHandler

	// Mutex for thread-safe operations
	mu     sync.RWMutex
	sendMu sync.Mutex

	// State
	isConnected bool
	isClosed    bool
}

// NewClient creates a new WebSocket client with the provided options
func NewClient(opts ...Option) *Socket {
	options := ConnectionOptions{
		UseSSL:        true,
		RequestHeader: http.Header{},
	}

	for _, opt := range opts {
		opt(&options)
	}

	return &Socket{
		options:       options,
		dialer:        &websocket.Dialer{},
		eventHandlers: make(map[EventType]EventHandler),
	}
}

// On registers an event handler for a specific event type
func (s *Socket) On(eventType EventType, handler EventHandler) *Socket {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.eventHandlers[eventType] = handler
	return s
}

// emitEvent triggers the appropriate event handler if registered
func (s *Socket) emitEvent(eventType EventType, data any) {
	s.mu.RLock()
	handler, exists := s.eventHandlers[eventType]
	s.mu.RUnlock()

	if exists && handler != nil {
		handler(Event{
			Type:   eventType,
			Data:   data,
			Socket: s,
		})
	}
}

// configureDialer sets up the dialer with the connection options
func (s *Socket) configureDialer() {
	s.dialer.EnableCompression = s.options.UseCompression
	s.dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: !s.options.UseSSL}
	s.dialer.Proxy = s.options.ProxyHandler
	s.dialer.Subprotocols = s.options.Subprotocols
}

// Connect establishes a WebSocket connection
func (s *Socket) Connect(url string) {
	s.mu.Lock()
	if s.isConnected {
		s.mu.Unlock()
		s.emitEvent(EventConnectError, errors.New("socket is already connected"))
	}

	if s.isClosed {
		s.mu.Unlock()
		s.emitEvent(EventConnectError, errors.New("socket is closed and cannot be reused"))
		return
	}

	s.url = url
	s.mu.Unlock()

	s.configureDialer()

	var err error
	s.conn, _, err = s.dialer.Dial(s.url, s.options.RequestHeader)
	if err != nil {
		s.emitEvent(EventConnectError, err)
		return
	}

	s.mu.Lock()
	s.isConnected = true
	s.mu.Unlock()

	s.emitEvent(EventConnected, nil)

	// Set up handlers
	s.setupHandlers()

	// Start message reading loop
	go s.readLoop()

}

// setupHandlers configures the WebSocket event handlers
func (s *Socket) setupHandlers() {
	// Set up ping handler
	defaultPingHandler := s.conn.PingHandler()
	s.conn.SetPingHandler(func(appData string) error {
		s.emitEvent(EventPing, appData)
		return defaultPingHandler(appData)
	})

	// Set up pong handler
	defaultPongHandler := s.conn.PongHandler()
	s.conn.SetPongHandler(func(appData string) error {
		s.emitEvent(EventPong, appData)
		return defaultPongHandler(appData)
	})

	// Set up close handler
	defaultCloseHandler := s.conn.CloseHandler()
	s.conn.SetCloseHandler(func(code int, text string) error {
		result := defaultCloseHandler(code, text)

		s.mu.Lock()
		wasConnected := s.isConnected
		s.isConnected = false
		s.mu.Unlock()

		if wasConnected {
			closeErr := fmt.Errorf("connection closed: %d %s", code, text)
			s.emitEvent(EventDisconnected, closeErr)
		}

		return result
	})
}

// readLoop continuously reads messages from the WebSocket
func (s *Socket) readLoop() {
	for {
		// Check if we're still connected
		s.mu.RLock()
		if !s.isConnected {
			s.mu.RUnlock()
			return
		}

		timeout := s.options.Timeout
		s.mu.RUnlock()

		// Set read deadline if timeout is specified
		if timeout > 0 {
			s.conn.SetReadDeadline(time.Now().Add(timeout))
		}

		messageType, message, err := s.conn.ReadMessage()
		if err != nil {
			s.mu.Lock()
			if s.isConnected {
				s.isConnected = false
				s.mu.Unlock()
				s.emitEvent(EventDisconnected, err)
			} else {
				s.mu.Unlock()
			}
			return
		}

		// Process message based on type
		switch messageType {
		case websocket.TextMessage:
			s.emitEvent(EventTextMessage, string(message))
		case websocket.BinaryMessage:
			s.emitEvent(EventBinaryMessage, message)
		}
	}
}

// IsConnected returns the current connection status
func (s *Socket) IsConnected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isConnected
}

// SendText sends a text message
func (s *Socket) SendText(message string) error {
	return s.send(websocket.TextMessage, []byte(message))
}

// SendBinary sends a binary message
func (s *Socket) SendBinary(data []byte) error {
	return s.send(websocket.BinaryMessage, data)
}

// send is a helper method to send WebSocket messages
func (s *Socket) send(messageType int, data []byte) error {
	s.mu.RLock()
	if !s.isConnected {
		s.mu.RUnlock()
		return errors.New("cannot send on a disconnected socket")
	}
	s.mu.RUnlock()

	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	return s.conn.WriteMessage(messageType, data)
}

// Close gracefully closes the WebSocket connection
func (s *Socket) Close() error {
	s.mu.Lock()
	if !s.isConnected {
		s.mu.Unlock()
		return errors.New("socket is not connected")
	}

	if s.isClosed {
		s.mu.Unlock()
		return errors.New("socket is already closed")
	}

	s.isConnected = false
	s.isClosed = true
	s.mu.Unlock()

	// Send close message
	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")

	s.sendMu.Lock()
	err := s.conn.WriteMessage(websocket.CloseMessage, closeMsg)
	s.sendMu.Unlock()

	if err != nil {
		closeErr := fmt.Errorf("write close: %w", err)
		s.emitEvent(EventDisconnected, closeErr)
		return closeErr
	}

	// Close the underlying connection
	err = s.conn.Close()
	if err != nil {
		s.emitEvent(EventDisconnected, err)
	} else {
		s.emitEvent(EventDisconnected, nil)
	}

	return err
}
