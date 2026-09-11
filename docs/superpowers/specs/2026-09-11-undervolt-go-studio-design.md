# Undervolt Go Studio — Design Specification

Date: 2026-09-11

Status: Approved; Intel tuning details refined by `2026-09-11-intel-tuning-backend-design.md`

Working repository name: `undervolt-go-studio`

## 1. Purpose

Undervolt Go Studio is a Linux desktop application for observing, tuning, and stress-testing CPU and GPU power and thermal behavior. It evolves the existing `Softorage/undervolt-go` GPL-3.0 project into an Intel XTU-style workstation without copying Intel branding or presenting itself as an Intel product.

The primary user scenario is a repeatable experiment:

1. Record a stock baseline during a synthetic load or game session.
2. Temporarily lower CPU power limits or change another supported control.
3. Repeat the same workload.
4. Compare sustained performance, temperature, power, clocks, and time spent throttling.
5. Restore the original settings automatically.

The first release targets Linux x86-64 and implements Intel tuning first. The internal interfaces must allow later AMD and additional GPU backends without restructuring the GUI or experiment model.

## 2. Goals

- Provide an XTU-like native desktop workflow using Go and Fyne.
- Display high-rate CPU and GPU telemetry with selectable sampling intervals from 100 ms to 5 s; the default is 250 ms.
- Avoid UI stalls through asynchronous, subscription-driven telemetry and lazy page construction.
- Apply supported tuning controls through a small privileged helper, never by running the whole GUI as root.
- Make tuning changes transactional and temporary by default, with automatic rollback after failure or disconnect.
- Include CPU, FPU, cache, RAM, Vulkan compute, Vulkan graphics, and combined stress tests.
- Ship all application-owned user-space components in offline-ready release artifacts; the application must not download components at runtime.
- Record complete experiment sessions and produce comparable JSON, CSV, and HTML results.
- Degrade gracefully when a sensor, tuning control, GPU provider, or stress engine is unavailable.

## 3. Non-goals for the first release

- AMD-specific voltage or power tuning. The interfaces are included; the implementation follows later.
- Vendor-specific fan control, embedded-controller writes, or a custom kernel module.
- Automatic overclocking or an optimizer that chooses unsafe settings without user input.
- Running the GUI or stress workers as root.
- Replacing GPU drivers or bundling kernel drivers.
- Cloud accounts, telemetry upload, or mandatory online services.
- An in-app updater that downloads or installs a release.

## 4. Supported environment and capability model

The application discovers capabilities at runtime rather than assuming that a CPU family, kernel, firmware, or driver exposes a control. A supported control is visible and editable; a readable but locked control is visible with an explanation; an absent control is omitted from tuning surfaces and listed as unavailable in Hardware diagnostics.

The first tuning backend supports Intel MSR/RAPL controls that the installed kernel and firmware permit, including:

- PL1 and PL2 power limits;
- Turbo Time Window;
- supported thermal limits;
- Linux energy-performance preference or equivalent policy controls;
- ratio controls and voltage offsets only when hardware and firmware explicitly permit them.

Generic Linux providers use `sysfs`, `procfs`, `perf_event_open`, and `hwmon` where possible. Intel-specific telemetry may use RAPL, MSRs, and performance counters. NVIDIA telemetry is provided through a dedicated adapter. Vulkan device enumeration and stress testing work with any conforming installed Vulkan driver.

GPU and telemetry devices use stable identities where available, such as PCI address plus driver UUID, so iGPU and dGPU selections remain consistent across sessions.

## 5. Process architecture

The product consists of three separately supervised processes.

### 5.1 Desktop GUI

The main `undervolt-go-studio` process runs as the logged-in user. It owns:

- Fyne windows and view models;
- hardware discovery that does not require privileges;
- telemetry scheduling, aggregation, chart buffers, and subscriptions;
- profiles and experiment sessions stored in the user's data directory;
- stress-test orchestration;
- report generation;
- communication with the privileged helper over a private, versioned child-process pipe.

The GUI performs no blocking I/O on Fyne's main thread.

### 5.2 Privileged helper

`undervolt-go-studio-helper` is a minimal root session process launched through `pkexec` after the user confirms a reviewed change set. The GUI executes a fixed installed path and fixed argument vector without a shell. The helper communicates over a private, versioned stdin/stdout protocol and exposes only typed, allow-listed operations. It does not accept shell commands, file paths supplied for arbitrary writes, raw scripts, or unrestricted MSR addresses.

