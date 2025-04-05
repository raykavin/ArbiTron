package ui

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/mum4k/termdash"
	"github.com/mum4k/termdash/cell"
	"github.com/mum4k/termdash/container"
	"github.com/mum4k/termdash/container/grid"
	"github.com/mum4k/termdash/linestyle"
	"github.com/mum4k/termdash/terminal/tcell"
	"github.com/mum4k/termdash/terminal/terminalapi"
	"github.com/mum4k/termdash/widgets/barchart"
	"github.com/mum4k/termdash/widgets/button"
	"github.com/mum4k/termdash/widgets/linechart"
	"github.com/mum4k/termdash/widgets/text"
)

// Constants for dashboard configuration
const (
	redrawInterval = 250 * time.Millisecond
	maxHistorySize = 50
)

// ChartMode represents the current view mode of the line chart
type ChartMode int

const (
	ModeAll ChartMode = iota
	ModeSingle
)

// CoinData represents a single arbitrage opportunity data point
type CoinData struct {
	Timestamp    time.Time
	Symbol       string
	BuyExchange  string
	SellExchange string
	BuyPrice     float64
	SellPrice    float64
	Profit       float64
	Spread       float64
	LiquidProfit float64
}

// ArbitrageDashboard manages the UI and data for crypto arbitrage visualization
type ArbitrageDashboard struct {
	// UI widgets
	coinWidgets  map[string]*text.Text
	barChart     *barchart.BarChart
	lineChart    *linechart.LineChart
	chartButtons map[string]*button.Button

	// Data channels
	updateChan chan CoinData
	closeChan  chan struct{}

	// State data
	spreadsHistory map[string][]float64
	profits        map[string]float64
	coins          []string
	chartColors    []cell.Color
	selectedCoin   string
	mode           ChartMode

	// Synchronization
	mu sync.RWMutex
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

	return &ArbitrageDashboard{
		coins:          coins,
		chartColors:    chartColors,
		coinWidgets:    make(map[string]*text.Text),
		chartButtons:   make(map[string]*button.Button),
		updateChan:     make(chan CoinData, 100),
		closeChan:      make(chan struct{}),
		spreadsHistory: make(map[string][]float64),
		profits:        make(map[string]float64),
		mode:           ModeAll,
	}
}

// InitWidgets initializes all dashboard UI widgets
func (ad *ArbitrageDashboard) InitWidgets() error {
	if err := ad.initCoinWidgets(); err != nil {
		return fmt.Errorf("failed to initialize coin widgets: %w", err)
	}

	if err := ad.initBarChart(); err != nil {
		return fmt.Errorf("failed to initialize bar chart: %w", err)
	}

	if err := ad.initLineChart(); err != nil {
		return fmt.Errorf("failed to initialize line chart: %w", err)
	}

	if err := ad.initChartButtons(); err != nil {
		return fmt.Errorf("failed to initialize chart buttons: %w", err)
	}

	return nil
}

// initCoinWidgets initializes the text widgets for each coin
func (ad *ArbitrageDashboard) initCoinWidgets() error {
	for _, coin := range ad.coins {
		widget, err := text.New(text.RollContent(), text.WrapAtWords())
		if err != nil {
			return fmt.Errorf("failed to create text widget for %s: %w", coin, err)
		}
		ad.coinWidgets[coin] = widget
	}
	return nil
}

// initBarChart initializes the profit bar chart
func (ad *ArbitrageDashboard) initBarChart() error {
	barChart, err := barchart.New(
		barchart.BarColors(ad.chartColors),
		barchart.ShowValues(),
		barchart.Labels(ad.coins),
	)
	if err != nil {
		return fmt.Errorf("failed to create bar chart: %w", err)
	}
	ad.barChart = barChart
	return nil
}

// initLineChart initializes the spread history line chart
func (ad *ArbitrageDashboard) initLineChart() error {
	lineChart, err := linechart.New(
		linechart.AxesCellOpts(cell.FgColor(cell.ColorRed)),
		linechart.YLabelCellOpts(cell.FgColor(cell.ColorGreen)),
		linechart.XLabelCellOpts(cell.FgColor(cell.ColorGreen)),
	)
	if err != nil {
		return fmt.Errorf("failed to create line chart: %w", err)
	}
	ad.lineChart = lineChart
	return nil
}

