package pages

import (
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
	"github.com/noteMASTER11/undervolt-go-studio/internal/ui/viewmodel"
)

type pageHandle struct {
	frames chan telemetry.Frame
	once   sync.Once
	closed int
}

func (h *pageHandle) Frames() <-chan telemetry.Frame { return h.frames }
func (h *pageHandle) Close() {
	h.once.Do(func() { h.closed++; close(h.frames) })
}

type pageSource struct {
	calls  int
	handle *pageHandle
}

func (s *pageSource) Subscribe(telemetry.Subscription) viewmodel.SubscriptionHandle {
	s.calls++
	s.handle = &pageHandle{frames: make(chan telemetry.Frame, 1)}
	return s.handle
}

func TestLivePagesSubscribeOnlyWhenActivated(t *testing.T) {
	application := test.NewApp()
	t.Cleanup(application.Quit)
	catalog := telemetry.Catalog{Metrics: []telemetry.Descriptor{{
		ID: "cpu.utilization", Label: "CPU Utilization", Unit: "%",
	}}}
	for _, build := range []func(*pageSource) PageForTest{
		func(source *pageSource) PageForTest { return NewOverview(source, catalog) },
		func(source *pageSource) PageForTest { return NewMonitor(source, catalog) },
	} {
		source := &pageSource{}
		page := build(source)
		if source.calls != 0 {
			t.Fatal("page subscribed during construction")
		}
		page.Activate()
		if source.calls != 1 {
			t.Fatalf("subscribe calls = %d", source.calls)
		}
		page.Deactivate()
		if source.handle.closed != 1 {
			t.Fatalf("close calls = %d", source.handle.closed)
		}
	}
}

type PageForTest interface {
	Activate()
	Deactivate()
}

func TestMonitorIntervalOptionsStayWithinSchedulerBounds(t *testing.T) {
	application := test.NewApp()
	t.Cleanup(application.Quit)
	source := &pageSource{}
	page := NewMonitor(source, telemetry.Catalog{})
	want := []time.Duration{100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 5 * time.Second}
	if len(page.intervals) != len(want) {
		t.Fatalf("intervals = %v", page.intervals)
	}
	for index := range want {
		if page.intervals[index] != want[index] {
			t.Fatalf("intervals = %v, want %v", page.intervals, want)
		}
	}
}

func TestRecentSamplesKeepsOnlySixtySecondWindow(t *testing.T) {
	now := time.Unix(100, 0)
	samples := []telemetry.Sample{{Timestamp: now.Add(-61 * time.Second)}, {Timestamp: now.Add(-60 * time.Second)}, {Timestamp: now}}
	got := recentSamples(samples, now.Add(-60*time.Second))
	if len(got) != 2 || !got[0].Timestamp.Equal(now.Add(-60*time.Second)) {
		t.Fatalf("recent samples = %+v", got)
	}
}

func TestAverageSamplesCombinesPerCoreFrequency(t *testing.T) {
	metrics := []telemetry.MetricID{"cpu.0.frequency", "cpu.1.frequency"}
	current := map[telemetry.MetricID]telemetry.Sample{
		"cpu.0.frequency": {MetricID: "cpu.0.frequency", Value: 2000, Quality: telemetry.QualityGood},
		"cpu.1.frequency": {MetricID: "cpu.1.frequency", Value: 3000, Quality: telemetry.QualityGood},
	}
	got, ok := averageSample(current, metrics)
	if !ok || got.Value != 2500 || got.Quality != telemetry.QualityGood {
		t.Fatalf("average = %+v, ok=%v", got, ok)
	}
}

func TestAppendAverageFrequencyKeepsAlignedFrameAverage(t *testing.T) {
	page := &Overview{metrics: overviewMetrics{frequencies: []telemetry.MetricID{"cpu.0.frequency", "cpu.1.frequency"}}}
	timestamp := time.Unix(20, 0)
	state := viewmodel.MonitorState{Current: map[telemetry.MetricID]telemetry.Sample{
		"cpu.0.frequency": {Value: 2000, Timestamp: timestamp, Quality: telemetry.QualityGood},
		"cpu.1.frequency": {Value: 4000, Timestamp: timestamp, Quality: telemetry.QualityGood},
	}}
	page.appendAverageFrequency(state)
	page.appendAverageFrequency(state)
	if len(page.frequencyHistory) != 1 || page.frequencyHistory[0].Value != 3000 || !page.frequencyHistory[0].Timestamp.Equal(timestamp) {
		t.Fatalf("frequency history = %+v", page.frequencyHistory)
	}
}

func TestMonitorSeriesAreGroupedByUnit(t *testing.T) {
	descriptors := map[telemetry.MetricID]telemetry.Descriptor{
		"load": {ID: "load", Label: "Load", Unit: "%"},
		"freq": {ID: "freq", Label: "Frequency", Unit: "MHz"},
	}
	groups := groupSeriesByUnit([]telemetry.MetricID{"load", "freq"}, descriptors, map[telemetry.MetricID][]telemetry.Sample{})
	if len(groups) != 2 || groups[0].Unit == groups[1].Unit {
		t.Fatalf("groups = %+v", groups)
	}
}
