# Undervolt Go Studio Foundation and Telemetry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create the GitHub fork and deliver a runnable, unprivileged read-only MVP with an XTU Classic shell, lazy pages, asynchronous subscription-driven telemetry, Linux CPU/temperature providers, bounded history, and provider diagnostics.

**Architecture:** Keep the legacy root command operational while introducing focused `internal` packages and a new `cmd/studio` entry point. Providers expose batched discovery and sampling; a scheduler isolates providers, derives work from active subscriptions, applies deadlines and backpressure, and publishes immutable frames. Fyne pages are instantiated lazily, and widget updates are marshalled through `fyne.Do`.

**Tech Stack:** Go 1.24.1, Fyne 2.7.4, Go standard library, Linux `sysfs`/`procfs`/`hwmon`, GitHub Actions, Go tests and race detector.

**Spec:** `docs/superpowers/specs/2026-09-11-undervolt-go-studio-design.md`

## Global Constraints

- Product name is **Undervolt Go Studio**; repository name is `undervolt-go-studio`.
- Target Linux x86-64; GUI and telemetry run as the logged-in user.
- Retain GPL-3.0, original history, and `Softorage/undervolt-go` as `upstream`.
- Do not add privileged writes, PolicyKit, stress tests, or persistence in this plan.
- Never execute shell commands in telemetry polling paths.
- Never perform blocking I/O or widget mutation directly on Fyne's main thread.
- Visible intervals are 100 ms–5 s, default 250 ms.
- Providers batch requested metrics, have independent deadlines, and cannot block each other.
- Subscription and history queues are bounded with latest-value-wins behavior.
- Pages and page subscriptions are created lazily on first selection.
- Missing or failed metrics are explicit; unavailable is never represented as zero.
- The application performs no runtime downloads.

## Program Decomposition

The approved design is implemented as five independently reviewable plans:

1. Foundation and telemetry — this plan; a working read-only Studio MVP.
2. Privileged tuning — D-Bus/PolicyKit helper, Intel backend, RAPL power telemetry, transactions, leases, rollback.
3. Stress and safety — NVIDIA/Vulkan telemetry, packaged `stress-ng`, Vulkan worker, supervisor, thermal stops.
4. Sessions and reports — recovery, A/B comparison, MangoHud import, exports.
5. Offline packaging and release — installer, Arch package, SBOM, signatures, CI.

Do not start a later plan before the preceding deliverable is green and reviewed.

## File Map

```text
cmd/studio/main.go                         Studio GUI entry point
internal/product/info.go                  Product identity and version
internal/hardware/device.go               Stable device identity
internal/telemetry/model.go               Metrics, samples, frames, quality
internal/telemetry/provider.go            Provider and subscription contracts
internal/telemetry/scheduler.go           Async provider workers
internal/telemetry/delivery.go            Latest-value delivery
internal/telemetry/diagnostics.go          Provider health
internal/telemetry/history/                Bounded history and downsampling
internal/providers/fake/provider.go        Deterministic development provider
internal/providers/linuxfs/                cpufreq, hwmon, procstat providers
internal/ui/desktop.go                     Fyne lifecycle
internal/ui/shell.go                       Three-column shell
internal/ui/navigation.go                  Lazy page registry
internal/ui/components/                    Cards and timeline
internal/ui/pages/                         Overview, Monitor, Hardware
internal/ui/viewmodel/                     GUI-neutral page state
.github/workflows/go.yml                   Unit, race, vet, build checks
README.md                                  Alpha usage and scope
```

---

### Task 1: Fork the Repository and Capture the Baseline

**Files:**
- Modify: Git remotes and branch metadata only
- Verify: root package and `gui` build tag

**Interfaces:**
- Consumes: GitHub account `noteMASTER11`, `Softorage/undervolt-go`, current local `main` containing the approved specification and this plan
- Produces: `noteMASTER11/undervolt-go-studio`, `origin`, `upstream`, isolated feature worktree

- [ ] **Step 1: Verify the untouched baseline**

```bash
git status --short
git remote -v
git rev-parse HEAD
go test ./...
go build ./...
go build -tags gui ./...
```

Expected: only `.superpowers/` is untracked, HEAD contains the two documentation commits after upstream `4b057b0`, and all Go commands exit 0.

- [ ] **Step 2: Create the authenticated GitHub fork**

Using the GitHub UI/API, create a public fork owned by `noteMASTER11`, rename it to `undervolt-go-studio`, retain `main`, and confirm:

