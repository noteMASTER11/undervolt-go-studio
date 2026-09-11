# Intel Tuning Backend and Tune Workbench Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver a responsive XTU-style Tune workbench and a kernel-first, capability-driven Intel tuning backend with a short-lived `pkexec` helper, verified temporary changes, lease-based rollback, and explicit support reporting on the Intel Core Ultra 9 275HX.

**Architecture:** The unprivileged GUI discovers kernel-backed capabilities asynchronously and stages semantic changes locally. A fixed-path helper starts only after review, resolves semantic controls through kernel or allow-listed family 6/model `0xC6` drivers, snapshots every affected source, applies in safe order, verifies read-back, and owns a ten-second rollback lease. Raw sysfs paths and MSR addresses never cross the protocol boundary.

**Tech Stack:** Go 1.24.1, Fyne 2.7.4, Linux powercap/sysfs, `intel_pstate`, `intel_tcc_cooling`, `/dev/cpu/*/msr`, `pkexec`/PolicyKit, Go standard-library framed JSON, `golang.org/x/sys/unix`, Go tests and race detector.

**Spec:** `docs/superpowers/specs/2026-09-11-intel-tuning-backend-design.md`

## Global Constraints

- Product name is **Undervolt Go Studio** and target platform is Linux x86-64.
- The GUI and stress workers never run as root.
- Privilege is obtained only after Review & Apply confirmation by launching `/usr/libexec/undervolt-go-studio-helper --session --protocol=1` through `pkexec` without a shell.
- The protocol contains semantic control IDs and typed values only; it never accepts raw commands, paths, MSR addresses, masks, or arbitrary arguments.
- Kernel interfaces are preferred. Raw MSR access is restricted to explicit model tables and masked read-modify-write operations.
- Family 6/model `0xC6` stepping 2 is the initial hardware validation target. An unknown CPU model cannot reach a raw write.
- Positive voltage offsets and ratio increases above the captured firmware values are rejected.
- Apply order is power/thermal policy, ratio reductions, then negative voltage offset. Rollback order is the reverse.
- Every mutation captures stock state, validates bounds, reads back the effective value, and remains temporary under a ten-second helper-owned lease.
- No boot-time auto-apply, custom kernel module, BIOS-variable patching, or runtime download is added by this plan.
- Capability discovery and telemetry are cancellable and never perform blocking work or widget mutation on Fyne's main thread.
- Missing values remain missing or stale; unavailable data is never represented as zero.
- Hardware mutation tests are opt-in and must prove final restoration before passing.

## Scope decomposition

This plan covers the Intel tuning boundary and Tune page only. It produces a runnable application after every task. The packaged CPU/FPU/cache/memory/Vulkan stress engines and 30-minute combined CPU/GPU release gate are implemented by the separate Stress and Safety plan after this backend is reviewed.

## File map

```text
internal/tuning/model.go                 semantic capabilities, values, changes, events
internal/tuning/validate.go              value normalization and cross-control validation
internal/tuning/driver.go                private driver and prepared-operation contracts
internal/tuning/sysfs/store.go           root-confined read/write sysfs abstraction
internal/tuning/testkit/                 deterministic fake store, MSR device, and drivers
internal/tuning/intel/identity.go         CPU identity and hybrid topology
internal/tuning/intel/cpuid_amd64.go      safe per-CPU CPUID wrapper
internal/tuning/intel/cpuid_amd64.s       amd64 CPUID instruction shim
internal/tuning/intel/powercap/           PL1, PL2, and time-window kernel driver
internal/tuning/intel/policy/             intel_pstate EPP driver
internal/tuning/intel/thermal/            intel_tcc_cooling driver
internal/tuning/intel/msr/                device, codecs, model table, ratio, voltage
internal/tuning/discovery.go              concurrent driver probe and capability generations
internal/tuning/transaction.go            prepare, snapshot, apply, verify, rollback
internal/tuning/recovery.go               root-owned recovery records under /run
internal/privilege/protocol/              bounded framed JSON wire format
internal/privilege/session/               helper state machine and lease owner
internal/privilege/client/                fixed pkexec launcher and GUI client
cmd/helper/main.go                        privileged helper entry point
internal/events/store.go                  bounded semantic tuning-event log
internal/ui/viewmodel/tune.go              async discovery, staging, review, session state
internal/ui/components/tune_control.go     capability-driven control cards
internal/ui/components/pending.go          staged-change rail
internal/ui/pages/tune.go                  XTU Workbench page
internal/ui/pages/logs.go                  human-readable event page
internal/ui/shell.go                       lazy Tune/Logs wiring
internal/ui/desktop.go                     tuning lifecycle and close-time rollback
cmd/ui-snapshot/main.go                    deterministic Tune screenshot state
packaging/polkit/*.policy                  fixed helper authorization policy
cmd/tuning-smoke/main.go                   explicit opt-in 275HX validation harness
docs/images/studio-*.png                   project-owned headless UI screenshots
README.md                                  current Studio overview and upstream attribution
```

---

### Task 1: Define the semantic tuning model and validator

**Files:**
- Create: `internal/tuning/model.go`
- Create: `internal/tuning/model_test.go`
- Create: `internal/tuning/validate.go`
- Create: `internal/tuning/validate_test.go`
- Create: `internal/tuning/driver.go`

**Interfaces:**
- Consumes: `context.Context`, `encoding/json`, `time.Time`
- Produces: `tuning.ControlID`, `Value`, `Capability`, `CapabilitySet`, `Change`, `ChangeSet`, `Event`, `Driver`, `PreparedOperation`, `ValidateChangeSet`

- [ ] **Step 1: Write failing model and normalization tests**

```go
func TestNormalizeNumericClampsToRepresentableStep(t *testing.T) {
	capability := Capability{ID: ControlPL1, Unit: UnitWatt, State: StateSupported,
		Range: &NumericRange{Minimum: 15, Maximum: 55, Step: 0.125}}
	got, adjusted, err := capability.Normalize(NumericValue(44.06))
	if err != nil { t.Fatal(err) }
	if got.Number != 44 || !adjusted { t.Fatalf("got %+v adjusted=%v", got, adjusted) }
}

func TestNormalizeRejectsPositiveVoltage(t *testing.T) {
	capability := Capability{ID: ControlVoltageCore, Unit: UnitMilliVolt, State: StateSupported,
		Range: &NumericRange{Minimum: -250, Maximum: 0, Step: 0.9765625}}
	if _, _, err := capability.Normalize(NumericValue(10)); err == nil {
		t.Fatal("positive voltage offset accepted")
	}
}
```

- [ ] **Step 2: Run the tests and confirm the red state**

Run: `go test ./internal/tuning -run 'TestNormalize'`

Expected: FAIL because the tuning package and types do not exist.

- [ ] **Step 3: Implement the domain types and driver boundary**

