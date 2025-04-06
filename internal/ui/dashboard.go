package ui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mum4k/termdash"
	"github.com/mum4k/termdash/cell"
	"github.com/mum4k/termdash/container"
	"github.com/mum4k/termdash/terminal/tcell"
	"github.com/mum4k/termdash/terminal/terminalapi"
)

// Constants for dashboard configuration
const maxHistorySize = 50

// ArbitrageDashboard manages the UI and data for crypto arbitrage visualization
type ArbitrageDashboard struct {
	// UI components
	widgets  *dashboardWidgets
	layouter *dashboardLayouter

	// Data channels with increased buffer to reduce blocking
	updateChan chan CoinData
	closeChan  chan struct{}

	// State data
	spreadsHistory map[string][]float64
	profits        map[string]float64
	coins          []string
	chartColors    []cell.Color
	selectedCoin   string
	mode           ChartMode

	// Synchronization for updates and redrawing
	dataMu      sync.RWMutex // for data protection
	widgetMu    sync.Mutex   // for widget protection
	updating    bool         // signals that an update is in progress
	batchUpdate bool         // indicates if we're in batch update mode
}

// NewArbitrageDashboard creates a new dashboard instance for the given coins
func NewArbitrageDashboard(coins []string) *ArbitrageDashboard {
	chartColors := []cell.Color{
		cell.ColorGreen,
		cell.ColorBlue,
		cell.ColorCyan,
		cell.ColorMagenta,
		cell.ColorYellow,
	}

	dashboard := &ArbitrageDashboard{
		coins:          coins,
		chartColors:    chartColors,
		updateChan:     make(chan CoinData, 1000),
		closeChan:      make(chan struct{}),
		spreadsHistory: make(map[string][]float64),
		profits:        make(map[string]float64),
		mode:           ModeAll,
		batchUpdate:    false,
	}

	// Initialize widgets and layouter
	dashboard.widgets = newDashboardWidgets(dashboard)
	dashboard.layouter = newDashboardLayouter(dashboard)

	return dashboard
}

// Init initializes all dashboard UI widgets
func (ad *ArbitrageDashboard) Init() error {
	return ad.widgets.initAllWidgets()
}

// StartBatchUpdates initiates a batch update mode for greater efficiency
func (ad *ArbitrageDashboard) StartBatchUpdates() {
	ad.widgetMu.Lock()
	ad.batchUpdate = true
	ad.widgetMu.Unlock()
}

// EndBatchUpdates terminates batch update mode and updates all charts
func (ad *ArbitrageDashboard) EndBatchUpdates() {
	ad.widgetMu.Lock()
	defer ad.widgetMu.Unlock()

	ad.batchUpdate = false

	if !ad.updating {
		ad.updating = true
		ad.widgets.updateAllCharts()
		ad.updating = false
	}
}

// scheduleUIUpdate schedules a UI update to happen in a separate goroutine
func (ad *ArbitrageDashboard) scheduleUIUpdate() {
	go func() {
		// Small pause to allow grouping of updates
		time.Sleep(50 * time.Millisecond)

		ad.dataMu.RLock()
		defer ad.dataMu.RUnlock()

		ad.widgetMu.Lock()
		defer ad.widgetMu.Unlock()

		if !ad.updating {
			ad.updating = true
			defer func() { ad.updating = false }()

			ad.widgets.updateAllCharts()
		}
	}()
}

// StartUpdateListener starts the goroutine for processing coin updates
func (ad *ArbitrageDashboard) StartUpdateListener(ctx context.Context) {
	go func() {
		// Update buffer to avoid UI overload
		updateBuffer := make([]CoinData, 0, 10)
		updateTicker := time.NewTicker(200 * time.Millisecond)
		defer updateTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case coinData := <-ad.updateChan:
				updateBuffer = append(updateBuffer, coinData)

			case <-updateTicker.C:
				if len(updateBuffer) > 0 {
					ad.StartBatchUpdates()

					for _, data := range updateBuffer {
						ad.processCoinUpdate(data)
					}

					updateBuffer = updateBuffer[:0]
					ad.EndBatchUpdates()
				}

			case <-ad.closeChan:
				return
			}
		}
	}()
}

// processCoinUpdate handles new coin data updates with improved synchronization
func (ad *ArbitrageDashboard) processCoinUpdate(coinData CoinData) {
	ad.dataMu.Lock()
	// Update internal data
	ad.updateProfitData(coinData)
	ad.updateSpreadHistory(coinData)
	ad.dataMu.Unlock()

	// Update widgets with clear locking pattern
	ad.widgetMu.Lock()
	ad.widgets.updateCoinWidget(coinData)
	ad.widgetMu.Unlock()

	// Schedule UI update after a short delay to batch updates
	go func() {
		time.Sleep(100 * time.Millisecond)
		ad.widgetMu.Lock()
		if !ad.updating {
			ad.updating = true
			ad.widgets.updateAllCharts()
			ad.updating = false
		}
		ad.widgetMu.Unlock()
	}()
}

// updateProfitData updates the profit data for a specific coin
func (ad *ArbitrageDashboard) updateProfitData(coinData CoinData) {
	ad.profits[coinData.Symbol] = 100
}

// updateSpreadHistory updates the spread history for a specific coin
func (ad *ArbitrageDashboard) updateSpreadHistory(coinData CoinData) {
	history := ad.spreadsHistory[coinData.Symbol]
	if len(history) >= maxHistorySize {
		history = history[1:]
	}
	ad.spreadsHistory[coinData.Symbol] = append(history, coinData.Spread)
}

// SendCoinData sends coin data to the dashboard for processing
func (ad *ArbitrageDashboard) SendCoinData(data CoinData) {
	select {
	case ad.updateChan <- data:
		// Data sent successfully
	default:
		// Channel buffer full, prevent lock
	}
}

// Close shuts down the dashboard cleanly
func (ad *ArbitrageDashboard) Close() {
	close(ad.closeChan)
}

// RunDashboard starts and runs the dashboard terminal UI
func RunDashboard(ctx context.Context, ad *ArbitrageDashboard, updateUIInterval time.Duration) error {
	t, err := tcell.New(tcell.ColorMode(terminalapi.ColorMode256))
	if err != nil {
		return fmt.Errorf("failed to initialize terminal: %w", err)
	}
	defer t.Close()

	gridOpts, err := ad.layouter.CreateLayout()
	if err != nil {
		return fmt.Errorf("failed to create layout: %w", err)
	}

	c, err := container.New(t, gridOpts...)
	if err != nil {
		return fmt.Errorf("failed to create root container: %w", err)
	}

	// Make sure to properly clean up on exit
	dashCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer ad.Close()

	return termdash.Run(dashCtx, t, c, termdash.RedrawInterval(updateUIInterval))
}