```text
https://github.com/noteMASTER11/undervolt-go-studio
```

Expected: GitHub shows `Softorage/undervolt-go` as the parent.

- [ ] **Step 3: Configure both remotes**

```bash
git remote rename origin upstream
git remote add origin https://github.com/noteMASTER11/undervolt-go-studio.git
git fetch --all --prune
git remote -v
```

Expected: `origin` is the fork and `upstream` is Softorage.

- [ ] **Step 4: Push the design and create an isolated worktree**

At execution time invoke `superpowers:using-git-worktrees`, then run:

```bash
git push origin main
git worktree add ../undervolt-go-studio-foundation -b feature/foundation-telemetry main
git -C ../undervolt-go-studio-foundation status --short
```

Expected: the fork contains the approved specification and implementation plan and the worktree is clean.

### Task 2: Establish Product Identity Without Breaking Legacy Builds

**Files:**
- Create: `internal/product/info.go`
- Create: `internal/product/info_test.go`
- Create: `internal/product/module_test.go`
- Modify: `go.mod`
- Modify: `.gitignore`
- Modify: `README.md`

**Interfaces:**
- Consumes: version string injected with `-ldflags`
- Produces: `product.Info`, `product.Current(version string) Info`, canonical module path

- [ ] **Step 1: Write failing identity tests**

```go
package product

import "testing"

func TestCurrent(t *testing.T) {
	got := Current("v0.1.0-alpha.1")
	if got.Name != "Undervolt Go Studio" { t.Fatalf("Name = %q", got.Name) }
	if got.AppID != "io.github.notemaster11.UndervoltGoStudio" { t.Fatalf("AppID = %q", got.AppID) }
	if got.Version != "v0.1.0-alpha.1" { t.Fatalf("Version = %q", got.Version) }
}
```

`module_test.go` reads `../../go.mod` and asserts its first line is exactly:

```text
module github.com/noteMASTER11/undervolt-go-studio
```

- [ ] **Step 2: Confirm the red state**

```bash
go test ./internal/product
```

Expected: FAIL because the package and canonical module declaration do not exist.

- [ ] **Step 3: Implement identity and rename the module**

```go
package product

type Info struct {
	Name string
	AppID string
	Version string
}

func Current(version string) Info {
	return Info{Name: "Undervolt Go Studio", AppID: "io.github.notemaster11.UndervoltGoStudio", Version: version}
}
```

Change `go.mod` to the asserted module path. Add `/build/` to `.gitignore`. Add a README heading and fork/upstream attribution without removing GPL notices.

- [ ] **Step 4: Verify Studio identity and legacy compatibility**

```bash
gofmt -w internal/product
go test ./internal/product
go test ./...
go build ./...
go build -tags gui ./...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go.mod .gitignore internal/product README.md
git commit -m "chore: establish Undervolt Go Studio identity"
```

### Task 3: Define Hardware and Telemetry Contracts

**Files:**
- Create: `internal/hardware/device.go`
- Create: `internal/hardware/device_test.go`
- Create: `internal/telemetry/model.go`
- Create: `internal/telemetry/model_test.go`
- Create: `internal/telemetry/provider.go`

**Interfaces:**
- Consumes: `context.Context`, `time.Duration`
- Produces: `hardware.DeviceID`, `hardware.Device`, metric domain types, `telemetry.Provider`, `telemetry.Subscription`

- [ ] **Step 1: Write failing stable-ID and freshness tests**

```go
func TestNewDeviceIDIsStable(t *testing.T) {
	a := NewDeviceID(KindCPU, "intel", "0000:00:00.0")
	b := NewDeviceID(KindCPU, "intel", "0000:00:00.0")
	if a != b || a == "" { t.Fatalf("IDs are not stable: %q %q", a, b) }
}
```

```go
func TestSampleFreshness(t *testing.T) {
	now := time.Unix(100, 0)
	s := Sample{Timestamp: now, Quality: QualityGood}
	if s.QualityAt(now.Add(249*time.Millisecond), 250*time.Millisecond) != QualityGood { t.Fatal("became stale early") }
	if s.QualityAt(now.Add(251*time.Millisecond), 250*time.Millisecond) != QualityStale { t.Fatal("did not become stale") }
}
```

- [ ] **Step 2: Confirm the red state**

```bash
go test ./internal/hardware ./internal/telemetry
```

Expected: FAIL because the packages do not exist.

- [ ] **Step 3: Implement stable device identity**

