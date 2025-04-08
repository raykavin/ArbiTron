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
	"github.com/mum4k/termdash/widgets/text"
	"github.com/raykavin/ArbiTron/internal/config"
)

// Constants for dashboard configuration
const maxHistorySize = 50

// ArbitrageDashboard manages the UI and data for crypto arbitrage visualization
type ArbitrageDashboard struct {
	// UI components
	widgets  *dashboardWidgets
	layouter *dashboardLayouter
	logger   *text.Text

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
	config         *config.Config // Store config for reference

	// Flashing widgets tracking
	flashingWidgets map[string]time.Time
	flashTicker     *time.Ticker
	flashMu         sync.Mutex

	// Synchronization for updates and redrawing
	dataMu      sync.RWMutex // for data protection
	widgetMu    sync.Mutex   // for widget protection
	updating    bool         // signals that an update is in progress
	batchUpdate bool         // indicates if we're in batch update mode
}

// NewArbitrageDashboard creates a new dashboard instance for the given coins
// NewArbitrageDashboard creates a new dashboard instance for the given coins
func NewArbitrageDashboard(cfg *config.Config) *ArbitrageDashboard {
	chartColors := []cell.Color{
		cell.ColorMagenta,
		cell.ColorBlue,
		cell.ColorMaroon,
		cell.ColorFuchsia,
		cell.ColorYellow,
	}

	dashboard := &ArbitrageDashboard{
		coins:           cfg.Coins,
		chartColors:     chartColors,
		updateChan:      make(chan CoinData, 1000),
		closeChan:       make(chan struct{}),
		spreadsHistory:  make(map[string][]float64),
		profits:         make(map[string]float64),
		mode:            ModeAll,
		batchUpdate:     false,
		flashingWidgets: make(map[string]time.Time),
		config:          cfg,
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

// addFlashingWidget adds a widget to the flashing list
func (ad *ArbitrageDashboard) addFlashingWidget(symbol string, flashUntil time.Time) {
	ad.flashMu.Lock()
	defer ad.flashMu.Unlock()

	ad.flashingWidgets[symbol] = flashUntil

	// Start flash ticker if not already running
	if ad.flashTicker == nil {
		ad.flashTicker = time.NewTicker(500 * time.Millisecond) // Flash every 500ms

		go func() {
			flashState := false
			for {
				select {
				case <-ad.flashTicker.C:
					flashState = !flashState // Toggle flash state

					// Update all flashing widgets
					ad.flashMu.Lock()

					// Check if any widgets should still be flashing
					now := time.Now()
					activeFlash := false

					for symbol, until := range ad.flashingWidgets {
						if now.After(until) {
							// Stop flashing this widget
							delete(ad.flashingWidgets, symbol)

							// Reset border color
							if _, ok := ad.widgets.coinWidgets[symbol]; ok {
								// Find the original color index
								colorIdx := 0
								for i, coin := range ad.coins {
									if coin == symbol {
										colorIdx = i
										break
									}
								}

								color := ad.chartColors[colorIdx%len(ad.chartColors)]
								// Reset widget appearance
								container.BorderTitle(fmt.Sprintf(" %s Arbitrage ", symbol))
								container.BorderColor(color)
							}
						} else {
							activeFlash = true

							// Update widget border based on flash state
							if widget, ok := ad.widgets.coinWidgets[symbol]; ok {
								if flashState {
									widget.Write("", text.WriteCellOpts(cell.BgColor(cell.ColorLime)))
								} else {
									// Find the original color index
									colorIdx := 0
									for i, coin := range ad.coins {
										if coin == symbol {
											colorIdx = i
											break
										}
									}

									color := ad.chartColors[colorIdx%len(ad.chartColors)]
									widget.Write("", text.WriteCellOpts(cell.BgColor(color)))
								}
							}
						}
					}

					ad.flashMu.Unlock()

					// If no widgets are still flashing, stop the ticker
					if !activeFlash {
						ad.flashTicker.Stop()
						ad.flashTicker = nil
						return
					}

				case <-ad.closeChan:
					return
				}
			}
		}()
	}
}

// StartUpdateListener starts the goroutine for processing coin updates
func (ad *ArbitrageDashboard) StartUpdateListener(ctx context.Context) {
	go func() {
		// Update buffer to avoid UI overload
		updateBuffer := make([]CoinData, 0, 10)
		// updateTicker := time.NewTicker(5 * time.Millisecond)
		// defer updateTicker.Stop()

		for {
			select {
			case <-ctx.Done():
			case <-ad.closeChan:
				return
			case coinData := <-ad.updateChan:
				updateBuffer = append(updateBuffer, coinData)
				if len(updateBuffer) > 0 {
					ad.StartBatchUpdates()

					for _, data := range updateBuffer {
						ad.processCoinUpdate(data)
					}

					updateBuffer = updateBuffer[:0]
					ad.EndBatchUpdates()
				}
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

func (ad *ArbitrageDashboard) GetLoggerWidget() *text.Text {
	return ad.logger
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

	// Log startup message
	// ad.LogMessage("Dashboard started")

	return termdash.Run(dashCtx, t, c, termdash.RedrawInterval(updateUIInterval))
}
