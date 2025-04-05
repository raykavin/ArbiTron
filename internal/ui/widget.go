package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/mum4k/termdash/cell"
	"github.com/mum4k/termdash/widgets/barchart"
	"github.com/mum4k/termdash/widgets/button"
	"github.com/mum4k/termdash/widgets/linechart"
	"github.com/mum4k/termdash/widgets/text"
)

var strBuilder strings.Builder

// dashboardWidgets manages all UI widgets for the ArbitrageDashboard
type dashboardWidgets struct {
	dashboard    *ArbitrageDashboard
	coinWidgets  map[string]*text.Text
	barChart     *barchart.BarChart
	lineChart    *linechart.LineChart
	chartButtons map[string]*button.Button
}

// newDashboardWidgets creates a new dashboard widgets manager
func newDashboardWidgets(dashboard *ArbitrageDashboard) *dashboardWidgets {
	return &dashboardWidgets{
		dashboard:    dashboard,
		coinWidgets:  make(map[string]*text.Text),
		chartButtons: make(map[string]*button.Button),
	}
}

// initAllWidgets initializes all dashboard UI widgets
func (dw *dashboardWidgets) initAllWidgets() error {
	if err := dw.initCoinWidgets(); err != nil {
		return fmt.Errorf("failed to initialize coin widgets: %w", err)
	}

	if err := dw.initBarChart(); err != nil {
		return fmt.Errorf("failed to initialize bar chart: %w", err)
	}

	if err := dw.initLineChart(); err != nil {
		return fmt.Errorf("failed to initialize line chart: %w", err)
	}

	if err := dw.initChartButtons(); err != nil {
		return fmt.Errorf("failed to initialize chart buttons: %w", err)
	}

	return nil
}

// initCoinWidgets initializes the text widgets for each coin
func (dw *dashboardWidgets) initCoinWidgets() error {
	for _, coin := range dw.dashboard.coins {
		widget, err := text.New(text.RollContent(), text.WrapAtWords())
		if err != nil {
			return fmt.Errorf("failed to create text widget for %s: %w", coin, err)
		}
		dw.coinWidgets[coin] = widget
	}
	return nil
}

// initBarChart initializes the profit bar chart
func (dw *dashboardWidgets) initBarChart() error {
	barChart, err := barchart.New(
		barchart.BarColors(dw.dashboard.chartColors),
		barchart.ShowValues(),
		barchart.Labels(dw.dashboard.coins),
	)
	if err != nil {
		return fmt.Errorf("failed to create bar chart: %w", err)
	}
	dw.barChart = barChart
	return nil
}

// initLineChart initializes the spread history line chart
func (dw *dashboardWidgets) initLineChart() error {
	lineChart, err := linechart.New(
		linechart.AxesCellOpts(cell.FgColor(cell.ColorRed)),
		linechart.YLabelCellOpts(cell.FgColor(cell.ColorGreen)),
		linechart.XLabelCellOpts(cell.FgColor(cell.ColorGreen)),
	)
	if err != nil {
		return fmt.Errorf("failed to create line chart: %w", err)
	}
	dw.lineChart = lineChart
	return nil
}

// initChartButtons initializes the chart control buttons
func (dw *dashboardWidgets) initChartButtons() error {
	// "All Coins" button
	allButton, err := button.New("All coins", func() error {
		dw.dashboard.dataMu.Lock()
		dw.dashboard.mode = ModeAll
		dw.dashboard.dataMu.Unlock()

		// Schedule a chart update for current data
		dw.dashboard.scheduleUIUpdate()
		return nil
	},
		button.WidthFor("All coins"),
		button.Height(1),
		button.FillColor(cell.ColorNumber(220)),
	)
	if err != nil {
		return fmt.Errorf("failed to create All button: %w", err)
	}
	dw.chartButtons["All"] = allButton

	// Individual coin buttons
	for _, coin := range dw.dashboard.coins {
		coinCopy := coin // Create a copy to avoid closure capture issues
		btn, err := button.New(coinCopy, func() error {
			dw.dashboard.dataMu.Lock()
			dw.dashboard.mode = ModeSingle
			dw.dashboard.selectedCoin = coinCopy
			dw.dashboard.dataMu.Unlock()

			// Schedule a chart update for current data
			dw.dashboard.scheduleUIUpdate()
			return nil
		},
			button.WidthFor(coinCopy),
			button.Height(1),
			button.FillColor(cell.ColorNumber(196)),
		)
		if err != nil {
			return fmt.Errorf("failed to create button for %s: %w", coin, err)
		}
		dw.chartButtons[coinCopy] = btn
	}

	return nil
}