```go
type ControlID string
type CapabilityState string
type Unit string
type ValueKind string

const (
	ControlPL1 ControlID = "intel.package.pl1"
	ControlPL2 ControlID = "intel.package.pl2"
	ControlTau ControlID = "intel.package.tau"
	ControlEPP ControlID = "intel.policy.epp"
	ControlThermalLimit ControlID = "intel.package.thermal_limit"
	ControlRatioPCore ControlID = "intel.ratio.pcore"
	ControlRatioECore ControlID = "intel.ratio.ecore"
	ControlVoltageCore ControlID = "intel.voltage.core"
	ControlVoltageCache ControlID = "intel.voltage.cache"
)

const (
	StateSupported CapabilityState = "supported"
	StateReadOnly CapabilityState = "read_only"
	StateFirmwareLocked CapabilityState = "firmware_locked"
	StateKernelBlocked CapabilityState = "kernel_blocked"
	StateRequiresProbe CapabilityState = "requires_probe"
	StateUnavailable CapabilityState = "unavailable"
	StateUnknownModel CapabilityState = "unknown_model"
)

const (
	UnitWatt Unit = "W"; UnitSecond Unit = "s"; UnitCelsius Unit = "°C"
	UnitRatio Unit = "ratio"; UnitMilliVolt Unit = "mV"; UnitChoice Unit = "choice"
)

type Value struct { Kind ValueKind `json:"kind"`; Number float64 `json:"number,omitempty"`; Choice string `json:"choice,omitempty"`; Vector []float64 `json:"vector,omitempty"` }
func NumericValue(number float64) Value
func ChoiceValue(choice string) Value
func VectorValue(vector []float64) Value
type NumericRange struct { Minimum, Maximum, Step float64 }
type Capability struct { ID ControlID; Scope, Label string; Unit Unit; State CapabilityState; Current Value; Range *NumericRange; Choices []string; DriverID, ReasonCode, Reason string; RequiresPrivilege, Experimental bool; ObservedAt time.Time }
type CapabilitySet struct { Generation, MachineID string; Capabilities []Capability }
type Change struct { ID ControlID `json:"id"`; Requested Value `json:"requested"` }
type ChangeSet struct { Generation, MachineID string; Changes []Change }
type Adjustment struct { ID ControlID; Requested, Normalized Value }
type ValidationResult struct { ChangeSet ChangeSet; Adjustments []Adjustment }
type Event struct { Time time.Time; Kind, Message, Detail string; ControlID ControlID; Capabilities *CapabilitySet; Effective, Remaining map[ControlID]Value }

const (
	OrderPower = 10; OrderPolicy = 15; OrderThermal = 20; OrderRatio = 30; OrderVoltage = 40
)

var (
	ErrCrossField = errors.New("cross-field validation failed")
	ErrStaleCapabilities = errors.New("capability generation changed")
)

func ValidateChangeSet(capabilities CapabilitySet, changes ChangeSet) (ValidationResult, error)

type Driver interface {
	ID() string
	Probe(context.Context) ([]Capability, error)
	Prepare(context.Context, Change) (PreparedOperation, error)
	Restore(context.Context, ControlID, json.RawMessage) (Value, error)
}

type PreparedOperation interface {
	ControlID() ControlID
	DriverID() string
	Order() int
	Capture(context.Context) (json.RawMessage, error)
	Apply(context.Context) (Value, error)
	Restore(context.Context, json.RawMessage) (Value, error)
}
```

- [ ] **Step 4: Add cross-control validation tests**

```go
func TestValidateChangeSetRejectsPL1AbovePL2(t *testing.T) {
	caps := CapabilitySet{Generation: "g1", MachineID: "cpu", Capabilities: []Capability{
		{ID: ControlPL1, State: StateSupported, Range: &NumericRange{Minimum: 15, Maximum: 55, Step: 1}},
		{ID: ControlPL2, State: StateSupported, Current: NumericValue(45), Range: &NumericRange{Minimum: 15, Maximum: 160, Step: 1}},
	}}
	_, err := ValidateChangeSet(caps, ChangeSet{Generation: "g1", MachineID: "cpu", Changes: []Change{{ID: ControlPL1, Requested: NumericValue(50)}}})
	if !errors.Is(err, ErrCrossField) { t.Fatalf("err = %v", err) }
}

func TestValidateChangeSetRejectsStaleGeneration(t *testing.T) {
	_, err := ValidateChangeSet(CapabilitySet{Generation: "new", MachineID: "cpu"}, ChangeSet{Generation: "old", MachineID: "cpu"})
	if !errors.Is(err, ErrStaleCapabilities) { t.Fatalf("err = %v", err) }
}
```

- [ ] **Step 5: Implement deterministic normalization and validation**

Implement `Capability.Normalize` with `math.Round((value-minimum)/step)` and a `1e-9` tolerance. `ValidateChangeSet` rejects duplicate IDs, non-supported states, wrong value kinds, stale machine/generation IDs, positive voltage, ratio increases beyond `Current.Vector`, non-monotonic ratio vectors, and effective PL1 above effective PL2. It returns `ValidationResult`, whose normalized `ChangeSet` is passed to the transaction engine and whose `Adjustments` populate the review dialog.

- [ ] **Step 6: Verify and commit**

```bash
gofmt -w internal/tuning/*.go
go test ./internal/tuning
git add internal/tuning
git commit -m "feat: define semantic tuning model"
```

Expected: PASS.

### Task 2: Add confined sysfs access and CPU identity/topology

**Files:**
- Create: `internal/tuning/sysfs/store.go`
- Create: `internal/tuning/sysfs/store_test.go`
- Create: `internal/tuning/intel/identity.go`
- Create: `internal/tuning/intel/identity_test.go`
- Create: `internal/tuning/intel/cpuid_amd64.go`
- Create: `internal/tuning/intel/cpuid_amd64.s`
- Create: `internal/tuning/testkit/store.go`

**Interfaces:**
- Consumes: filesystem root, `unix.CPUSet`, CPUID leaves 0, 1, and `0x1A`
- Produces: `sysfs.Store`, `sysfs.RootStore`, `intel.Identity`, `intel.Core`, `intel.DetectIdentity`, `intel.DetectTopology`

- [ ] **Step 1: Write path-confinement tests**

```go
func TestRootStoreRejectsEscape(t *testing.T) {
	root := t.TempDir()
	store := RootStore{Root: root}
	for _, path := range []string{"../etc/passwd", "/etc/passwd", "sys/../../etc/passwd"} {
		if _, err := store.Read(path); err == nil { t.Fatalf("Read(%q) succeeded", path) }
		if err := store.Write(path, []byte("x")); err == nil { t.Fatalf("Write(%q) succeeded", path) }
	}
}
```

- [ ] **Step 2: Implement the store and reusable fake**

```go
type Store interface {
	Read(path string) ([]byte, error)
	Write(path string, value []byte) error
	List(path string) ([]string, error)
	Resolve(path string) (string, error)
}

type RootStore struct { Root string }
```

`RootStore.resolve` must clean the relative path, reject absolute paths and `..`, resolve symlinks, and verify the result remains beneath the configured root before every read or write. `testkit.MemoryStore` stores byte slices by normalized relative path, returns sorted child names, records writes, and supports per-operation injected errors.

```go
type MemoryStore struct { Files map[string][]byte; Writes []StoreWrite; Failures map[string]error }
type StoreWrite struct { Path string; Value []byte }
func NewMemoryStore(files map[string]string) *MemoryStore
func (s *MemoryStore) Fail(operation, path string, err error)
```

- [ ] **Step 3: Write CPU signature and hybrid-core tests**

```go
func TestDecodeSignatureC0662(t *testing.T) {
	got := DecodeSignature(0x000c0662)
	if got.Family != 6 || got.Model != 0xc6 || got.Stepping != 2 { t.Fatalf("identity = %+v", got) }
}

func TestCoreTypeFromLeaf1A(t *testing.T) {
	if got := CoreTypeFromLeaf1A(0x40000000); got != CorePerformance { t.Fatalf("got %q", got) }
	if got := CoreTypeFromLeaf1A(0x20000000); got != CoreEfficiency { t.Fatalf("got %q", got) }
}
```

- [ ] **Step 4: Implement CPUID and per-CPU affinity safely**

```go
func cpuid(eax, ecx uint32) (a, b, c, d uint32)

func CPUIDOnCPU(cpu int, eax, ecx uint32) (a, b, c, d uint32, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var old, one unix.CPUSet
	if err = unix.SchedGetaffinity(0, &old); err != nil { return }
	one.Set(cpu)
	if err = unix.SchedSetaffinity(0, &one); err != nil { return }
	defer unix.SchedSetaffinity(0, &old)
	a, b, c, d = cpuid(eax, ecx)
	return
}
```

`cpuid_amd64.s` implements the Go ABI wrapper with `CPUID` and returns EAX/EBX/ECX/EDX. `DetectTopology` reads online CPU IDs, queries leaf `0x1A` on each, and cross-checks `cpuinfo_max_freq`. An unknown leaf remains `CoreUnknown`; frequency alone may support a diagnostic hint but cannot authorize a model-specific write.

- [ ] **Step 5: Verify identity on the development machine without privileges**

```bash
gofmt -w internal/tuning/sysfs/*.go internal/tuning/intel/*.go internal/tuning/testkit/*.go
go test ./internal/tuning/sysfs ./internal/tuning/intel
go test -race ./internal/tuning/sysfs ./internal/tuning/intel
```

Expected: tests report family 6/model `0xC6` from fixtures; no test opens `/dev/cpu/*/msr`.

- [ ] **Step 6: Commit**

```bash
git add internal/tuning/sysfs internal/tuning/intel internal/tuning/testkit
git commit -m "feat: detect Intel identity and hybrid topology"
```

### Task 3: Implement kernel powercap controls