```go
package hardware

import "strings"

type DeviceID string
type Kind string

const (
	KindCPU Kind = "cpu"
	KindGPU Kind = "gpu"
)

type Device struct {
	ID DeviceID
	Kind Kind
	Vendor string
	Name string
	Attributes map[string]string
}

func NewDeviceID(kind Kind, vendor, nativeID string) DeviceID {
	n := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	return DeviceID(string(kind) + ":" + n(vendor) + ":" + n(nativeID))
}
```

- [ ] **Step 4: Implement metric models and provider contract**

Use the exact public shapes below, split between `model.go` and `provider.go`:

```go
type MetricID string
type Unit string
type Quality string

const (
	QualityGood Quality = "good"
	QualityStale Quality = "stale"
	QualityUnavailable Quality = "unavailable"
)

type Descriptor struct {
	ID MetricID
	ProviderID string
	DeviceID hardware.DeviceID
	Label string
	Unit Unit
	MinInterval time.Duration
}

type Sample struct {
	MetricID MetricID
	Value float64
	Timestamp time.Time
	Quality Quality
	Error string
}

func (s Sample) QualityAt(now time.Time, freshness time.Duration) Quality {
	if s.Quality != QualityGood { return s.Quality }
	if now.Sub(s.Timestamp) > freshness { return QualityStale }
	return QualityGood
}

type Frame struct {
	ProviderID string
	StartedAt time.Time
	FinishedAt time.Time
	Samples []Sample
}

type Catalog struct {
	Devices []hardware.Device
	Metrics []Descriptor
}

type Subscription struct {
	ConsumerID string
	MetricIDs []MetricID
	Interval time.Duration
}

type Provider interface {
	ID() string
	Discover(context.Context) (Catalog, error)
	Sample(context.Context, []MetricID) (Frame, error)
}
```

- [ ] **Step 5: Verify and commit**

```bash
gofmt -w internal/hardware internal/telemetry
go test ./internal/hardware ./internal/telemetry
go test ./...
git add internal/hardware internal/telemetry
git commit -m "feat: define hardware telemetry contracts"
```

Expected: PASS and one contract-focused commit.

### Task 4: Build the Fake Provider and Async Scheduler

**Files:**
- Create: `internal/providers/fake/provider.go`
- Create: `internal/providers/fake/provider_test.go`
- Create: `internal/telemetry/scheduler.go`
- Create: `internal/telemetry/scheduler_test.go`
- Create: `internal/telemetry/delivery.go`
- Create: `internal/telemetry/diagnostics.go`

**Interfaces:**
- Consumes: `telemetry.Provider`, `telemetry.Subscription`
- Produces: `SchedulerOptions`, `NewScheduler`, `Discover`, `Start`, `Subscribe`, `Diagnostics`, `Handle.Frames`, `Handle.Close`

- [ ] **Step 1: Write failing scheduler tests**

Cover no eager polling, batched metrics, isolation, and cancellation:

```go
func TestSchedulerDoesNotSampleWithoutSubscribers(t *testing.T) {
	p := fake.New(fake.Options{ProviderID: "fake", MetricCount: 3})
	s := NewScheduler([]Provider{p}, SchedulerOptions{AllowTestIntervals: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := s.Discover(ctx); err != nil { t.Fatal(err) }
	s.Start(ctx)
	time.Sleep(30 * time.Millisecond)
	if p.SampleCalls() != 0 { t.Fatalf("sample calls = %d", p.SampleCalls()) }
}

func TestSchedulerBatchesRequestedMetrics(t *testing.T) {
	p := fake.New(fake.Options{ProviderID: "fake", MetricCount: 3})
	s := NewScheduler([]Provider{p}, SchedulerOptions{ProviderTimeout: 50*time.Millisecond, AllowTestIntervals: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := s.Discover(ctx); err != nil { t.Fatal(err) }
	s.Start(ctx)
	h := s.Subscribe(Subscription{ConsumerID: "monitor", MetricIDs: []MetricID{"fake.metric.0", "fake.metric.1"}, Interval: 10*time.Millisecond})
	defer h.Close()
	select {
	case frame := <-h.Frames():
		if len(frame.Samples) != 2 { t.Fatalf("samples = %d", len(frame.Samples)) }
	case <-time.After(200*time.Millisecond):
		t.Fatal("no frame")
	}
	if p.LastBatchSize() != 2 { t.Fatalf("batch = %d", p.LastBatchSize()) }
}
```

