package telemetry_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/providers/fake"
	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

func TestSchedulerSubscriptionSoak(t *testing.T) {
	baseline := runtime.NumGoroutine()
	provider := fake.New(fake.Options{ProviderID: "soak", MetricCount: 8})
	scheduler := telemetry.NewScheduler([]telemetry.Provider{provider}, telemetry.SchedulerOptions{
		ProviderTimeout: 50 * time.Millisecond, MinInterval: time.Millisecond, MaxInterval: time.Second, AllowTestIntervals: true,
	})
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := scheduler.Discover(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	scheduler.Start(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for iteration := 0; time.Now().Before(deadline); iteration++ {
		handle := scheduler.Subscribe(telemetry.Subscription{
			ConsumerID: "soak", MetricIDs: []telemetry.MetricID{telemetry.MetricID("soak.metric." + string(rune('0'+iteration%8)))},
			Interval: time.Millisecond,
		})
		select {
		case <-handle.Frames():
		case <-time.After(5 * time.Millisecond):
		}
		handle.Close()
	}

	waitForDiagnosticsState(t, scheduler, "idle", time.Second)
	for _, diagnostic := range scheduler.Diagnostics() {
		if diagnostic.ActiveMetrics != 0 || diagnostic.ActiveConsumers != 0 {
			t.Fatalf("demand remained after churn: %+v", diagnostic)
		}
	}
	cancel()
	waitForDiagnosticsState(t, scheduler, "stopped", time.Second)
	runtime.GC()
	deadline = time.Now().Add(time.Second)
	for runtime.NumGoroutine() > baseline+5 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if got := runtime.NumGoroutine(); got > baseline+5 {
		t.Fatalf("goroutines = %d, baseline = %d", got, baseline)
	}
}

func waitForDiagnosticsState(t *testing.T, scheduler *telemetry.Scheduler, state string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		diagnostics := scheduler.Diagnostics()
		if len(diagnostics) == 1 && diagnostics[0].State == state {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("provider did not reach %q: %+v", state, scheduler.Diagnostics())
}