**Files:**
- Create: `internal/tuning/intel/powercap/powercap.go`
- Create: `internal/tuning/intel/powercap/discovery.go`
- Create: `internal/tuning/intel/powercap/operation.go`
- Create: `internal/tuning/intel/powercap/powercap_test.go`

**Interfaces:**
- Consumes: `sysfs.Store`, fixed root `sys/class/powercap`
- Produces: `powercap.New(store) tuning.Driver`, capabilities for PL1, PL2, and Tau

- [ ] **Step 1: Write duplicate-zone discovery tests**

```go
func TestProbeCombinesMSRAndMMIOPackageConstraints(t *testing.T) {
	store := testkit.NewPowercapStore(map[string]string{
		"intel-rapl:0/name": "package-0\n", "intel-rapl:0/constraint_0_name": "long_term\n",
		"intel-rapl:0/constraint_0_power_limit_uw": "44000000\n", "intel-rapl:0/constraint_0_max_power_uw": "55000000\n",
		"intel-rapl-mmio:0/name": "package-0\n", "intel-rapl-mmio:0/constraint_0_name": "long_term\n",
		"intel-rapl-mmio:0/constraint_0_power_limit_uw": "44000000\n", "intel-rapl-mmio:0/constraint_0_max_power_uw": "55000000\n",
	})
	caps, err := New(store).Probe(context.Background())
	if err != nil { t.Fatal(err) }
	pl1 := capabilityByID(caps, tuning.ControlPL1)
	if pl1.Current.Number != 44 || pl1.Range.Maximum != 55 { t.Fatalf("PL1 = %+v", pl1) }
}
```

- [ ] **Step 2: Add zero/empty maximum and semantic-name tests**

Test that `short_term` maps to PL2 regardless of index, an empty maximum does not parse as zero, and a literal `0` maximum produces a nil/unknown upper bound while retaining the current value.

- [ ] **Step 3: Implement discovery with fixed roots and source grouping**

```go
type Driver struct { store sysfs.Store; zones map[tuning.ControlID][]constraint }
func New(store sysfs.Store) *Driver
func (d *Driver) ID() string { return "intel.powercap" }
func (d *Driver) Probe(ctx context.Context) ([]tuning.Capability, error)
func (d *Driver) Prepare(ctx context.Context, change tuning.Change) (tuning.PreparedOperation, error)
```

Enumerate both `intel-rapl:*` and `intel-rapl-mmio:*` package zones, resolve symlinks beneath the root, and group every active source by semantic constraint name. The displayed effective limit is the minimum verified active source. Do not infer a 160 W writable maximum from Intel ARK.

- [ ] **Step 4: Write apply/read-back/restore tests**

```go
func TestPL1OperationWritesEveryParticipatingSourceAndRestoresEachValue(t *testing.T) {
	// Fixture begins with MSR=44 W and MMIO=45 W.
	op := preparePL1(t, 40)
	raw, err := op.Capture(context.Background()); if err != nil { t.Fatal(err) }
	effective, err := op.Apply(context.Background()); if err != nil { t.Fatal(err) }
	if effective.Number != 40 { t.Fatalf("effective = %+v", effective) }
	if _, err := op.Restore(context.Background(), raw); err != nil { t.Fatal(err) }
	assertWatts(t, "intel-rapl:0/...", 44)
	assertWatts(t, "intel-rapl-mmio:0/...", 45)
}
```

Inject an error into the second source and assert the operation restores the first source before returning.

- [ ] **Step 5: Implement multi-source operation and verification**

Capture a JSON array of `{path, value}` records. Apply all participating sources in deterministic MSR-then-MMIO order for reductions, read each path back, and return the minimum effective value. Restore each captured path in reverse write order and verify it independently.

- [ ] **Step 6: Verify and commit**

```bash
gofmt -w internal/tuning/intel/powercap/*.go
go test ./internal/tuning/intel/powercap
git add internal/tuning/intel/powercap
git commit -m "feat: add kernel power limit controls"
```

### Task 4: Implement EPP and TCC thermal controls

**Files:**
- Create: `internal/tuning/intel/policy/epp.go`
- Create: `internal/tuning/intel/policy/epp_test.go`
- Create: `internal/tuning/intel/thermal/tcc.go`
- Create: `internal/tuning/intel/thermal/tcc_test.go`

**Interfaces:**
- Consumes: `sysfs.Store`, fixed CPU policy and thermal roots
- Produces: drivers `intel.epp` and `intel.tcc`

- [ ] **Step 1: Write EPP discovery and all-policy restore tests**

```go
func TestEPPAppliesSameChoiceToEveryPolicyAndRestoresIndividually(t *testing.T) {
	driver := New(testkit.NewEPPStore([]string{"default", "performance"}, map[string]string{"policy0": "default", "policy8": "performance"}))
	op, err := driver.Prepare(context.Background(), tuning.Change{ID: tuning.ControlEPP, Requested: tuning.ChoiceValue("balance_power")})
	if err != nil { t.Fatal(err) }
	raw, _ := op.Capture(context.Background())
	_, err = op.Apply(context.Background()); if err != nil { t.Fatal(err) }
	_, err = op.Restore(context.Background(), raw); if err != nil { t.Fatal(err) }
	assertPolicy(t, "policy0", "default"); assertPolicy(t, "policy8", "performance")
}
```

The fixture must expose `balance_power` in every policy's available choices. Add a test that the common choice list is the intersection, not the union.

- [ ] **Step 2: Implement EPP without changing the governor**

Discover policy directories, current preferences, and the common available choices. A permission or governor rejection maps to `StateKernelBlocked`; do not write `scaling_governor`. Snapshot and restore every policy separately.

- [ ] **Step 3: Write TCC conversion and bounds tests**

```go
func TestTCCCapabilityUsesEffectiveCeiling(t *testing.T) {
	driver := New(storeWithTjMaxAndCoolingState(105, 10, 127))
	caps, err := driver.Probe(context.Background()); if err != nil { t.Fatal(err) }
	cap := caps[0]
	if cap.Current.Number != 95 || cap.Range.Maximum != 105 { t.Fatalf("cap = %+v", cap) }
}

func TestTCCWritesOffsetAndReadsBackCeiling(t *testing.T) {
	op := prepareThermal(t, 90) // TjMax 105 => offset 15.
	_, err := op.Apply(context.Background()); if err != nil { t.Fatal(err) }
	assertCoolingState(t, 15)
}
```

- [ ] **Step 4: Implement the TCC driver**

Find the cooling device whose `type` is exactly `TCC Offset`, read `max_state` and `cur_state`, find the single fixed-root PCI attribute matching `sys/bus/pci/devices/*/tcc_offset_degree_celsius`, and correlate package `temp*_crit` from the `coretemp` hwmon device. Convert user ceiling to offset, write the PCI `tcc_offset_degree_celsius` attribute, then verify both that attribute and the cooling device `cur_state`. If the two kernel interfaces disagree, report `StateReadOnly` with both diagnostic values.

- [ ] **Step 5: Verify and commit**

```bash
gofmt -w internal/tuning/intel/policy/*.go internal/tuning/intel/thermal/*.go
go test ./internal/tuning/intel/policy ./internal/tuning/intel/thermal
git add internal/tuning/intel/policy internal/tuning/intel/thermal
git commit -m "feat: add Intel policy and thermal controls"
```

### Task 5: Build the allow-listed MSR foundation

**Files:**
- Create: `internal/tuning/intel/msr/device.go`
- Create: `internal/tuning/intel/msr/device_linux.go`
- Create: `internal/tuning/intel/msr/device_test.go`
- Create: `internal/tuning/intel/msr/model.go`
- Create: `internal/tuning/intel/msr/model_test.go`
- Create: `internal/tuning/testkit/msr.go`

**Interfaces:**
- Consumes: `intel.Identity`, root-only `/dev/cpu/<n>/msr`
- Produces: `msr.Device`, `LinuxDevice`, `Model`, `LookupModel`, `ReadModifyWrite`

- [ ] **Step 1: Write allow-list and reserved-bit tests**