Add `TestSlowProviderDoesNotBlockFastProvider`: a fake delayed by 200 ms with a 25 ms timeout must not prevent another fake provider from publishing within 200 ms.

- [ ] **Step 2: Confirm the red state**

```bash
go test ./internal/telemetry ./internal/providers/fake
```

Expected: FAIL because scheduler and fake provider are absent.

- [ ] **Step 3: Implement deterministic fake sampling and bounded delivery**

`fake.Options` is:

```go
type Options struct {
	ProviderID string
	MetricCount int
	Delay time.Duration
	FailEvery int
}
```

Each sample value is deterministically `sampleCall*1000 + metricIndex`. Implement delivery as:

```go
func deliverLatest(ch chan Frame, frame Frame) (dropped bool) {
	select { case ch <- frame: return false; default: }
	select { case <-ch: dropped = true; default: }
	select { case ch <- frame: default: dropped = true }
	return dropped
}
```

- [ ] **Step 4: Implement provider workers and diagnostics**

```go
type SchedulerOptions struct {
	ProviderTimeout time.Duration
	MinInterval time.Duration
	MaxInterval time.Duration
	AllowTestIntervals bool
}

type ProviderDiagnostics struct {
	ProviderID string
	State string
	LastSuccess time.Time
	LastError string
	LastLatency time.Duration
	ActiveMetrics int
	ActiveConsumers int
	DroppedFrames uint64
}
```

Expose these signatures:

```go
func NewScheduler(providers []Provider, options SchedulerOptions) *Scheduler
func (s *Scheduler) Discover(ctx context.Context) (Catalog, error)
func (s *Scheduler) Start(ctx context.Context)
func (s *Scheduler) Subscribe(subscription Subscription) *Handle
func (s *Scheduler) Diagnostics() []ProviderDiagnostics
func (h *Handle) Frames() <-chan Frame
func (h *Handle) Close()
```

One cancellable worker per provider derives the unique metric batch and fastest cadence from current subscribers. Production intervals clamp to 100 ms–5 s. Each `Sample` call receives its own timeout context. State values are `idle`, `running`, `backoff`, `stopped`; error backoff starts at 250 ms, doubles, and caps at 5 s. Handle channels have capacity one. `Handle.Close` is idempotent through `sync.Once`. Cancellation stops workers and closes handles.

- [ ] **Step 5: Verify races and commit**

```bash
gofmt -w internal/providers/fake internal/telemetry
go test ./internal/providers/fake ./internal/telemetry
go test -race ./internal/providers/fake ./internal/telemetry
git add internal/providers/fake internal/telemetry
git commit -m "feat: add asynchronous telemetry scheduler"
```

Expected: PASS with no race report.

### Task 5: Add Bounded History and Downsampling

**Files:**
- Create: `internal/telemetry/history/ring.go`
- Create: `internal/telemetry/history/ring_test.go`
- Create: `internal/telemetry/history/downsample.go`
- Create: `internal/telemetry/history/downsample_test.go`

**Interfaces:**
- Consumes: `telemetry.Sample`
- Produces: `history.New`, `Ring.Append`, `Ring.Snapshot`, `DownsampleMinMax`

- [ ] **Step 1: Write failing boundedness and spike tests**

```go
func TestRingKeepsNewestSamples(t *testing.T) {
	r := New(3)
	for i := 1; i <= 5; i++ { r.Append(telemetry.Sample{Value: float64(i)}) }
	got := r.Snapshot()
	if len(got) != 3 || got[0].Value != 3 || got[2].Value != 5 { t.Fatalf("snapshot = %#v", got) }
}

func TestDownsampleMinMaxPreservesSpike(t *testing.T) {
	in := make([]telemetry.Sample, 100)
	for i := range in { in[i].Value = 10 }
	in[53].Value = 95
	out := DownsampleMinMax(in, 10)
	found := false
	for _, sample := range out { if sample.Value == 95 { found = true } }
	if !found { t.Fatal("spike was discarded") }
	if len(out) > 20 { t.Fatalf("points = %d", len(out)) }
}
```

- [ ] **Step 2: Confirm the red state**

```bash
go test ./internal/telemetry/history
```

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement fixed capacity and chronological min/max buckets**

Use:

```go
func New(capacity int) *Ring
func (r *Ring) Append(sample telemetry.Sample)
func (r *Ring) Snapshot() []telemetry.Sample
func DownsampleMinMax(samples []telemetry.Sample, pixelColumns int) []telemetry.Sample
```