Its responsibilities are:

- return its protocol version and supported privileged capabilities;
- read the privileged portion of a tuning snapshot;
- validate a proposed change against backend and hardware bounds;
- atomically apply a validated tuning transaction;
- verify values by reading them back;
- restore an earlier snapshot;
- maintain temporary-profile leases and restore settings when a lease expires;
- reject new mutations until any recoverable interrupted session has been reconciled.

PolicyKit authorization is requested only after a mutating transaction is reviewed and confirmed. The helper remains alive only for the temporary tuning session. Persistent boot-time application is outside the first Intel-tuning milestone.

### 5.3 Stress worker

`undervolt-go-studio-stress` runs unprivileged in its own process group. It hosts the bundled workload adapters and reports progress and metrics over a local authenticated channel. The GUI can stop the entire process group even if an individual workload hangs.

The worker has no access to privileged tuning methods. It receives bounded workload descriptions rather than arbitrary command lines.

## 6. Internal boundaries

The existing `main.go` and `gui.go` monolith is split into packages with narrow interfaces. The package boundaries and names below are the implementation baseline; changing one requires an explicit plan revision that preserves the same separation of responsibilities.

### 6.1 Core interfaces

`TelemetryProvider`

- discovers its devices and metric catalog;
- accepts a set of subscribed metric IDs and a requested cadence;
- emits timestamped batch snapshots;
- reports latency, errors, and freshness independently of other providers;
- stops promptly when its context is cancelled.

`TuningBackend`

- reports supported, locked, and unavailable controls with valid ranges;
- produces a complete pre-change snapshot;
- validates a typed change set;
- applies and verifies a transaction;
- restores a snapshot after failure or lease expiry.

`StressEngine`

- declares supported workloads and adjustable parameters;
- validates a workload description;
- starts, pauses where supported, and stops a workload;
- streams progress, throughput, warnings, and termination reason.

`SessionRecorder`

- records monotonic and wall-clock timestamps;
- aligns telemetry, profile changes, workload phases, and user markers;
- writes incrementally so an interrupted session remains recoverable;
- exports the canonical session to JSON, CSV views, and an HTML report.

`ProfileManager`

- stores named profiles and their hardware compatibility metadata;
- distinguishes staged profiles from temporarily applied profiles;
- supports import, export, comparison, and explicit reset to captured stock values.

### 6.2 Proposed source layout

```text
cmd/
  studio/                 unprivileged desktop entry point
  helper/                 short-lived privileged session entry point
  stress-worker/          isolated workload process
internal/
  app/                    application lifecycle and dependency wiring
  hardware/               discovery, device IDs, capability model
  telemetry/              scheduler, subscriptions, buffers, aggregation
  providers/linux/        sysfs, procfs, hwmon, perf providers
  providers/intel/        RAPL, MSR, hybrid-core telemetry
  providers/nvidia/       optional NVIDIA telemetry adapter
  tuning/                 transactions, bounds, profiles, leases
  privilege/              typed helper protocol, pkexec client, and recovery
  stress/                 orchestration and engine adapters
  session/                recording and comparison
  report/                 JSON, CSV, and HTML export
  ui/                     Fyne pages, components, themes, and view models
assets/                   icons, shaders, report templates, notices
packaging/                PolicyKit, desktop integration, Arch, and bundle files
```

No package may bypass a core interface to write hardware controls directly.

## 7. Asynchronous telemetry and lazy UI

### 7.1 Discovery and subscriptions

Startup performs only lightweight device discovery and creates a metric catalog. Pages, heavy widgets, historical queries, and provider subscriptions are created on first use.

A metric is sampled only while at least one consumer subscribes to it. Consumers include:

- the currently visible page;
- an active session recording;
- a running stress test;
- a user-pinned tray or overview metric.

Closing a page releases its subscriptions unless another consumer still requires them.

### 7.2 Cadence classes

The scheduler groups requested metrics into provider-level batches instead of issuing one request per sensor. It uses independent cadence classes:

- fast: utilization, effective clock, package power, and workload throughput;
- medium: temperatures, fan speeds, and throttling state;
- slow or event-driven: static hardware data, supported ranges, and rarely changing limits.