// updateCoinWidget updates the text widget for a specific coin
func (dw *dashboardWidgets) updateCoinWidget(coinData CoinData) {
	widget, exists := dw.coinWidgets[coinData.Symbol]
	if !exists {
		return
	}

	fmt.Fprintf(&strBuilder, "%-16s\n", "BUY:")
	fmt.Fprintf(&strBuilder, "%-16s %-16s\n", "Exchange:", coinData.BuyExchange)

	fmt.Fprintf(&strBuilder, "%-16s $%.7f\n", "Price:", coinData.BuyPrice)
	fmt.Fprintln(&strBuilder, strings.Repeat("-", 33))
	fmt.Fprintf(&strBuilder, "%-16s\n", "SELL:")
	fmt.Fprintf(&strBuilder, "%-16s %s\n", "Exchange:", coinData.SellExchange)
	fmt.Fprintf(&strBuilder, "%-16s $%.7f\n", "Price:", coinData.SellPrice)
	fmt.Fprintln(&strBuilder, strings.Repeat("-", 33))

	fmt.Fprintf(&strBuilder, "%-16s %.7f%%\n", "Spread:", coinData.Spread)
	fmt.Fprintf(&strBuilder, "%-16s %.7f%%\n", "Gross Profit:", coinData.Profit)
	fmt.Fprintf(&strBuilder, "%-16s %.7f%%\n", "Net Profit:", coinData.LiquidProfit)
	fmt.Fprintf(&strBuilder, "%-16s %.6f %s\n", "Max Volume:", coinData.MaxTradeSize, coinData.Symbol)
	fmt.Fprintf(&strBuilder, "%-16s $%.2f\n", "Pot. Profit:", coinData.PotentialProfit)
	fmt.Fprintf(&strBuilder, "%-16s %s\n", "Timestamp:", coinData.Timestamp.Format("15:04:05"))

	widgetText := strBuilder.String()

	widget.Reset()
	widget.Write(widgetText)
	strBuilder.Reset()

}

// updateAllCharts updates all charts with current data
func (dw *dashboardWidgets) updateAllCharts() {
	dw.updateBarChart()
	dw.updateLineChart()
}

// updateBarChart updates the bar chart with current profit data
func (dw *dashboardWidgets) updateBarChart() {
	barData := make([]int, len(dw.dashboard.coins))
	for i, coin := range dw.dashboard.coins {
		profit, exists := dw.dashboard.profits[coin]
		if exists {
			barData[i] = int(math.Abs(profit) * 20000)
		}
	}
	_ = dw.barChart.Values(barData, 1000)
}

// updateLineChart updates the line chart based on current mode
func (dw *dashboardWidgets) updateLineChart() {
	dw.lineChart.Series("", []float64{}, linechart.SeriesCellOpts(cell.FgColor(cell.ColorDefault)))

	// Clear all coin series to ensure complete cleaning
	for _, coin := range dw.dashboard.coins {
		dw.lineChart.Series(coin, []float64{}, linechart.SeriesCellOpts(cell.FgColor(cell.ColorDefault)))
	}

	switch dw.dashboard.mode {
	case ModeAll:
		dw.renderAllCoinsChart()
	case ModeSingle:
		dw.renderSingleCoinChart()
	}
}

// renderAllCoinsChart renders line chart with all coins
func (dw *dashboardWidgets) renderAllCoinsChart() {
	for i, coin := range dw.dashboard.coins {
		if spreads, ok := dw.dashboard.spreadsHistory[coin]; ok && len(spreads) > 0 {
			// Ignore update errors
			dw.lineChart.Series(coin,
				spreads,
				linechart.SeriesCellOpts(cell.FgColor(dw.dashboard.chartColors[i%len(dw.dashboard.chartColors)])),
			)
		}
	}
}

// renderSingleCoinChart renders line chart with only the selected coin
func (dw *dashboardWidgets) renderSingleCoinChart() {
	if spreads, ok := dw.dashboard.spreadsHistory[dw.dashboard.selectedCoin]; ok && len(spreads) > 0 {
		colorIdx := 0
		for i, coin := range dw.dashboard.coins {
			if coin == dw.dashboard.selectedCoin {
				colorIdx = i
				break
			}
		}

		_ = dw.lineChart.Series(
			dw.dashboard.selectedCoin,
			spreads,
			linechart.SeriesCellOpts(cell.FgColor(dw.dashboard.chartColors[colorIdx%len(dw.dashboard.chartColors)])),
		)
	}
}