`New` panics with `history: capacity must be positive` below one. `Snapshot` returns a copy, oldest first. `DownsampleMinMax` returns empty for empty input or non-positive width, returns a copy when `len <= width*2`, otherwise emits chronological minimum and maximum values for each contiguous bucket.

- [ ] **Step 4: Verify and commit**

```bash
gofmt -w internal/telemetry/history
go test ./internal/telemetry/history
go test -race ./internal/telemetry/history
git add internal/telemetry/history
git commit -m "feat: add bounded telemetry history"
```

Expected: PASS.

### Task 6: Implement Batched Linux Providers

**Files:**
- Create: `internal/providers/linuxfs/filesystem.go`
- Create: `internal/providers/linuxfs/cpufreq.go`
- Create: `internal/providers/linuxfs/cpufreq_test.go`
- Create: `internal/providers/linuxfs/hwmon.go`
- Create: `internal/providers/linuxfs/hwmon_test.go`
- Create: `internal/providers/linuxfs/procstat.go`
- Create: `internal/providers/linuxfs/procstat_test.go`
- Create: `internal/providers/linuxfs/testdata/`

**Interfaces:**
- Consumes: filesystem root and requested `MetricID` values
- Produces: `NewCPUFreq(root string) *CPUFreq`, `NewHWMon(root string) *HWMon`, `NewProcStat(root string) *ProcStat`; each concrete type implements `telemetry.Provider`

- [ ] **Step 1: Create fixtures and failing tests**

Fixtures:

```text
sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq = 4200000
sys/devices/system/cpu/cpu1/cpufreq/scaling_cur_freq = 3100000
sys/class/hwmon/hwmon0/name = coretemp
sys/class/hwmon/hwmon0/temp1_label = Package id 0
sys/class/hwmon/hwmon0/temp1_input = 67000
sys/class/hwmon/hwmon0/temp2_label = Core 0
sys/class/hwmon/hwmon0/temp2_input = 64000
proc/stat = cpu  100 0 50 850 0 0 0 0 0 0
```

Tests assert CPU metric IDs `cpu.0.frequency` and `cpu.1.frequency`, sampled values 4200 and 3100 MHz, hwmon values 67 and 64 °C, and utilization calculated from the delta between two proc snapshots.

- [ ] **Step 2: Confirm the red state**

```bash
go test ./internal/providers/linuxfs
```

Expected: FAIL because constructors are absent.

- [ ] **Step 3: Implement root-confined filesystem access**

```go
type FileSystem interface {
	ReadFile(path string) ([]byte, error)
	ReadDir(path string) ([]os.DirEntry, error)
}

type RootFS struct { Root string }
```

`RootFS` cleans relative paths, joins beneath `Root`, and rejects any path whose cleaned form is `..` or starts with `../`.

Provider constructors are exactly:

```go
func NewCPUFreq(root string) *CPUFreq
func NewHWMon(root string) *HWMon
func NewProcStat(root string) *ProcStat
```

- [ ] **Step 4: Implement CPU frequency and hwmon batches**

`CPUFreq.Discover` enumerates CPU directories, sorts numeric CPU IDs, and maps metric IDs to exact source paths. `Sample` reads only requested IDs in one provider invocation and converts kHz to MHz.

`HWMon.Discover` reads chip names, enumerates `temp*_input` and `fan*_input`, uses labels when present, and assigns `°C` or `RPM`. A failed file produces one unavailable sample while successful sibling samples remain good.

- [ ] **Step 5: Implement delta-based procstat utilization**

Store previous aggregate and per-core counters under a mutex. The first sample is unavailable with `utilization requires two snapshots`. Subsequent values use:

```go
utilization := float64(deltaTotal-deltaIdle) / float64(deltaTotal) * 100
```

Counter regression and zero delta produce unavailable samples with explicit errors.

- [ ] **Step 6: Test malformed and disappearing sensors**

Add table tests for whitespace, malformed integers, missing labels, disappearing files, zero proc delta, and counter regression. Run:

```bash
gofmt -w internal/providers/linuxfs
go test ./internal/providers/linuxfs
go test -race ./internal/providers/linuxfs
go test ./...
! rg -n "exec\.Command|sh -c|sensors" internal/providers
```

Expected: PASS and no shell polling matches.

- [ ] **Step 7: Commit**

```bash
git add internal/providers/linuxfs
git commit -m "feat: add batched Linux telemetry providers"
```

### Task 7: Build the Lazy XTU Classic Shell