```go
func TestLookupModelAllowsOnlyC6Stepping2(t *testing.T) {
	if _, ok := LookupModel(intel.Identity{Vendor: "GenuineIntel", Family: 6, Model: 0xc6, Stepping: 2}); !ok { t.Fatal("target rejected") }
	for _, id := range []intel.Identity{{Vendor:"AuthenticAMD", Family:6, Model:0xc6, Stepping:2}, {Vendor:"GenuineIntel", Family:6, Model:0xb7, Stepping:2}} {
		if _, ok := LookupModel(id); ok { t.Fatalf("unexpected model accepted: %+v", id) }
	}
}

func TestReadModifyWritePreservesBitsOutsideMask(t *testing.T) {
	device := testkit.NewMSR(map[testkit.MSRKey]uint64{{CPU:0, Register:0x1a2}: 0xa5a5a5a5a5a5a5a5})
	got, err := ReadModifyWrite(device, 0, 0x1a2, uint64(0x7f)<<24, uint64(10)<<24)
	if err != nil { t.Fatal(err) }
	if got & ^(uint64(0x7f)<<24) != 0xa5a5a5a5a5a5a5a5 & ^(uint64(0x7f)<<24) { t.Fatal("reserved bits changed") }
}
```

- [ ] **Step 2: Define the model table without a generic write escape hatch**

```go
type Device interface { Read(cpu int, register uint32) (uint64, error); Write(cpu int, register uint32, value uint64) error }
type Model struct { Identity intel.Identity; PCoreRatio *RatioLayout; ECoreRatio *RatioLayout; Voltage *VoltageLayout }

const (
	RegisterOCMailbox uint32 = 0x150
	RegisterTemperatureTarget uint32 = 0x1a2
	RegisterTurboRatioLimit uint32 = 0x1ad
)
```

Only package-internal code receives register constants. The exported driver methods remain semantic. The initial table contains GenuineIntel family 6/model `0xC6` stepping 2; `ECoreRatio` remains nil until a documented register layout passes the target-machine read-only probe.

- [ ] **Step 3: Implement Linux MSR I/O and exact error classification**

Use `os.OpenFile` plus `ReadAt`/`WriteAt` with an eight-byte little-endian buffer. Open only `/dev/cpu/<validated decimal CPU>/msr`. Classify missing module/device, permission denied, kernel lockdown, general-protection (`EIO`), and read-back mismatch into stable reason codes.

- [ ] **Step 4: Add fake-device error injection**

`testkit.MSRDevice` stores `(CPU, register) -> value`, records ordered reads/writes, and can reject or clamp the Nth write. Tests prove `ReadModifyWrite` performs read, masked write, then read-back, and reports a clamped value without altering reserved bits.

- [ ] **Step 5: Verify and commit**

```bash
gofmt -w internal/tuning/intel/msr/*.go internal/tuning/testkit/*.go
go test ./internal/tuning/intel/msr
git add internal/tuning/intel/msr internal/tuning/testkit
git commit -m "feat: add allow-listed Intel MSR access"
```

### Task 6: Implement conservative ratio and voltage operations for model 0xC6

**Files:**
- Create: `internal/tuning/intel/msr/ratio.go`
- Create: `internal/tuning/intel/msr/ratio_test.go`
- Create: `internal/tuning/intel/msr/voltage.go`
- Create: `internal/tuning/intel/msr/voltage_test.go`
- Create: `internal/tuning/intel/msr/driver.go`
- Create: `internal/tuning/intel/msr/driver_test.go`

**Interfaces:**
- Consumes: model table and `msr.Device`
- Produces: privileged `intel.msr` driver for P-core ratios and negative core/cache offsets

- [ ] **Step 1: Write turbo-ratio codec tests**

```go
func TestDecodeTurboRatiosUsesOneBytePerActiveCoreCount(t *testing.T) {
	got := DecodeTurboRatios(0x2f30313233343536, 8)
	want := []float64{54, 53, 52, 51, 50, 49, 48, 47}
	if !reflect.DeepEqual(got, want) { t.Fatalf("got %v want %v", got, want) }
}

func TestEncodeTurboRatiosRejectsIncreaseAndNonMonotonicVector(t *testing.T) {
	current := []float64{54,53,52,52,51,51,50,50}
	if _, err := EncodeTurboRatios(current, []float64{55,53,52,52,51,51,50,50}); err == nil { t.Fatal("increase accepted") }
	if _, err := EncodeTurboRatios(current, []float64{54,53,52,53,51,51,50,50}); err == nil { t.Fatal("non-monotonic vector accepted") }
}
```

- [ ] **Step 2: Implement P-core ratio read/lower/restore**

Use `MSR_TURBO_RATIO_LIMIT` (`0x1AD`) with eight little-endian ratio bytes for active-core counts 1–8. Probe succeeds only when every ratio is nonzero, nonincreasing, and consistent with the detected P-core count and policy maxima. Apply supports lowering only, preserves the full captured `uint64` for restore, writes package representative CPU 0, and requires exact read-back. E-core ratio remains visible as `StateReadOnly` with reason `intel.msr.ecore_layout_unverified` on this model.

- [ ] **Step 3: Write voltage mailbox golden-vector tests**

```go
func TestVoltageOffsetRoundTrip(t *testing.T) {
	for _, mv := range []float64{0, -10, -50, -125} {
		encoded, normalized, err := EncodeVoltageOffset(mv)
		if err != nil { t.Fatal(err) }
		if math.Abs(DecodeVoltageOffset(encoded)-normalized) > 0.001 { t.Fatalf("round trip %v", mv) }
	}
}

func TestMailboxCommandsAreFixedByPlane(t *testing.T) {
	if got := PackVoltageRead(PlaneCore); got != 0x8000001000000000 { t.Fatalf("read = %#x", got) }
	if PackVoltageWrite(PlaneCore, 0) != 0x8000001100000000 { t.Fatal("unexpected write command") }
}
```

- [ ] **Step 4: Port and constrain the existing OC mailbox codec**

Port the proven upstream encoding from root `main.go`: round mV by `mV * 1.024`, encode the signed 11-bit quantity into bits 31:21, and use fixed mailbox command bits 63, 36, and 32. The model table exposes only the reviewed core and cache plane IDs. Positive offsets, values below −250 mV, unknown planes, and any command not produced by the codec are rejected. Source comments link the Intel MSR manual for documented registers and identify the upstream GPL implementation for the undocumented `0x150` mailbox codec; mailbox capabilities set `Experimental: true` until target-machine validation succeeds.

- [ ] **Step 5: Implement probe, stepped apply, and restore**

For each plane, issue the fixed read command, read back `0x150`, and require a valid response. A requested offset is split into representable steps no larger than 10 mV. Each step performs mailbox write, mailbox read, exact normalized comparison, and a cancellable 250 ms health interval. A nonzero request that reads back as zero maps to `StateFirmwareLocked`. Restore steps toward the captured value before any other control rollback.

- [ ] **Step 6: Add model-gate and clamping tests**

Tests prove an unknown model calls neither `Device.Write` nor the mailbox codec; a locked mailbox remains visible but disabled; failure on voltage step 3 restores the captured offset; and ratio read-back mismatch returns an error suitable for transaction rollback.

- [ ] **Step 7: Verify and commit**

```bash
gofmt -w internal/tuning/intel/msr/*.go
go test ./internal/tuning/intel/msr
git add internal/tuning/intel/msr
git commit -m "feat: add conservative Intel ratio and voltage controls"
```

### Task 7: Aggregate asynchronous capability discovery

**Files:**
- Create: `internal/tuning/discovery.go`
- Create: `internal/tuning/discovery_test.go`

**Interfaces:**
- Consumes: ordered `[]tuning.Driver`, machine identity
- Produces: `Discoverer.Discover(ctx) <-chan DiscoveryResult`, stable capability generations

```go
type DiscoveryResult struct { Set CapabilitySet; Complete bool; Err error }
type Discoverer struct { MachineID string; Drivers []Driver; DriverTimeout time.Duration }
func NewDiscoverer(machineID string, drivers ...Driver) *Discoverer
func (d *Discoverer) Discover(context.Context) <-chan DiscoveryResult
```

- [ ] **Step 1: Write partial-result and cancellation tests**

```go
func TestDiscoveryPublishesFastDriverBeforeSlowDriver(t *testing.T) {
	fast := testkit.Driver("powercap", 0, []Capability{{ID:ControlPL1}})
	slow := testkit.Driver("msr", time.Second, []Capability{{ID:ControlRatioPCore}})
	results := NewDiscoverer("machine", fast, slow).Discover(context.Background())
	first := <-results
	if !hasCapability(first.Set, ControlPL1) || hasCapability(first.Set, ControlRatioPCore) { t.Fatalf("first = %+v", first) }
}

func TestDiscoveryStopsAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background()); cancel()
	for range NewDiscoverer("machine", testkit.BlockingDriver()).Discover(ctx) { t.Fatal("published after cancel") }
}
```

