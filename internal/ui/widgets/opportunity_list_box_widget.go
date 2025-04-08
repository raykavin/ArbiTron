package widgets

import (
	"fmt"
	"sync"
	"time"

	"github.com/mum4k/termdash/widgets/text"
)

// ListBoxItem represents an item in the opportunities list
type ListBoxItem struct {
	Symbol          string
	BuyExchange     string
	SellExchange    string
	Profit          float64
	PotentialProfit float64
	Timestamp       time.Time
}

// OpportunityListBox is a custom list box for arbitrage opportunities
type OpportunityListBox struct {
	*text.Text
	items []ListBoxItem
	mu    sync.Mutex
}

// NewOpportunityListBox creates a new opportunities list box
func NewOpportunityListBox() (*OpportunityListBox, error) {
	textWidget, err := text.New(text.RollContent(), text.WrapAtWords())
	if err != nil {
		return nil, err
	}

	return &OpportunityListBox{
		Text:  textWidget,
		items: make([]ListBoxItem, 0, 50),
	}, nil
}

// AddOpportunity adds a new arbitrage opportunity to the list
func (olb *OpportunityListBox) AddOpportunity(item ListBoxItem) {
	olb.mu.Lock()
	defer olb.mu.Unlock()

	// Add to beginning of list
	olb.items = append([]ListBoxItem{item}, olb.items...)

	// Trim list if it gets too long
	if len(olb.items) > 50 {
		olb.items = olb.items[:50]
	}

	olb.Refresh()
}

// Refresh updates the displayed list
func (olb *OpportunityListBox) Refresh() {
	olb.Reset()

	olb.Write("═══════════════════ ARBITRAGE OPPORTUNITIES ═══════════════════\n")
	olb.Write("SYMBOL  BUY      SELL     PROFIT    POTENTIAL    TIME\n")
	olb.Write("──────────────────────────────────────────────────────────────\n")

	for i, item := range olb.items {
		timeStr := item.Timestamp.Format("15:04:05")
		olb.Write(fmt.Sprintf("%-6s  %-8s %-8s %6.2f%%   $%-9.2f %s\n",
			item.Symbol,
			item.BuyExchange,
			item.SellExchange,
			item.Profit,
			item.PotentialProfit,
			timeStr))

		if i < len(olb.items)-1 {
			olb.Write("──────────────────────────────────────────────────────────────\n")
		}
	}
}