**Files:**
- Create: `cmd/studio/main.go`
- Create: `internal/ui/desktop.go`
- Create: `internal/ui/shell.go`
- Create: `internal/ui/shell_test.go`
- Create: `internal/ui/navigation.go`
- Create: `internal/ui/navigation_test.go`
- Create: `internal/ui/pages/placeholder.go`
- Create: `internal/ui/theme.go`

**Interfaces:**
- Consumes: `product.Info`, `telemetry.Scheduler`
- Produces: `ui.NewDesktop`, `Desktop.Run`, `Page`, `PageFactory`, `NewLazyNavigator`, `LazyNavigator.Select`

- [ ] **Step 1: Write failing lazy navigation tests**

```go
func TestLazyNavigatorCreatesPageOnce(t *testing.T) {
	created := 0
	n := NewLazyNavigator([]PageFactory{{
		ID: "monitor",
		Create: func() Page { created++; return &fakePage{id: "monitor"} },
	}})
	if created != 0 { t.Fatalf("eager creations = %d", created) }
	p1, err := n.Select("monitor")
	if err != nil { t.Fatal(err) }
	p2, err := n.Select("monitor")
	if err != nil { t.Fatal(err) }
	if created != 1 || p1 != p2 { t.Fatalf("created=%d same=%v", created, p1 == p2) }
}
```

Add `TestSelectionDeactivatesPreviousPage`, asserting the previous page receives exactly one `Deactivate` and the new page exactly one `Activate`.

The interface is:

```go
type Page interface {
	ID() string
	Object() fyne.CanvasObject
	Activate()
	Deactivate()
}
```

- [ ] **Step 2: Confirm the red state**

```bash
go test ./internal/ui
```

Expected: FAIL because navigation is absent.

- [ ] **Step 3: Implement lazy navigation and shell layout**

`PageFactory` contains `ID`, `Label`, `Icon`, and `Create`. `Select` caches the page, deactivates the prior page, activates the selected page, and errors on an unknown ID.

Implement:

```go
func NewShell(info product.Info, scheduler *telemetry.Scheduler) *Shell
```

The left rail contains Overview, Monitor, Tune, Stress Tests, Profiles, Reports, Hardware, Logs. Task 7 registers lazy placeholders for every destination; Tasks 8 and 9 replace the Overview, Monitor, and Hardware factories with concrete pages. The center swaps page content. The right rail displays profile `Stock`, unavailable read-only PL1/PL2 fields, provider status, and disabled Apply/Revert buttons.

- [ ] **Step 4: Add the desktop entry point**

```go
package main

import (
	"log"
	"github.com/noteMASTER11/undervolt-go-studio/internal/product"
	"github.com/noteMASTER11/undervolt-go-studio/internal/ui"
)

var version = "dev"

func main() {
	if err := ui.NewDesktop(product.Current(version)).Run(); err != nil { log.Fatal(err) }
}
```

`NewDesktop` creates Linux providers and the scheduler. `Run` creates app ID `io.github.notemaster11.UndervoltGoStudio`, shows the window immediately in loading state, discovers providers asynchronously, starts the scheduler, and cancels its application context in the close intercept before closing.

- [ ] **Step 5: Verify headless construction and builds**

Use `fyne.io/fyne/v2/test.NewApp()` in tests and call `Quit` with `defer`. Run:

```bash
gofmt -w cmd/studio internal/ui
go test ./internal/ui
go build -o build/undervolt-go-studio ./cmd/studio
go test ./...
```

Expected: PASS and a Studio binary.

- [ ] **Step 6: Commit**

```bash
git add cmd/studio internal/ui
git commit -m "feat: add lazy XTU-style application shell"
```

### Task 8: Connect Overview and Monitor Asynchronously

**Files:**
- Create: `internal/ui/viewmodel/overview.go`
- Create: `internal/ui/viewmodel/overview_test.go`
- Create: `internal/ui/viewmodel/monitor.go`
- Create: `internal/ui/viewmodel/monitor_test.go`
- Create: `internal/ui/components/metric_card.go`
- Create: `internal/ui/components/timeline.go`
- Create: `internal/ui/components/timeline_test.go`
- Create: `internal/ui/pages/overview.go`
- Create: `internal/ui/pages/monitor.go`
- Modify: `internal/ui/shell.go`
- Modify: `internal/ui/desktop.go`

**Interfaces:**
- Consumes: scheduler catalog, subscription handles, history ring
- Produces: `NewOverview`, `NewMonitor`, `Monitor.SetInterval`, lazy subscription lifecycle, timeline updates