- [ ] **Step 2: Implement concurrent probe with immutable generations**

Each driver gets its own goroutine and timeout. Merge successful capabilities by semantic ID and source priority: kernel powercap/policy/TCC first, model MSR fallback only when no supported kernel control exists. Hash machine ID, sorted capability IDs, state, bounds, source, and current value to create the generation. Publish copies and close the result channel on completion or cancellation.

- [ ] **Step 3: Test late-result suppression and unknown models**

Add tests that a cancelled generation cannot overwrite its successor and that model `0xB7` returns kernel capabilities plus `StateUnknownModel` advanced controls with zero MSR writes.

- [ ] **Step 4: Verify and commit**

```bash
gofmt -w internal/tuning/discovery.go internal/tuning/discovery_test.go
go test -race ./internal/tuning -run 'TestDiscovery'
git add internal/tuning/discovery.go internal/tuning/discovery_test.go
git commit -m "feat: discover tuning capabilities asynchronously"
```

### Task 8: Define the bounded helper protocol

**Files:**
- Create: `internal/privilege/protocol/message.go`
- Create: `internal/privilege/protocol/codec.go`
- Create: `internal/privilege/protocol/codec_test.go`

**Interfaces:**
- Consumes: `io.Reader`, `io.Writer`, semantic tuning messages
- Produces: four-byte big-endian length-prefixed JSON frames, `protocol.Reader`, `protocol.Writer`

- [ ] **Step 1: Write round-trip and rejection tests**

```go
func TestCodecRoundTrip(t *testing.T) {
	var wire bytes.Buffer
	w := NewWriter(&wire); r := NewReader(&wire)
	want := Message{Version: 1, RequestID: "r1", Type: TypeHello, Payload: json.RawMessage(`{"client":"dev"}`)}
	if err := w.Write(want); err != nil { t.Fatal(err) }
	got, err := r.Read(); if err != nil { t.Fatal(err) }
	if !reflect.DeepEqual(got, want) { t.Fatalf("got %+v", got) }
}

func TestCodecRejectsOversizedFrameBeforeAllocation(t *testing.T) {
	wire := bytes.NewBuffer([]byte{0, 16, 0, 1})
	if _, err := NewReader(wire).Read(); !errors.Is(err, ErrFrameTooLarge) { t.Fatalf("err=%v", err) }
}
```

- [ ] **Step 2: Define the allow-listed message vocabulary**

```go
const Version = 1
const MaxFrameSize = 1 << 20
const (
	TypeHello Type = "hello"; TypeProbe Type = "probe_privileged"; TypeBegin Type = "begin_transaction"
	TypeRenew Type = "renew_lease"; TypeRevert Type = "revert"; TypeClose Type = "close_session"
	TypeCapabilities Type = "capabilities"; TypeReviewChanged Type = "review_changed"
	TypeProgress Type = "transaction_progress"; TypeApplied Type = "transaction_applied"
	TypeFailed Type = "transaction_failed"; TypeRollbackComplete Type = "rollback_complete"
	TypeRollbackIncomplete Type = "rollback_incomplete"
)
type Message struct { Version int `json:"version"`; RequestID string `json:"request_id"`; Type Type `json:"type"`; Payload json.RawMessage `json:"payload,omitempty"` }
```

Payload structs contain `tuning.CapabilitySet`, `ChangeSet`, `Event`, or verified values only. No payload type contains path, register, mask, command, or process arguments.

- [ ] **Step 3: Implement strict framing and payload validation**

Read exactly four length bytes, reject zero or greater than 1 MiB, allocate once, then `json.Unmarshal` with unknown fields rejected for each typed payload. Reject version mismatch, empty request IDs, unknown message types, and messages illegal for the current direction.

- [ ] **Step 4: Fuzz and verify**

```go
func FuzzReader(f *testing.F) {
	f.Add([]byte{0, 0, 0, 2, '{', '}'})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = NewReader(bytes.NewReader(data)).Read()
	})
}
```

```bash
gofmt -w internal/privilege/protocol/*.go
go test ./internal/privilege/protocol
go test ./internal/privilege/protocol -run=^$ -fuzz=FuzzReader -fuzztime=10s
git add internal/privilege/protocol
git commit -m "feat: define bounded tuning helper protocol"
```

### Task 9: Implement recovery records and failure-atomic transactions

**Files:**
- Create: `internal/tuning/recovery.go`
- Create: `internal/tuning/recovery_test.go`
- Create: `internal/tuning/transaction.go`
- Create: `internal/tuning/transaction_test.go`

**Interfaces:**
- Consumes: `Driver`, `PreparedOperation`, validated `ChangeSet`
- Produces: `RecoveryRecord`, `RecoveryStore`, `Engine.Apply`, `ActiveTransaction.Rollback`

```go
type Engine struct { Capabilities CapabilitySet; Drivers map[string]Driver; Recovery RecoveryStore; BootID string }
func NewEngine(capabilities CapabilitySet, recovery RecoveryStore, drivers ...Driver) *Engine
func (e *Engine) Apply(context.Context, ChangeSet) (*ActiveTransaction, ValidationResult, error)
func (e *Engine) Recover(context.Context) (RecoveryResult, error)
func (a *ActiveTransaction) Rollback(context.Context) error
```

- [ ] **Step 1: Write recovery-store permission and atomicity tests**

```go
func TestRecoveryStoreWrites0600AndRoundTrips(t *testing.T) {
	store := FileRecoveryStore{Directory: t.TempDir()}
	want := RecoveryRecord{Protocol:1, MachineID:"cpu", BootID:"boot", State:"applying"}
	if err := store.Save(want); err != nil { t.Fatal(err) }
	info, _ := os.Stat(store.Path()); if info.Mode().Perm() != 0o600 { t.Fatalf("mode=%o", info.Mode().Perm()) }
	got, err := store.Load(); if err != nil || got.MachineID != want.MachineID { t.Fatalf("got=%+v err=%v", got, err) }
}
```

Use a filesystem spy to prove Save writes a same-directory temporary file, syncs it, renames it, and syncs the directory before success.

- [ ] **Step 2: Define durable recovery entries**

```go
type RecoveryEntry struct { DriverID string `json:"driver_id"`; ControlID ControlID `json:"control_id"`; Snapshot json.RawMessage `json:"snapshot"`; Applied bool `json:"applied"` }
type RecoveryRecord struct { Protocol int; MachineID, BootID, TransactionID, State string; CreatedAt time.Time; Entries []RecoveryEntry }
type RecoveryStore interface { Load() (RecoveryRecord,error); Save(RecoveryRecord) error; Remove() error }
```

The production directory is `/run/undervolt-go-studio`, created root-owned `0700`; the record is `recovery-v1.json` mode `0600`.

- [ ] **Step 3: Write transaction order and failure-injection tests**

```go
func TestEngineAppliesSafeOrderAndRollsBackReverseOrder(t *testing.T) {
	log := []string{}
	drivers := testkit.OrderedDrivers(&log, map[ControlID]int{ControlPL1:10, ControlThermalLimit:20, ControlRatioPCore:30, ControlVoltageCore:40})
	active, _, err := NewEngine(capabilities, store, drivers...).Apply(context.Background(), approvedChanges())
	if err != nil { t.Fatal(err) }
	if err := active.Rollback(context.Background()); err != nil { t.Fatal(err) }
	want := []string{"apply:pl1","apply:thermal","apply:ratio","apply:voltage","restore:voltage","restore:ratio","restore:thermal","restore:pl1"}
	if !reflect.DeepEqual(log, want) { t.Fatalf("log=%v", log) }
}
```

For every operation index, inject capture, Save, Apply, and read-back errors; assert already applied entries restore and the recovery record accurately lists any unrestored entry.

- [ ] **Step 4: Implement prepare/capture/apply/rollback**

Resolve each change by its capability `DriverID`, call `Prepare`, sort operations stably by `Order`, capture all snapshots before any write, persist the record, then apply one operation at a time and mark each entry applied after verified success. On error, restore applied entries in reverse order. Remove the record only after every restore verifies.

- [ ] **Step 5: Implement stale-record reconciliation**