The UI interval controls the desired visible resolution between 100 ms and 5 s. Providers may expose their minimum safe cadence and the scheduler clamps or aggregates requests transparently. A fast graph does not force unrelated static metrics into the fast loop.

### 7.3 Isolation and backpressure

Each provider runs in its own cancellable goroutine with a deadline, exponential backoff after repeated errors, and a circuit breaker for persistent failures. One blocked provider cannot delay another.

Snapshots enter bounded queues using a latest-value-wins policy. If a consumer is slow, stale intermediate frames are coalesced rather than accumulating memory or blocking producers. Dropped samples are counted and visible in diagnostics.

The aggregation layer publishes immutable frames. Fyne view models batch changes and schedule a single main-thread update. Tables are virtualized. Charts retain recent full-resolution points in a ring buffer and progressively downsample older ranges according to display width.

### 7.4 Data quality

Every metric carries:

- stable metric and device IDs;
- monotonic timestamp plus wall-clock correlation;
- value and unit;
- source provider;
- freshness and quality state;
- optional error or unsupported reason.

Values that exceed their freshness window are marked stale rather than silently reused. Shell commands are prohibited in hot polling paths. A fallback command adapter, when unavoidable, runs out of process with a strict deadline and bounded output.

## 8. User interface

The selected visual direction is **XTU Classic**: persistent left navigation, a central work area, and an always-visible right tuning panel.

### 8.1 Navigation

- **Overview** — current profile, headline CPU/GPU thermals, package power, effective P/E-core clocks, fan speed, PL1/PL2, and prominent throttling reasons.
- **Monitor** — configurable live charts, per-core and per-device selection, pause, zoom, markers, and CSV export.
- **Tune** — capability-driven grouped controls with staged changes, review, Apply, and Revert.
- **Stress Tests** — workload builder, live safety state, phase progress, and emergency Stop.
- **Profiles** — named profiles, compatibility, temporary application, import, and export.
- **Reports** — experiment history, A/B comparison, and export.
- **Hardware** — detected devices, providers, supported controls, driver state, and diagnostics.
- **Logs** — user-readable events with expandable technical detail.

### 8.2 Monitoring interaction

Users can show individual cores or grouped P/E-core statistics, overlay temperature, power, clocks, throttling, FPS, and frametime, and choose the visible sampling interval. Applying a profile, starting a phase, reaching a threshold, or adding a manual marker annotates the same timeline.

Rendering and collection rates are independent. Pausing a chart does not stop an active recording. Changing the visible time range does not alter the underlying test.

### 8.3 Tuning interaction

Sliders and fields stage changes locally. Apply opens a review showing old and new values, validation warnings, temporary-profile behavior, and the controls that require authorization. Only confirmation invokes the privileged helper. Read-back discrepancies are reported precisely and the transaction is rolled back.

Unsupported controls are not rendered as apparently functional sliders. Hardware diagnostics explain whether the cause is CPU support, firmware lock, kernel policy, missing driver, or insufficient privileges.

## 9. Tuning transactions and safety

### 9.1 Transaction protocol

For each mutation the helper:

1. identifies the exact hardware backend and verifies protocol compatibility;
2. rereads all affected values;
3. validates the complete change set and cross-field constraints;
4. saves an in-memory and durable recovery snapshot;
5. writes values in backend-defined safe order;
6. reads every value back;
7. commits only after verification;
8. restores the snapshot immediately after a partial write, failed verification, or cancellation.

Profiles store semantic controls, units, and hardware compatibility constraints, not arbitrary register writes.

### 9.2 Temporary-profile lease

Temporary application is the default. The GUI renews a helper-owned lease while the profile is active. The helper restores the pre-application snapshot after missed heartbeats, GUI termination, explicit Revert, test completion when requested, or session logout. Lease timing is conservative and independent of UI rendering.

The recovery snapshot is persisted atomically so the next helper session can repair an interrupted transaction. A reboot naturally clears volatile hardware controls. Persistent boot-time application is not provided in the first Intel-tuning milestone.

### 9.3 Stress safety controller

Safety enforcement is independent of chart rendering. A stress run stops when:

- a configured CPU or GPU thermal limit is reached;
- a required critical sensor becomes unavailable or stale;
- the workload stops responding;
- the helper reports a tuning transaction failure;
- the user presses the globally available Stop control.