// initChartButtons initializes the chart control buttons
func (ad *ArbitrageDashboard) initChartButtons() error {
	// "All Coins" button
	allButton, err := button.New("Todas as moedas", func() error {
		ad.mu.Lock()
		ad.mode = ModeAll
		ad.mu.Unlock()
		return nil
	},
		button.WidthFor("Todas as moedas"),
		button.Height(1),
		button.FillColor(cell.ColorNumber(220)),
	)
	if err != nil {
		return fmt.Errorf("failed to create All button: %w", err)
	}
	ad.chartButtons["All"] = allButton

	// Individual coin buttons
	for _, coin := range ad.coins {
		coinCopy := coin // Create a copy to avoid closure capture issues
		btn, err := button.New(coinCopy, func() error {
			ad.mu.Lock()
			ad.mode = ModeSingle
			ad.selectedCoin = coinCopy
			ad.mu.Unlock()
			return nil
		},
			button.WidthFor(coinCopy),
			button.Height(1),
			button.FillColor(cell.ColorNumber(196)),
		)
		if err != nil {
			return fmt.Errorf("failed to create button for %s: %w", coin, err)
		}
		ad.chartButtons[coinCopy] = btn
	}

	return nil
}

// ProcessCoinUpdate handles new coin data updates
func (ad *ArbitrageDashboard) processCoinUpdate(coinData CoinData) {
	ad.mu.Lock()
	defer ad.mu.Unlock()

	ad.updateCoinWidget(coinData)
	ad.updateProfitData(coinData)
	ad.updateSpreadHistory(coinData)
	ad.updateBarChart()
	ad.updateLineChart()
}

// updateCoinWidget updates the text widget for a specific coin
func (ad *ArbitrageDashboard) updateCoinWidget(coinData CoinData) {
	widget, exists := ad.coinWidgets[coinData.Symbol]
	if !exists {
		return
	}

	widgetText := fmt.Sprintf(`
COMPRAR:
Exchange:       %s
Preço:          $%.7f
---------------------------------
VENDER:
Exchange:        %s
Preço:           $%.7f
---------------------------------
Spread:          %.7f%%
Lucro Bruto:     %.7f%%
Lucro Liquido:   %.7f%%
Horário          %s
`,
		coinData.BuyExchange,
		coinData.BuyPrice,
		coinData.SellExchange,
		coinData.SellPrice,
		coinData.Profit,
		coinData.Spread,
		coinData.LiquidProfit,
		coinData.Timestamp.Format("15:04:05"),
	)

	widget.Reset()
	widget.Write(widgetText)
}

// updateProfitData updates the profit data for a specific coin
func (ad *ArbitrageDashboard) updateProfitData(coinData CoinData) {
	ad.profits[coinData.Symbol] = coinData.Profit
}

// updateSpreadHistory updates the spread history for a specific coin
func (ad *ArbitrageDashboard) updateSpreadHistory(coinData CoinData) {
	history := ad.spreadsHistory[coinData.Symbol]
	if len(history) >= maxHistorySize {
		history = history[1:]
	}
	ad.spreadsHistory[coinData.Symbol] = append(history, coinData.Spread)
}

// updateBarChart updates the bar chart with current profit data
func (ad *ArbitrageDashboard) updateBarChart() {
	barData := make([]int, len(ad.coins))
	for i, coin := range ad.coins {
		barData[i] = int(math.Abs(ad.profits[coin]) * 20000)
	}
	ad.barChart.Values(barData, 1000)
}

// updateLineChart updates the line chart based on current mode
func (ad *ArbitrageDashboard) updateLineChart() {
	// Clear existing series
	ad.lineChart.Series("", []float64{}, linechart.SeriesCellOpts(cell.FgColor(cell.ColorDefault)))

	// Clear all coin series to ensure full cleanup
	for _, coin := range ad.coins {
		ad.lineChart.Series(coin, []float64{}, linechart.SeriesCellOpts(cell.FgColor(cell.ColorDefault)))
	}

	switch ad.mode {
	case ModeAll:
		ad.renderAllCoinsChart()
	case ModeSingle:
		ad.renderSingleCoinChart()
	}
}