`Engine.Recover` compares protocol, machine ID, and boot ID. Same-boot records restore through the named driver. Earlier-boot records do not replay volatile MSR snapshots; they audit kernel-backed controls and return explicit discrepancies for the UI.

- [ ] **Step 6: Verify and commit**

```bash
gofmt -w internal/tuning/recovery.go internal/tuning/recovery_test.go internal/tuning/transaction.go internal/tuning/transaction_test.go
go test -race ./internal/tuning
git add internal/tuning/recovery.go internal/tuning/recovery_test.go internal/tuning/transaction.go internal/tuning/transaction_test.go
git commit -m "feat: add transactional tuning recovery"
```

### Task 10: Build the privileged session helper and fixed pkexec client

**Files:**
- Create: `internal/privilege/session/server.go`
- Create: `internal/privilege/session/server_test.go`
- Create: `internal/privilege/client/client.go`
- Create: `internal/privilege/client/launcher.go`
- Create: `internal/privilege/client/client_test.go`
- Create: `cmd/helper/main.go`

**Interfaces:**
- Consumes: protocol reader/writer, tuning discoverer/engine, fixed helper path
- Produces: `session.Server.Run`, `client.Client`, `client.PKExecLauncher`, helper binary

- [ ] **Step 1: Write helper state-machine and lease tests**

```go
func TestServerRollsBackOnEOFAndLeaseExpiry(t *testing.T) {
	for _, cause := range []string{"eof", "lease"} {
		t.Run(cause, func(t *testing.T) {
			engine := testkit.NewAppliedEngine()
			server := NewServer(engine, WithLease(100*time.Millisecond))
			runServerScenario(t, server, cause)
			if engine.RollbackCalls() != 1 { t.Fatalf("rollback calls=%d", engine.RollbackCalls()) }
		})
	}
}
```

Add tests for invalid transition, duplicate Begin, stale generation producing `review_changed`, signal cancellation, rollback-incomplete response, and recovery-before-probe.

- [ ] **Step 2: Implement the helper session state machine**

`Run(ctx, reader, writer)` performs Hello, reconciliation, privileged Probe, Begin, Renew, Revert, and Close transitions. Start a ten-second timer only after successful Apply. Reset it only for a valid renewal belonging to the active transaction. EOF, context cancellation, invalid protocol after Apply, or timer expiry calls rollback before exit.

- [ ] **Step 3: Write exact launcher-argument tests**

```go
func TestPKExecLauncherUsesFixedCommandWithoutShell(t *testing.T) {
	launcher := PKExecLauncher{CommandFactory: recordingFactory}
	_, _ = launcher.Start(context.Background())
	want := []string{"pkexec", "/usr/libexec/undervolt-go-studio-helper", "--session", "--protocol=1"}
	if !reflect.DeepEqual(recordedArgs, want) { t.Fatalf("args=%q", recordedArgs) }
}
```

The production launcher has no public field for user-supplied path or extra arguments. Tests also assert cancellation of the PolicyKit prompt returns `ErrAuthorizationCancelled` and starts no session.

- [ ] **Step 4: Implement client handshake, request correlation, and close**

The client starts `exec.Command("pkexec", fixedHelper, "--session", "--protocol=1")`, obtains pipes before Start, verifies protocol/build identity, serializes writes under a mutex, correlates responses by request ID, and runs a reader goroutine. After Apply it renews every three seconds. `Close` sends `close_session`, waits for rollback-complete with a three-second deadline, then closes stdin and waits for the process.

- [ ] **Step 5: Implement `cmd/helper` with fixed flags**

Accept only the exact `--session` boolean and `--protocol=1`. Any positional argument, unknown flag, other protocol, non-root EUID, or TTY interactive mode exits before probing hardware. Wire root-confined sysfs, Intel identity, powercap/EPP/TCC/MSR drivers, recovery store, transaction engine, and session server.

- [ ] **Step 6: Verify process behavior and commit**

```bash
gofmt -w internal/privilege/session/*.go internal/privilege/client/*.go cmd/helper/*.go
go test -race ./internal/privilege/...
go build -o build/undervolt-go-studio-helper ./cmd/helper
go test ./...
git add internal/privilege cmd/helper
git commit -m "feat: add temporary privileged tuning session"
```

Expected: no test launches a real `pkexec` prompt or writes real hardware.

### Task 11: Add a bounded semantic event log

**Files:**
- Create: `internal/events/store.go`
- Create: `internal/events/store_test.go`
- Create: `internal/ui/pages/logs.go`
- Create: `internal/ui/pages/logs_test.go`

**Interfaces:**
- Consumes: `tuning.Event`
- Produces: `events.Store.Append`, `Snapshot`, `Subscribe`; concrete lazy Logs page

- [ ] **Step 1: Write bounded-store and redaction tests**

```go
func TestStoreKeepsNewestEventsWithinCapacity(t *testing.T) {
	store := NewStore(2)
	store.Append(event("one")); store.Append(event("two")); store.Append(event("three"))
	got := store.Snapshot()
	if len(got) != 2 || got[0].Message != "two" || got[1].Message != "three" { t.Fatalf("got=%v", got) }
}

func TestUserMessageDoesNotContainRawRegister(t *testing.T) {
	e := FromError(tuning.ControlPL1, errors.New("write msr 0x610: EIO"))
	if strings.Contains(e.Message, "0x610") || !strings.Contains(e.Detail, "0x610") { t.Fatalf("event=%+v", e) }
}
```

- [ ] **Step 2: Implement latest-value subscription and event mapping**

Use a mutex-protected ring with default capacity 2,000. `Append` copies the event and notifies bounded subscribers without blocking. Map stable reason codes to semantic messages such as “BIOS rejected Core voltage offset” and keep raw paths/registers/errno only in `Detail`.

- [ ] **Step 3: Build the Logs page**

Render time, severity, and human message first. Selecting a row reveals technical detail. Activation subscribes; deactivation closes the subscription. Use `components.LatestDispatcher` for UI updates.

- [ ] **Step 4: Verify and commit**

```bash
gofmt -w internal/events/*.go internal/ui/pages/logs.go internal/ui/pages/logs_test.go
go test ./internal/events ./internal/ui/pages
git add internal/events internal/ui/pages/logs.go internal/ui/pages/logs_test.go
git commit -m "feat: add tuning event log"
```

### Task 12: Implement the asynchronous Tune view model

**Files:**
- Create: `internal/ui/viewmodel/tune.go`
- Create: `internal/ui/viewmodel/tune_test.go`

**Interfaces:**
- Consumes: `TuneService`, telemetry subscriptions, capabilities and events
- Produces: `TuneState`, `Tune.Activate`, `Deactivate`, `Stage`, `Reset`, `Review`, `Apply`, `Revert`, `Close`

- [ ] **Step 1: Define the GUI-neutral service and state**

```go
type TuneService interface {
	Discover(context.Context) <-chan tuning.DiscoveryResult
	Apply(context.Context, tuning.ChangeSet) (<-chan tuning.Event, error)
	Revert(context.Context) error
	Close(context.Context) error
}

type TuneState struct {
	Active bool
	Phase string
	Capabilities tuning.CapabilitySet
	Pending []tuning.Change
	Review []ReviewRow
	Effective map[tuning.ControlID]tuning.Value
	LastError string
}
type ReviewRow struct { ID tuning.ControlID; Label string; Stock, Requested, Normalized tuning.Value; Adjusted bool }
```

- [ ] **Step 2: Write lazy discovery and stale-result tests**

```go
func TestTuneIgnoresDiscoveryFromPreviousActivation(t *testing.T) {
	service := newControlledTuneService()
	vm := NewTune(service)
	vm.Activate(); first := service.LastRequest(); vm.Deactivate(); vm.Activate()
	first.Publish(capabilitySet("old"))
	if vm.State().Capabilities.Generation == "old" { t.Fatal("stale discovery replaced state") }
}
```

Assert construction starts no I/O, Activate is idempotent, Deactivate cancels only discovery/header subscriptions, and an active applied session survives navigation until Revert or Close.

- [ ] **Step 3: Write staging/review/reconfirmation tests**

Test that slider edits change only Pending, Reset writes nothing, Apply is disabled for an invalid stage, normalized values appear in Review, and a helper `review_changed` event returns to review instead of applying silently.

- [ ] **Step 4: Implement state transitions without UI-thread assumptions**

