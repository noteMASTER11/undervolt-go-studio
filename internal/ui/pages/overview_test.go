package pages

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

func TestOverviewChartLayoutSeparatesAdjacentChartAxes(t *testing.T) {
	charts := []fyne.CanvasObject{widget.NewLabel("CPU Load"), widget.NewLabel("Package Temperature")}
	layout := overviewChartLayout(charts)
	grid, ok := layout.(*fyne.Container)
	if !ok {
		t.Fatalf("two-chart layout = %T, want grid container", layout)
	}
	for index, chart := range charts {
		if grid.Objects[index] == chart {
			t.Fatalf("chart %d is not separated from its adjacent axis labels", index)
		}
	}
}

func TestOverviewChartLayoutStacksThreeChartsToKeepAxesReadable(t *testing.T) {
	charts := []fyne.CanvasObject{widget.NewLabel("CPU Load"), widget.NewLabel("Package Temperature"), widget.NewLabel("Average CPU Frequency")}
	layout := overviewChartLayout(charts)
	grid, ok := layout.(*fyne.Container)
	if !ok {
		t.Fatalf("three-chart layout = %T, want grid container", layout)
	}
	if got := len(grid.Objects); got != 3 {
		t.Fatalf("three-chart layout has %d direct rows, want 3", got)
	}
	for index, chart := range charts {
		if grid.Objects[index] == chart {
			t.Fatalf("chart %d has no inset in its own row", index)
		}
	}
}

func TestOverviewChartLayoutPadsLastRowForTimelineEndLabel(t *testing.T) {
	charts := []fyne.CanvasObject{widget.NewLabel("CPU Load"), widget.NewLabel("Package Temperature"), widget.NewLabel("Average CPU Frequency")}
	layout := overviewChartLayout(charts).(*fyne.Container)
	if layout.Objects[2] == charts[2] {
		t.Fatal("last chart has no right-hand safety inset for its end timestamp")
	}
}