- [ ] **Step 1: Write failing lifecycle tests**

```go
func TestMonitorSubscribesOnlyWhileActive(t *testing.T) {
	source := &fakeSource{}
	vm := NewMonitor(source, 250*time.Millisecond)
	if source.subscribeCalls != 0 { t.Fatal("eager subscription") }
	vm.Activate()
	if source.subscribeCalls != 1 { t.Fatalf("subscribe calls = %d", source.subscribeCalls) }
	vm.Deactivate()
	if source.closeCalls != 1 { t.Fatalf("close calls = %d", source.closeCalls) }
}

func TestMonitorIntervalClamps(t *testing.T) {
	vm := NewMonitor(&fakeSource{}, 250*time.Millisecond)
	vm.SetInterval(10*time.Millisecond)
	if vm.Interval() != 100*time.Millisecond { t.Fatalf("low = %s", vm.Interval()) }
	vm.SetInterval(9*time.Second)
	if vm.Interval() != 5*time.Second { t.Fatalf("high = %s", vm.Interval()) }
}
```

View models consume:

```go
type SubscriptionSource interface {
	Subscribe(telemetry.Subscription) *telemetry.Handle
}
```

- [ ] **Step 2: Confirm the red state**

```bash
go test ./internal/ui/viewmodel ./internal/ui/components
```

Expected: FAIL because the packages are absent.

- [ ] **Step 3: Implement immutable view-model state**

Overview subscribes to package temperature, aggregate utilization, aggregate frequency, and fan metrics when available. Monitor owns selected metric IDs and interval. Both consume frames off the UI thread, update bounded histories, and publish copied state with:

```go
type StateListener[T any] func(T)
func (m *Monitor) SetListener(listener StateListener[MonitorState])
```

Activation is idempotent. Deactivation closes the handle and joins its consumer goroutine. Interval or selection changes replace the subscription without leaking handles.

- [ ] **Step 4: Implement metric cards and a bounded timeline**

Unavailable cards show `—`; stale cards retain the last value and show a stale badge. Timeline input is:

```go
type Series struct {
	ID telemetry.MetricID
	Label string
	Unit telemetry.Unit
	Color color.Color
	Points []telemetry.Sample
}

func (t *Timeline) SetSeries(series []Series)
```

`SetSeries` copies its input and refreshes one canvas object. Paint code performs no I/O, provider call, or downsampling.

- [ ] **Step 5: Build the pages and marshal one batched UI update**

Overview has CPU temperature, utilization, effective frequency, fan cards, and a 60-second timeline. Monitor has interval options 100 ms, 250 ms, 500 ms, 1 s, 2 s, 5 s; searchable metrics grouped by device; pause/resume; timeline; current values; explicit loading/stale/unavailable/error states.

Register listeners exactly through the Fyne dispatcher:

```go
vm.SetListener(func(state viewmodel.MonitorState) {
	fyne.Do(func() { page.render(state) })
})
```

No page event handler or render method calls a provider.

- [ ] **Step 6: Verify lifecycle, races, and build**

```bash
gofmt -w internal/ui
go test ./internal/ui/...
go test -race ./internal/ui/viewmodel ./internal/telemetry/...
go build -o build/undervolt-go-studio ./cmd/studio
```

Expected: PASS with no races.

- [ ] **Step 7: Commit**

```bash
git add internal/ui
git commit -m "feat: connect lazy live monitoring pages"
```

### Task 9: Add Hardware Diagnostics and Performance Guards

**Files:**
- Create: `internal/ui/pages/hardware.go`
- Create: `internal/ui/pages/hardware_test.go`
- Create: `internal/telemetry/scheduler_benchmark_test.go`
- Create: `internal/telemetry/scheduler_soak_test.go`
- Modify: `internal/ui/shell.go`

**Interfaces:**
- Consumes: `Scheduler.Diagnostics`, discovered catalog
- Produces: lazy diagnostics page, 200-metric benchmark, subscription-churn test

- [ ] **Step 1: Write failing diagnostics mapping test**

```go
func TestHardwareRowsExposeProviderHealth(t *testing.T) {
	in := []telemetry.ProviderDiagnostics{{
		ProviderID: "linux.hwmon", State: "backoff", LastError: "permission denied",
		ActiveMetrics: 3, ActiveConsumers: 1, DroppedFrames: 2,
	}}
	rows := diagnosticsRows(in)
	if len(rows) != 1 { t.Fatalf("rows = %d", len(rows)) }
	if rows[0].Provider != "linux.hwmon" || rows[0].State != "backoff" || rows[0].Dropped != 2 {
		t.Fatalf("row = %+v", rows[0])
	}
}
```