Default thermal bounds come from hardware or driver-reported critical limits with a backend-defined safety margin. When no trustworthy critical limit exists, the app requires an explicit conservative limit before starting that device's stress test.

Stopping a test terminates its process group, records the reason, begins cooldown monitoring, and restores the temporary profile according to the run configuration.

## 10. Stress-test system

### 10.1 Workloads

The first release includes:

- CPU integer;
- FPU scalar, SSE, and AVX modes supported by the CPU;
- cache;
- RAM read, write, and mixed access;
- Vulkan compute;
- Vulkan graphics;
- combined CPU+RAM, CPU+GPU, and maximum-system presets.

Users can select CPU sets, thread count, memory allocation, target intensity, duration, and one or more Vulkan devices. Unsupported instruction modes are filtered by detected CPU capability.

### 10.2 Engines

CPU, FPU, cache, and RAM workloads use a pinned, packaged `stress-ng` build behind the `StressEngine` adapter. The user never supplies raw `stress-ng` arguments. Its license, notices, build recipe, and corresponding-source obligations are included in release materials.

Vulkan compute and graphics workloads are implemented in the bundled stress worker with packaged shaders. They enumerate physical devices explicitly and support independent or simultaneous iGPU and dGPU load. The installed system driver and kernel device support are prerequisites; the app downloads neither.

### 10.3 Run lifecycle

Every run has warm-up, measurement, and cooldown phases. Presets are:

- Quick: 5 minutes;
- Standard: 30 minutes;
- Soak: 2 hours;
- Manual: runs until stopped or until a configured safety condition fires.

The session records workload throughput, temperatures, power, effective clocks, throttling reasons, dropped or stale telemetry, profile state, and exact termination cause throughout all phases.

## 11. Experiment recording and reports

The canonical on-disk session is a versioned JSON document plus an append-only sample stream or chunked equivalent that can be recovered after interruption. CSV exports provide selected metric tables; the self-contained HTML report renders summaries and charts without network access.

The A/B workflow binds a baseline and candidate run to the same workload definition. The comparison reports:

- average and peak temperature;
- average and peak power;
- average effective P-core and E-core clocks;
- workload throughput or benchmark score;
- percentage and duration of each throttling reason;
- errors, stale-data intervals, and safety stops;
- absolute and percentage differences between runs.

Game sessions can import MangoHud CSV/log data and align FPS and frametime by timestamp. The integration is optional and does not make MangoHud a runtime requirement for synthetic tests.

## 12. Failure handling and diagnostics

- Provider failures are localized and use timeout, backoff, and circuit-breaker state.
- Missing values remain explicit; zero is never substituted for unavailable telemetry.
- A worker crash ends its run but leaves monitoring and profile restoration active.
- Helper disconnects disable Apply immediately and trigger temporary-profile recovery.
- Version mismatch between GUI, helper, and worker blocks incompatible operations with an actionable message.
- Logs provide a concise explanation first and expandable technical data second.
- Hardware diagnostics show provider latency, last success, current subscriptions, queue drops, backoff state, and capability reasons.
- Sensitive values and environment data not needed for diagnosis are omitted from exported reports by default.

## 13. Testing strategy

### 13.1 Simulation

A deterministic fake-hardware backend supports GUI and service development without root access. Scenarios include normal load, progressive heating, PL1/PL2 throttling, sensor loss, stale metrics, delayed providers, partial tuning writes, helper restart, and stress-worker crash.

### 13.2 Automated tests

- Unit tests cover parsers, unit conversion, aggregation, downsampling, profile compatibility, bounds, and report calculations.
- Race-detector tests exercise subscription churn, provider cancellation, queue coalescing, and session writes.
- Privileged-helper integration tests use fake register and sysfs stores to verify transaction ordering, read-back, durable snapshots, and rollback.
- Stress supervisor tests cover timeout, cancellation, child-process cleanup, safety stops, and corrupted worker messages.
- UI view-model tests cover loading, stale, unsupported, disconnected, staged, applying, failed, and restored states.
- Performance tests exercise hundreds of metrics at a 100 ms requested cadence and verify bounded queues, stable memory, and responsive event processing.
- Soak tests detect memory growth, goroutine leaks, orphan processes, and unbounded report files.

Continuous integration never writes to real MSRs or power controls.

### 13.3 Hardware validation