Protect state with a mutex, publish immutable copies to `StateListener[TuneState]`, use activation generation tokens, and perform service calls in cancellable goroutines. Phases are exactly `idle`, `discovering`, `staged`, `reviewing`, `authorizing`, `applying`, `active`, `rolling_back`, `failed`, and `rollback_incomplete`.

- [ ] **Step 5: Test close-time rollback and errors**

`Close` uses a bounded context, calls service Close exactly once, and retains verified remaining values on rollback failure. Cancellation of `pkexec` returns to `staged` with “Authorization cancelled; nothing was changed.”

- [ ] **Step 6: Verify and commit**

```bash
gofmt -w internal/ui/viewmodel/tune.go internal/ui/viewmodel/tune_test.go
go test -race ./internal/ui/viewmodel -run 'TestTune'
git add internal/ui/viewmodel/tune.go internal/ui/viewmodel/tune_test.go
git commit -m "feat: add asynchronous Tune view model"
```

### Task 13: Build the XTU Workbench page and wire lifecycle

**Files:**
- Create: `internal/ui/components/tune_control.go`
- Create: `internal/ui/components/tune_control_test.go`
- Create: `internal/ui/components/pending.go`
- Create: `internal/ui/components/pending_test.go`
- Create: `internal/ui/pages/tune.go`
- Create: `internal/ui/pages/tune_test.go`
- Modify: `internal/ui/shell.go`
- Modify: `internal/ui/shell_test.go`
- Modify: `internal/ui/desktop.go`
- Modify: `internal/ui/desktop_test.go`

**Interfaces:**
- Consumes: `viewmodel.Tune`, event store, existing telemetry scheduler
- Produces: concrete lazy Tune page matching layout A; guaranteed desktop close-time rollback

- [ ] **Step 1: Write capability-card tests**

```go
func TestTuneControlExplainsLockedCapability(t *testing.T) {
	control := NewTuneControl(tuning.Capability{ID:tuning.ControlVoltageCore, Label:"Core voltage offset", State:tuning.StateFirmwareLocked, Reason:"BIOS / undervolt protection blocks writes"}, nil)
	if control.Enabled() { t.Fatal("locked control enabled") }
	if !strings.Contains(control.StatusText(), "BIOS") { t.Fatalf("status=%q", control.StatusText()) }
}
```

Test supported numeric, choice, ratio-vector, loading, read-only, firmware-locked, kernel-blocked, requires-probe, unavailable, and stale states.

- [ ] **Step 2: Implement focused controls and pending rail**

`TuneControl` renders label, current value, range/step, status, and the control-type-specific Fyne slider, select, or ratio-vector editor. It invokes only `onStage(ControlID, Value)`. `PendingRail` displays stock → requested rows, Reset, Review & Apply, and Revert; it never owns service calls.

- [ ] **Step 3: Write page layout and UI-dispatch tests**

```go
func TestTunePageUsesWorkbenchGroupsAndPendingRail(t *testing.T) {
	page := NewTune(fakeTuneVM(), fakeMetrics())
	want := []string{"Power limits", "Thermal & voltage", "Core ratios", "Pending changes"}
	for _, label := range want { if !page.HasSection(label) { t.Fatalf("missing %q", label) } }
}
```

Use a fake dispatcher to prove background view-model notifications enqueue one latest UI update and never mutate widgets directly.

- [ ] **Step 4: Implement layout A and review dialog**

Build a header with Package °C, Package W, and P-core max; central scrollable grouped controls; and a fixed-width right pending rail. Review rows show Stock, Requested, and Normalized/Effective columns. The confirmation button text is `Authenticate & apply`; the dialog states that closing the app, losing the helper, or lease expiry restores stock values.

- [ ] **Step 5: Replace Tune and Logs placeholders lazily**

In `NewShell`, inject tuning service and event store, replace `placeholderFactory("tune", ...)` with `pages.NewTune`, and replace Logs with `pages.NewLogs`. Remove obsolete shell-global Apply/Revert buttons and update `TestShellKeepsTuningActionsDisabledInReadOnlyMilestone` into tests asserting Tune remains unconstructed until selected and creates no helper before confirmation.

- [ ] **Step 6: Wire desktop shutdown to rollback**

`Desktop.Close` first calls the Tune service Close with a three-second timeout, then deactivates pages and cancels telemetry. Preserve idempotence. Test two concurrent Close calls produce one helper Close and one scheduler cancellation.

- [ ] **Step 7: Verify and commit**

```bash
gofmt -w internal/ui/*.go internal/ui/components/*.go internal/ui/pages/*.go internal/ui/viewmodel/*.go
go test -race ./internal/ui/...
go test ./...
git add internal/ui
git commit -m "feat: add XTU Tune workbench"
```

### Task 14: Package PolicyKit metadata and add the opt-in 275HX harness

**Files:**
- Create: `packaging/polkit/io.github.notemaster11.undervolt-go-studio.policy`
- Create: `packaging/polkit/policy_test.go`
- Create: `cmd/tuning-smoke/main.go`
- Create: `cmd/tuning-smoke/main_test.go`
- Create: `docs/validation/intel-275hx-runbook.md`

**Interfaces:**
- Consumes: installed helper path, real capability discovery, explicit confirmation phrase
- Produces: graphical authorization metadata and read-only/mutating hardware-validation modes

- [ ] **Step 1: Add a machine-readable PolicyKit policy**

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE policyconfig PUBLIC "-//freedesktop//DTD PolicyKit Policy Configuration 1.0//EN" "http://www.freedesktop.org/standards/PolicyKit/1/policyconfig.dtd">
<policyconfig>
  <action id="io.github.notemaster11.undervolt-go-studio.tune">
    <description>Apply temporary CPU tuning settings</description>
    <message>Authentication is required to apply temporary CPU tuning settings</message>
    <defaults><allow_any>no</allow_any><allow_inactive>no</allow_inactive><allow_active>auth_admin_keep</allow_active></defaults>
    <annotate key="org.freedesktop.policykit.exec.path">/usr/libexec/undervolt-go-studio-helper</annotate>
    <annotate key="org.freedesktop.policykit.exec.allow_gui">false</annotate>
  </action>
