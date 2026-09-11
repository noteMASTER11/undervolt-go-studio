package components

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
)

func TestPendingRailReflectsPendingCount(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	rail := NewPendingRail(nil, nil, nil)
	rail.SetChanges([]tuning.Change{{ID: tuning.ControlPL1, Requested: tuning.NumericValue(40)}})
	if rail.Count() != 1 || rail.Object() == nil {
		t.Fatalf("count = %d", rail.Count())
	}
}

func TestPendingRailShowsStockToRequestedAndDisablesReviewWhileBusy(t *testing.T) {
	application := test.NewApp()
	defer application.Quit()
	rail := NewPendingRail(nil, nil, nil)
	capabilities := tuning.CapabilitySet{Capabilities: []tuning.Capability{{
		ID: tuning.ControlPL1, Unit: tuning.UnitWatt, Current: tuning.NumericValue(44),
	}}}
	rail.SetChanges([]tuning.Change{{ID: tuning.ControlPL1, Requested: tuning.NumericValue(40)}}, capabilities)
	row := rail.rows.Objects[0].(*widget.Label).Text
	if !strings.Contains(row, "44.0 W  →  40.0 W") {
		t.Fatalf("pending row = %q", row)
	}
	rail.SetReviewEnabled(false)
	if !rail.review.Disabled() {
		t.Fatal("review enabled while apply is busy")
	}
	rail.SetReviewEnabled(true)
	if rail.review.Disabled() {
		t.Fatal("review stayed disabled after staged state returned")
	}
}