A separate opt-in suite runs only on explicitly authorized local hardware. It begins with read-only discovery, captures stock values, requires confirmation before mutation, verifies each write, performs a minimal bounded load, and restores the original snapshot. The initial validation target is the Intel Core Ultra 9 275HX system with Intel integrated graphics and NVIDIA RTX 5080 Laptop GPU.

## 14. Packaging and distribution

Releases are offline-ready. The primary generic x86-64 artifact is a self-extracting installer containing all redistributable application-owned binaries, assets, worker engines, helper files, PolicyKit policy, desktop integration, required notices, and an uninstaller. Installation invokes a graphical PolicyKit request once for system files.

The release pipeline also produces an Arch/CachyOS `.pkg.tar.zst` first, followed by `.deb` and `.rpm` packages from the same pinned inputs. Distribution-specific packages may rely only on documented base-system and driver interfaces; they must not trigger application-controlled downloads after installation.

System prerequisites that cannot be application-bundled are:

- a supported Linux kernel and x86-64 userspace;
- system graphics/windowing libraries required by Fyne;
- installed CPU kernel interfaces;
- vendor GPU driver and Vulkan ICD for GPU telemetry or stress testing.

Absence of an optional prerequisite disables the affected capability and produces a precise diagnostic.

Every release publishes:

- SHA-256 checksums;
- signed manifests;
- a software bill of materials;
- included third-party licenses and notices;
- reproducible build metadata and pinned dependency versions.

The application may check release metadata only when the user enables that feature. It never downloads or installs an update itself.

## 15. Repository and migration strategy

The GitHub project is forked from `Softorage/undervolt-go` into the authenticated user's account as `undervolt-go-studio`. The original repository remains configured as `upstream` so useful fixes can be reviewed and selectively incorporated.

The fork retains GPL-3.0 licensing, original authorship, and repository history. Product branding changes to Undervolt Go Studio, and documentation clearly describes the relationship to the upstream project.

Migration proceeds by extracting tested seams from the existing code rather than combining the old GUI and the new architecture indefinitely:

1. establish the capability models, interfaces, and fake backends;
2. extract current Intel read/write behavior behind providers and a transaction layer;
3. introduce the unprivileged GUI and privileged helper boundary;
4. replace the existing screens with the lazy XTU Classic shell and telemetry pipeline;
5. add session recording and CPU/RAM stress orchestration;
6. add Vulkan stress engines and GPU providers;
7. add reports, offline packaging, and hardware validation.

Each stage must leave a runnable, testable application. Direct privileged calls from the legacy GUI are removed once their helper-backed replacements exist.

## 16. Acceptance criteria

The first public release is complete when:

- the GUI starts and provides read-only diagnostics without root privileges;
- opening and closing pages changes telemetry subscriptions without blocking the UI;
- a slow or failed provider cannot freeze unrelated telemetry or navigation;
- 100 ms monitoring with hundreds of simulated metrics uses bounded memory and queues;
- supported Intel power controls are capability-driven and applied through PolicyKit;
- a failed or interrupted tuning transaction restores its captured snapshot;
- loss of the GUI renewer causes a temporary profile to be restored by the helper;
- CPU, FPU, cache, RAM, Vulkan compute, Vulkan graphics, and combined tests run from packaged components without downloads;
- iGPU and dGPU can be selected independently when both expose Vulkan devices;
- safety stops work without depending on chart rendering;
- baseline and candidate runs produce a recoverable session and an accurate A/B report;
- the Arch/CachyOS package and generic offline installer install and uninstall cleanly;
- licenses, checksums, signatures, and SBOM accompany the release;
- all automated tests pass and the opt-in target-hardware smoke test restores stock settings.

## 17. Explicit design decisions

- UI stack: Go + Fyne, not Wails or a Rust rewrite.
- Visual structure: XTU Classic layout.
- Privilege model: unprivileged GUI plus a typed, short-lived helper launched through `pkexec` after review.
- Telemetry: asynchronous, batch-oriented, lazy, subscription-driven, and backpressured.
- Default tuning behavior: temporary lease with automatic rollback.
- CPU/RAM engine: packaged `stress-ng` adapter.
- GPU engine: bundled Vulkan compute and graphics workloads.
- Distribution: offline-ready artifacts with no runtime component downloads.
- Initial tuning backend: Intel, with vendor-neutral interfaces for later AMD support.