- [ ] **Step 2: Confirm the red state**

```bash
go test ./internal/ui/pages
```

Expected: FAIL because diagnostics mapping is absent.

- [ ] **Step 3: Implement lazy diagnostics**

Render a virtualized device/metric list and provider table with provider ID, state, last success, latency, active metrics, consumers, dropped frames, and last error. Refresh once per second only while active; `Deactivate` stops the ticker.

Copy Diagnostics produces JSON with product version, device catalog, metric catalog, and provider diagnostics. It excludes environment variables and home-directory paths.

- [ ] **Step 4: Add bounded-load benchmark and subscription soak test**

`BenchmarkScheduler200Metrics` subscribes to 200 fake metrics, keeps one intentionally slow consumer, reports allocations, and checks handle channel capacity is one. `TestSchedulerSubscriptionSoak` runs two seconds under the race detector, repeatedly adds and closes subscriptions, then asserts workers return to `idle` and goroutine count returns within five of baseline after garbage collection.

```bash
go test -run TestSchedulerSubscriptionSoak -race ./internal/telemetry
go test -run '^$' -bench BenchmarkScheduler200Metrics -benchmem ./internal/telemetry
```

Expected: PASS, bounded channels, no race.

- [ ] **Step 5: Verify and commit**

```bash
gofmt -w internal/ui/pages internal/telemetry
go test ./internal/ui/pages ./internal/telemetry
go test -race ./internal/telemetry
git add internal/ui internal/telemetry
git commit -m "feat: add telemetry diagnostics and guards"
```

### Task 10: Add CI, Documentation, and Milestone Review

**Files:**
- Modify: `.github/workflows/go.yml`
- Modify: `README.md`
- Verify: every file changed by Tasks 1–9

**Interfaces:**
- Consumes: complete read-only MVP
- Produces: quality gates, alpha instructions, review evidence

- [ ] **Step 1: Add pull-request quality gates**

Keep tagged legacy releases intact in this milestone. Add a `quality` job on pushes and pull requests with the repository's existing X11/OpenGL build dependencies, then these steps:

```yaml
- name: Unit tests
  run: go test ./...
- name: Telemetry race tests
  run: go test -race ./internal/telemetry/... ./internal/providers/...
- name: Vet
  run: go vet ./...
- name: Build Studio
  run: go build -o build/undervolt-go-studio ./cmd/studio
- name: Build legacy CLI
  run: go build ./...
- name: Build legacy GUI
  run: go build -tags gui ./...
```

- [ ] **Step 2: Document the alpha workflow and scope**

Add:

```bash
go test ./...
go build -o build/undervolt-go-studio ./cmd/studio
./build/undervolt-go-studio
```

State that this milestone is read-only, needs no root access, downloads nothing at runtime, and samples only metrics subscribed by active pages. Link the design and this plan.

- [ ] **Step 3: Run full milestone verification**

```bash
gofmt -w cmd internal
go test ./...
go test -race ./internal/telemetry/... ./internal/providers/... ./internal/ui/viewmodel
go vet ./...
go build -o build/undervolt-go-studio ./cmd/studio
go build ./...
go build -tags gui ./...
git diff --check
```

Expected: every command exits 0.

- [ ] **Step 4: Perform the normal-user smoke test**

Launch without `sudo` or `pkexec` and verify:

```text
Window appears before discovery finishes.
Monitor is created only when selected.
Interval changes affect only subscribed metrics.
Closing Monitor releases page-only subscriptions.
CPU frequency and hwmon metrics update without shell children.
Hardware shows latency, state, consumers, and dropped frames.
A failed provider does not freeze navigation or other providers.
Tune is read-only and Apply remains disabled.
```

- [ ] **Step 5: Commit and push the milestone**

```bash
git add .github/workflows/go.yml README.md
git commit -m "ci: verify read-only Studio milestone"
git push -u origin feature/foundation-telemetry
git status --short
```

Expected: clean feature worktree and remote branch present.

- [ ] **Step 6: Request code review**

Invoke `superpowers:requesting-code-review` for `main...feature/foundation-telemetry`. Resolve correctness and safety findings before merging or starting the privileged-tuning plan. Record verification output and smoke-test evidence in the pull-request description; do not claim tuning, stress, reports, or packaging are implemented.