</policyconfig>
```

The Go test parses XML, checks the action ID, exact exec path, active authorization rule, and absence of shell/interpreter paths.

- [ ] **Step 2: Write smoke-harness safety-gate tests**

```go
func TestMutatingSmokeRequiresExactConfirmation(t *testing.T) {
	for _, confirmation := range []string{"", "yes", "I understand"} {
		if err := validateMutationGate(true, confirmation, intel.Identity{Family:6, Model:0xc6, Stepping:2}); !errors.Is(err, ErrConfirmationRequired) { t.Fatalf("confirmation=%q err=%v", confirmation, err) }
	}
}
```

The exact phrase is `I UNDERSTAND TEMPORARY CPU TUNING`. Mutation also requires root EUID, family 6/model `0xC6` stepping 2, a saved output report path, and successful read-only probe in the same process.

- [ ] **Step 3: Implement read-only and conservative mutation modes**

Default mode prints JSON identity/capabilities and performs no writes. `--mutate` runs sequential snapshot/apply/read-back/restore cases: PL1/PL2, TCC, EPP, one-step P-core ratio if supported, then −10 mV core/cache offset if supported. Each case uses `defer` recovery, stops after the first failure, and writes stock/requested/effective/restored plus helper events to the report.

- [ ] **Step 4: Document exact local commands without normalizing root GUI use**

```bash
go run ./cmd/tuning-smoke --output build/275hx-read-only.json
go build -o build/undervolt-go-studio-helper ./cmd/helper
go build -o build/tuning-smoke ./cmd/tuning-smoke
pkexec build/tuning-smoke --mutate --confirm 'I UNDERSTAND TEMPORARY CPU TUNING' --output build/275hx-mutation.json
```

Write these commands and the safety gates to `docs/validation/intel-275hx-runbook.md`. State that end users use the GUI; `tuning-smoke` is an explicit developer validation harness.

- [ ] **Step 5: Verify and commit**

```bash
gofmt -w cmd/tuning-smoke/*.go packaging/polkit/*.go
go test ./cmd/tuning-smoke ./packaging/polkit
go test ./...
git add cmd/tuning-smoke packaging/polkit docs/validation/intel-275hx-runbook.md
git commit -m "test: add guarded Intel hardware validation"
```

Do not run mutating mode in automated CI.

### Task 15: Perform full verification and headless UI review

**Files:**
- Modify: `cmd/ui-snapshot/main.go`
- Create: `docs/validation/intel-275hx-read-only.md`

**Interfaces:**
- Consumes: completed Tune page, fake capability scenario, read-only host probe
- Produces: reproducible verification output and a reviewed Tune screenshot without touching the user's active windows

- [ ] **Step 1: Add a deterministic Tune snapshot scenario**

Extend `ui-snapshot` so `--page tune --scenario intel-275hx` injects PL1 44 W, PL2 44 W, 95 °C thermal ceiling, EPP choices, a supported P-core ratio vector, and firmware-locked voltage. It must not create the pkexec client.

- [ ] **Step 2: Run all automated verification**

```bash
go test ./...
go test -race ./internal/tuning/... ./internal/privilege/... ./internal/ui/...
go vet ./...
go build ./...
go build -o build/undervolt-go-studio ./cmd/studio
go build -o build/undervolt-go-studio-helper ./cmd/helper
go build -o build/tuning-smoke ./cmd/tuning-smoke
```

Expected: every command exits 0; no root prompt occurs.

- [ ] **Step 3: Run the read-only target probe**

```bash
build/tuning-smoke --output build/275hx-read-only.json
```

Expected: identity is GenuineIntel family 6/model `0xC6` stepping 2; powercap, EPP, and TCC capabilities are present; MSR-only controls say authorization is required rather than fabricating values.

- [ ] **Step 4: Render and inspect Tune in the background**

```bash
go run ./cmd/ui-snapshot --page tune --scenario intel-275hx --output build/tune-275hx.png --width 1600 --height 1000
```

Inspect `build/tune-275hx.png` with the image viewer tool. Verify no clipped labels, overlapping cards, unreadable disabled states, missing Pending Changes rail, thin/aliased charts, or raw technical data in the primary view. Iterate with failing UI tests before changing code.

- [ ] **Step 5: Record non-mutating evidence**

Create `docs/validation/intel-275hx-read-only.md` containing the commit, kernel, CPUID, discovered capabilities, test commands, pass/fail output, screenshot path, and explicit statement that no hardware writes were performed.

- [ ] **Step 6: Commit the verified milestone**

```bash
git add cmd/ui-snapshot/main.go docs/validation/intel-275hx-read-only.md
git commit -m "docs: record Intel tuning verification"
git status --short
```

Expected: clean worktree.

- [ ] **Step 7: Request code and safety review before real mutation**

Invoke `superpowers:requesting-code-review` for `origin/main...feature/intel-tuning-backend`. Resolve correctness, privilege-boundary, rollback, protocol, and UI findings. Repeat automated verification after fixes. Only then run the guarded mutating 275HX harness through its graphical `pkexec` prompt and confirm the final report shows every changed value restored.

### Task 16: Replace the upstream README with current Studio documentation

**Files:**
- Modify: `README.md`
- Create: `internal/product/readme_test.go`
- Create: `docs/images/studio-overview.png`
- Create: `docs/images/studio-monitor.png`
- Create: `docs/images/studio-hardware.png`
- Create: `docs/images/studio-tune.png`

**Interfaces:**
- Consumes: verified application state and deterministic headless snapshot scenarios
- Produces: a Studio-owned README with current screenshots, accurate feature status, and one retained upstream attribution

- [ ] **Step 1: Write a failing README contract test**

```go
func TestReadmeDescribesCurrentStudioOnly(t *testing.T) {
	data, err := os.ReadFile("../../README.md")
	if err != nil { t.Fatal(err) }
	text := string(data)
	for _, required := range []string{
		"# Undervolt Go Studio",
		"Forked from [Softorage/undervolt-go](https://github.com/Softorage/undervolt-go)",
		"docs/images/studio-overview.png",
		"docs/images/studio-monitor.png",
		"docs/images/studio-hardware.png",
		"docs/images/studio-tune.png",
		"Temporary tuning",
		"Intel Core Ultra 9 275HX",
	} {
		if !strings.Contains(text, required) { t.Errorf("README missing %q", required) }
	}
	for _, obsolete := range []string{"softorage.github.io/undervolt-go", "UndervoltGo.png", "Persist allows"} {
		if strings.Contains(text, obsolete) { t.Errorf("README retains obsolete content %q", obsolete) }
	}
}
```

- [ ] **Step 2: Run the contract test and confirm it fails**

Run: `go test ./internal/product -run TestReadmeDescribesCurrentStudioOnly`

Expected: FAIL against the old upstream README.

- [ ] **Step 3: Generate four project-owned screenshots headlessly**

```bash
mkdir -p docs/images
go run ./cmd/ui-snapshot --page overview --scenario intel-275hx --output docs/images/studio-overview.png --width 1600 --height 1000
go run ./cmd/ui-snapshot --page monitor --scenario intel-275hx --output docs/images/studio-monitor.png --width 1600 --height 1000
go run ./cmd/ui-snapshot --page hardware --scenario intel-275hx --output docs/images/studio-hardware.png --width 1600 --height 1000
go run ./cmd/ui-snapshot --page tune --scenario intel-275hx --output docs/images/studio-tune.png --width 1600 --height 1000
```

These commands use Fyne's in-memory test canvas and must not open, focus, or resize a physical desktop window.

- [ ] **Step 4: Inspect every screenshot before publishing it**

Open each PNG with the image viewer tool. Reject and fix any screenshot with clipping, overlapping controls, unreadable disabled text, meaningless random chart marks, raw diagnostic noise in the primary UI, or a state not produced by the current code. Regenerate after each UI correction.

- [ ] **Step 5: Replace README rather than appending to it**

The new README contains, in this order:

1. product name and one-sentence purpose;
2. current Studio screenshot and a compact feature overview;
3. Overview, Monitor, Hardware, and Tune sections using the four new PNGs;
4. a capability table distinguishing implemented, hardware-dependent, and planned features;
5. temporary-tuning safety behavior and the hard-lock/reboot limitation;
6. supported environment and current 275HX validation status;
7. build/run instructions for the unprivileged GUI and packaged helper;
8. links to the detailed design, validation runbook, and GPL-3.0 license;
9. exactly one provenance paragraph: `Forked from [Softorage/undervolt-go](https://github.com/Softorage/undervolt-go). Original history and authorship are retained.`

Delete old upstream badges, release claims, installation scripts, screenshots, CLI flag documentation, persistence instructions, and obsolete product names from README. Do not claim Stress Tests, Profiles, Reports, GPU telemetry, or packaged releases are complete until their code exists.

- [ ] **Step 6: Verify README, links, and image dimensions**

```bash
gofmt -w internal/product/readme_test.go
go test ./internal/product -run TestReadmeDescribesCurrentStudioOnly
file docs/images/studio-*.png
git diff --check
```

Expected: test passes; all four files are 1600×1000 PNG images; every relative README link resolves locally.

- [ ] **Step 7: Commit the documentation replacement**

```bash
git add README.md internal/product/readme_test.go docs/images/studio-*.png
git commit -m "docs: replace upstream README with Studio overview"
```

## Final acceptance checklist

- [ ] Tune is concrete, lazy, asynchronous, and visually matches layout A.
- [ ] Dragging or typing stages changes only; authorization starts only after review confirmation.
- [ ] The fixed `pkexec` launcher uses no shell and the helper accepts no raw hardware target.
- [ ] PL1, PL2, Tau, EPP, and TCC use verified kernel interfaces on the 275HX.
- [ ] Model-specific P-core ratio and voltage probes are restricted to the reviewed `0xC6` table; unsupported E-core ratios remain explicitly read-only.
- [ ] Positive voltage and ratio increases are impossible through model, protocol, helper, and UI layers.
- [ ] Transaction failure, GUI close, pipe loss, and lease expiry restore the captured snapshot.
- [ ] Incomplete rollback remains visible with verified remaining values and recovery actions.
- [ ] Telemetry and discovery cancellation pass race tests and do not block Fyne.
- [ ] The headless 1600×1000 Tune screenshot is visually reviewed without focusing the user's desktop.
- [ ] The opt-in 275HX mutation report proves stock values were restored before the backend is called hardware-validated.
- [ ] README contains only current Studio documentation, four project-owned screenshots, and the retained upstream attribution.
