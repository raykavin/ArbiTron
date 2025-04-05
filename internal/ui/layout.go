package ui

import (
	"fmt"

	"github.com/mum4k/termdash/container"
	"github.com/mum4k/termdash/container/grid"
	"github.com/mum4k/termdash/linestyle"
)

// dashboardLayouter manages layout for the ArbitrageDashboard
type dashboardLayouter struct {
	dashboard *ArbitrageDashboard
}

// newDashboardLayouter creates a new dashboard layout manager
func newDashboardLayouter(dashboard *ArbitrageDashboard) *dashboardLayouter {
	return &dashboardLayouter{
		dashboard: dashboard,
	}
}

// CreateLayout creates the grid layout for the dashboard
func (dl *dashboardLayouter) CreateLayout() ([]container.Option, error) {
	builder := grid.New()

	// Create button elements for line chart
	buttonElements := dl.createButtonElements()

	builder.Add(
		grid.RowHeightPerc(30,
			dl.createCoinWidgetsRow()...,
		),
		grid.RowHeightPerc(65,
			grid.ColWidthPerc(50,
				grid.Widget(dl.dashboard.widgets.barChart,
					container.Border(linestyle.Light),
					container.BorderTitle(" Arbitrage Profits "),
				),
			),
			grid.ColWidthPerc(50,
				grid.RowHeightPerc(15,
					buttonElements...,
				),
				grid.RowHeightPerc(85,
					grid.Widget(dl.dashboard.widgets.lineChart,
						container.Border(linestyle.Light),
						container.BorderTitle(" Spread History "),
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
func (dl *dashboardLayouter) createButtonElements() []grid.Element {
	var buttonElements []grid.Element

	// Add "All" button
	buttonElements = append(buttonElements,
		grid.ColWidthPerc(20,
			grid.Widget(dl.dashboard.widgets.chartButtons["All"],
				container.Border(linestyle.Light),
			),
		),
	)

	// Add coin buttons
	for _, coin := range dl.dashboard.coins {
		buttonElements = append(buttonElements,
			grid.ColWidthPerc(16,
				grid.Widget(dl.dashboard.widgets.chartButtons[coin],
					container.Border(linestyle.Light),
				),
			),
		)
	}

	return buttonElements
}

// createCoinWidgetsRow creates the grid elements for coin widgets
func (dl *dashboardLayouter) createCoinWidgetsRow() []grid.Element {
	var elements []grid.Element
	for _, coin := range dl.dashboard.coins {
		elements = append(elements,
			grid.ColWidthPerc(20,
				grid.Widget(dl.dashboard.widgets.coinWidgets[coin],
					container.Border(linestyle.Light),
					container.BorderTitle(fmt.Sprintf(" %s Arbitrage ", coin)),
				),
			),
		)
	}
	return elements
}
