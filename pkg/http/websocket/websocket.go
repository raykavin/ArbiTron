package websocket

// EventType represents different WebSocket event types
type EventType int

const (
	EventConnected EventType = iota
	EventConnectError
	EventDisconnected
	EventPing
	EventPong
	EventTextMessage
	EventBinaryMessage
)

// Event represents a WebSocket event with its data
type Event struct {
	Socket *Socket
	Type   EventType
	Data   any // string, []byte, or error based on event type
}

// EventHandler is a function that handles WebSocket events
type EventHandler func(event Event)

type Client interface {
	Connect(url string)
	Close() error
	IsConnected() bool

	SendText(message string) error
	SendBinary(data []byte) error

	On(eventType EventType, handler EventHandler) *Socket
}
