package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/mum4k/termdash/cell"
	"github.com/mum4k/termdash/widgets/button"
	"github.com/mum4k/termdash/widgets/linechart"
	"github.com/mum4k/termdash/widgets/text"
	"github.com/raykavin/ArbiTron/internal/ui/widgets"
)

var strBuilder strings.Builder

// dashboardWidgets manages all UI widgets for the ArbitrageDashboard
type dashboardWidgets struct {
	dashboard         *ArbitrageDashboard
	opportunitiesList *widgets.OpportunityListBox
	lineChart         *linechart.LineChart
	logsWidget        *text.Text
	coinWidgets       map[string]*text.Text
	chartButtons      map[string]*button.Button
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
// initAllWidgets initializes all dashboard UI widgets
func (dw *dashboardWidgets) initAllWidgets() error {
	if err := dw.initCoinWidgets(); err != nil {
		return fmt.Errorf("failed to initialize coin widgets: %w", err)
	}

	if err := dw.initOpportunitiesList(); err != nil {
		return fmt.Errorf("failed to initialize opportunities list: %w", err)
	}

	if err := dw.initLineChart(); err != nil {
		return fmt.Errorf("failed to initialize line chart: %w", err)
	}

	if err := dw.initLogsWidget(); err != nil {
		return fmt.Errorf("failed to initialize logs widget: %w", err)
	}

	if err := dw.initChartButtons(); err != nil {
		return fmt.Errorf("failed to initialize chart buttons: %w", err)
	}

	return nil
}

// initOpportunitiesList initializes the opportunities list box
func (dw *dashboardWidgets) initOpportunitiesList() error {
	opportunitiesList, err := widgets.NewOpportunityListBox()
	if err != nil {
		return fmt.Errorf("failed to create opportunities list: %w", err)
	}
	dw.opportunitiesList = opportunitiesList
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
// func (dw *dashboardWidgets) initBarChart() error {
// 	barChart, err := barchart.New(
// 		barchart.BarColors(dw.dashboard.chartColors),
// 		barchart.ShowValues(),
// 		barchart.Labels(dw.dashboard.coins),
// 	)
// 	if err != nil {
// 		return fmt.Errorf("failed to create bar chart: %w", err)
// 	}
// 	dw.barChart = barChart
// 	return nil
// }

// initLogsWidget initializes the logs text widget
func (dw *dashboardWidgets) initLogsWidget() error {
	logsWidget, err := text.New(text.RollContent(), text.WrapAtWords())
	if err != nil {
		return fmt.Errorf("failed to create logs widget: %w", err)
	}
	dw.logsWidget = logsWidget
	dw.dashboard.logger = logsWidget

	// Write initial message to logs
	logsWidget.Write("Arbitrage dashboard initialized.\n")
	logsWidget.Write("Waiting for data...\n")
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
// updateCoinWidget updates the text widget for a specific coin with flashing effect for profitable opportunities
func (dw *dashboardWidgets) updateCoinWidget(coinData CoinData) {
	widget, exists := dw.coinWidgets[coinData.Symbol]
	if !exists {
		return
	}

	// Check if this is a profitable opportunity and set flash timer
	if coinData.Profit > dw.dashboard.config.MinProfitPercentage && coinData.PotentialProfit > dw.dashboard.config.MinProfitAmount {
		coinData.Profitable = true
		coinData.FlashUntil = time.Now().Add(3 * time.Second)

		// Add to opportunities list
		dw.opportunitiesList.AddOpportunity(widgets.ListBoxItem{
			Symbol:          coinData.Symbol,
			BuyExchange:     coinData.BuyExchange,
			SellExchange:    coinData.SellExchange,
			Profit:          coinData.Profit,
			PotentialProfit: coinData.PotentialProfit,
			Timestamp:       coinData.Timestamp,
		})
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

	// Apply green text color if profitable
	if coinData.Profitable && time.Now().Before(coinData.FlashUntil) {
		widget.Write(widgetText, text.WriteCellOpts(cell.FgColor(cell.ColorLime)))

		// Store data for flashing
		dw.dashboard.addFlashingWidget(coinData.Symbol, coinData.FlashUntil)
	} else {
		widget.Write(widgetText)
	}

	strBuilder.Reset()

	// Add a log entry for this update
	dw.appendLog(fmt.Sprintf(
		"[%s] Updated %-6s    | Spread: %7.4f%% | Net Profit: %7.4f%%\n",
		coinData.Timestamp.Format("15:04:05"),
		coinData.Symbol,
		coinData.Spread,
		coinData.LiquidProfit,
	))
}

// appendLog adds a log entry to the logs widget
func (dw *dashboardWidgets) appendLog(message string) {
	if dw.logsWidget != nil {
		dw.logsWidget.Write(message)
	}
}

// updateAllCharts updates all charts with current data
func (dw *dashboardWidgets) updateAllCharts() {
	// dw.updateBarChart()
	dw.updateLineChart()
}

// updateBarChart updates the bar chart with current profit data
// func (dw *dashboardWidgets) updateBarChart() {
// 	barData := make([]int, len(dw.dashboard.coins))
// 	for i, coin := range dw.dashboard.coins {
// 		profit, exists := dw.dashboard.profits[coin]
// 		if exists {
// 			barData[i] = int(math.Abs(profit) * 20000)
// 		}
// 	}
// 	_ = dw.barChart.Values(barData, 1000)
// }

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