// renderAllCoinsChart renders line chart with all coins
func (ad *ArbitrageDashboard) renderAllCoinsChart() {
	for i, coin := range ad.coins {
		if spreads, ok := ad.spreadsHistory[coin]; ok && len(spreads) > 0 {
			ad.lineChart.Series(coin,
				spreads,
				linechart.SeriesCellOpts(cell.FgColor(ad.chartColors[i%len(ad.chartColors)])),
			)
		}
	}
}

// renderSingleCoinChart renders line chart with only the selected coin
func (ad *ArbitrageDashboard) renderSingleCoinChart() {
	if spreads, ok := ad.spreadsHistory[ad.selectedCoin]; ok && len(spreads) > 0 {
		colorIdx := 0
		for i, coin := range ad.coins {
			if coin == ad.selectedCoin {
				colorIdx = i
				break
			}
		}
		ad.lineChart.Series(
			ad.selectedCoin,
			spreads,
			linechart.SeriesCellOpts(cell.FgColor(ad.chartColors[colorIdx%len(ad.chartColors)])),
		)
	}
}

// StartUpdateListener starts the goroutine for processing coin updates
func (ad *ArbitrageDashboard) StartUpdateListener(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case coinData := <-ad.updateChan:
				ad.processCoinUpdate(coinData)
			case <-ad.closeChan:
				return
			}
		}
	}()
}

// Close shuts down the dashboard cleanly
func (ad *ArbitrageDashboard) Close() {
	close(ad.closeChan)
}

// SendCoinData sends coin data to the dashboard for processing
func (ad *ArbitrageDashboard) SendCoinData(data CoinData) {
	select {
	case ad.updateChan <- data:
		// Data sent successfully
	default:
		// Channel buffer full
	}
}

// CreateLayout creates the grid layout for the dashboard
func (ad *ArbitrageDashboard) CreateLayout() ([]container.Option, error) {
	builder := grid.New()

	// Create button elements for line chart
	buttonElements := ad.createButtonElements()

	builder.Add(
		grid.RowHeightPerc(30,
			ad.createCoinWidgetsRow()...,
		),
		grid.RowHeightPerc(65,
			grid.ColWidthPerc(50,
				grid.Widget(ad.barChart,
					container.Border(linestyle.Light),
					container.BorderTitle(" Lucros da arbitragem "),
				),
			),
			grid.ColWidthPerc(50,
				grid.RowHeightPerc(15,
					buttonElements...,
				),
				grid.RowHeightPerc(85,
					grid.Widget(ad.lineChart,
						container.Border(linestyle.Light),
						container.BorderTitle(" Histórico de spread da arbitragem "),
					),
				),
			),
		),
	)

	gridOpts, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build grid layout: %w", err)
	}
	return gridOpts, nil
}

// createButtonElements creates the grid elements for chart buttons
func (ad *ArbitrageDashboard) createButtonElements() []grid.Element {
	var buttonElements []grid.Element

	// Add "All" button
	buttonElements = append(buttonElements,
		grid.ColWidthPerc(20,
			grid.Widget(ad.chartButtons["All"],
				container.Border(linestyle.Light),
			),
		),
	)

	// Add coin buttons
	for _, coin := range ad.coins {
		buttonElements = append(buttonElements,
			grid.ColWidthPerc(16,
				grid.Widget(ad.chartButtons[coin],
					container.Border(linestyle.Light),
				),
			),
		)
	}

	return buttonElements
}

// createCoinWidgetsRow creates the grid elements for coin widgets
func (ad *ArbitrageDashboard) createCoinWidgetsRow() []grid.Element {
	var elements []grid.Element
	for _, coin := range ad.coins {
		elements = append(elements,
			grid.ColWidthPerc(20,
				grid.Widget(ad.coinWidgets[coin],
					container.Border(linestyle.Light),
					container.BorderTitle(fmt.Sprintf(" %s Arbitragem ", coin)),
				),
			),
		)
	}
	return elements
}

// RunDashboard starts and runs the dashboard terminal UI
func RunDashboard(ctx context.Context, ad *ArbitrageDashboard) error {
	t, err := tcell.New(tcell.ColorMode(terminalapi.ColorMode256))
	if err != nil {
		return fmt.Errorf("failed to initialize terminal: %w", err)
	}
	defer t.Close()

	gridOpts, err := ad.CreateLayout()
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

	// Start the update listener
	ad.StartUpdateListener(dashCtx)

	return termdash.Run(dashCtx, t, c, termdash.RedrawInterval(redrawInterval))
}
