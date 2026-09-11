# Intel Tuning Backend and Tune Workbench — Design Specification

Date: 2026-09-11

Status: Approved in conversation; awaiting written-spec review

Supersedes the privileged-helper transport and Intel-tuning details in `2026-09-11-undervolt-go-studio-design.md`. The general product specification remains authoritative for the rest of the application.

## 1. Purpose and milestone boundary

This milestone replaces the read-only Tune placeholder with an Intel XTU-style workbench and a safe, temporary tuning backend. The first hardware validation target is the current Intel Core Ultra 9 275HX machine. The implementation must be capability-driven rather than exposing guessed controls merely because the processor is made by Intel.

The milestone includes:

- an unprivileged Tune page with staged changes;
- asynchronous control discovery;
- a short-lived privileged session helper launched by `pkexec`;
- transactional apply, read-back verification, lease renewal, and rollback;
- kernel-first control of PL1, PL2, Turbo Time Window, TCC thermal offset, and energy-performance preference;
- allow-listed model-specific probing for P/E-core ratios and voltage offsets;
- user-readable errors and an expandable technical log;
- fake-backend, protocol, failure-injection, UI, and opt-in hardware tests.

This milestone does not implement persistent boot-time tuning, automatic overclocking, a custom kernel module, AMD controls, or the complete Stress Tests page. It exposes the control, telemetry, and session events needed by the later stress subsystem. Release-level validation still requires the packaged CPU, memory, GPU, and combined stress engines described by the general product specification.

## 2. Verified target-machine baseline

The design is grounded in a read-only probe of the development machine on 2026-09-11:

| Item | Observed value |
|---|---|
| CPU | Intel Core Ultra 9 275HX |
| CPUID identity | GenuineIntel, family 6, model 198 (`0xC6`), stepping 2 |
| Topology | 24 physical cores/threads: 8 P-cores and 16 E-cores |
| Reported maximum | 5.4 GHz overall; per-policy maximums distinguish the faster P-core policies from E-core policies |
| Microcode | `0x122` |
| Kernel | CachyOS Linux `7.2.3-1-cachyos` |
| CPU frequency driver | `intel_pstate`, active, HWP/EPP available |
| EPP choices | default, performance, balance_performance, balance_power, power |
| RAPL drivers | `intel_rapl_msr`, `intel_rapl_common`, and MMIO powercap zones present |
| Package powercap | current long- and short-term constraints both read 44 W during the probe |
| Reported long-term maximum | 55 W through both MSR-backed and MMIO-backed powercap zones |
| Thermal interface | `intel_tcc_cooling` exposes `tcc_offset_degree_celsius`; current offset is 10 °C |
| Package critical temperature | 105 °C through `coretemp`; the observed TCC offset therefore represents a 95 °C effective ceiling |
| Direct MSR device | `/dev/cpu/0/msr`, root-only |

Intel lists this processor with 55 W base power, 160 W maximum turbo power, 8 P-cores up to 5.4 GHz, and 16 E-cores up to 4.6 GHz. Marketing limits are descriptive, not writable-range authority. The application uses ranges reported by the active kernel/firmware interfaces and treats a zero or missing maximum as unknown rather than as a valid zero-watt range.

The target identity is a validation fixture, not a wildcard. Family 6/model `0xC6` receives model-specific writes only through an explicit table with reviewed masks and round-trip tests. Other models remain kernel-interface-only until they receive their own entry.

## 3. User experience: XTU Workbench

The selected Tune layout is **A — XTU Workbench**:

- persistent left application navigation;
- live package temperature, package power, and maximum P-core frequency in the header;
- a central work area containing Power Limits, Thermal & Voltage, and Core Ratios groups;
- an always-visible Pending Changes rail;
- a review dialog that shows stock, requested, and expected values before authorization.

Controls never write while a slider is dragged. The view model stages semantic values locally. Reset clears the stage; Review & Apply opens a complete transaction summary. The authentication request begins only after confirmation.

Each control has one of these states:

- `supported`: readable and writable with a known range and step;
- `read_only`: readable, but the active interface cannot safely write it;
- `firmware_locked`: the CPU exposes the feature but firmware or undervolt protection rejects mutation;
- `kernel_blocked`: a known kernel interface or driver is missing or denies access;
- `requires_probe`: availability requires the privileged model-specific probe, which is deferred until Apply;
- `unavailable`: absent on this hardware;
- `unknown_model`: a raw-register implementation exists only for other allow-listed CPU models.

Unsupported and locked controls remain visible where that helps explain the machine. They are disabled and include a plain-language reason. Raw MSR addresses, masks, paths, and errno values appear only in expandable diagnostics.

## 4. Capability model

The GUI and helper exchange semantic controls, never arbitrary paths or register addresses. A capability contains:

```text
Capability
  ID                  stable semantic ID, such as intel.package.pl1
  Scope               package, policy, P-core group, E-core group, or voltage plane
  Label               user-readable name
  Unit                W, s, °C, multiplier, mV, or enumerated preference
  State               one of the states defined above
  Current             last verified effective value
  Minimum/Maximum     nullable bounds
  Step                nullable representable increment
  WritableSource      kernel_powercap, intel_pstate, intel_tcc, or allow-listed MSR backend
  RequiresPrivilege   whether mutation or probing needs the helper
  Reason              stable reason code plus human-readable explanation
  Freshness           timestamp and stale state
```

Capabilities are immutable snapshots identified by a generation number. A staged transaction records the generation it was built against. The helper rejects an Apply request when the machine identity, firmware state, source paths, bounds, or generation-relevant values changed, and returns a fresh capability set for review.

Cross-field validation includes:

- PL1 must not exceed PL2 when both limits are under application control;
- every powercap source participating in the effective package limit must be snapshotted;
- thermal target is represented as `TjMax - TCC offset`, with both numbers shown in diagnostics;
- ratio entries must preserve the backend's required monotonic ordering;
- voltage offsets must be negative or zero in this milestone; positive voltage is rejected;
- values must be exactly representable by the source step or explicitly normalized before review.

## 5. Components and boundaries

### 5.1 Tune page and view model

`internal/ui/pages/tune.go` owns only page construction and widget binding. `internal/ui/viewmodel/tune.go` owns asynchronous state, staging, review data, helper-session state, and user-readable errors. It depends on the semantic tuning service and never imports an Intel register package.

The page is lazy-created on first navigation. Leaving Tune cancels its discovery work and visible-only telemetry subscriptions. An active tuning lease is not silently abandoned: navigation may continue, but closing the application or explicitly ending the tuning session triggers rollback.

### 5.2 Unprivileged discovery

`internal/tuning/discovery` batches read-only discovery from powercap, `intel_pstate`, `coretemp`, the Intel TCC sysfs attribute, CPU topology, and CPUID. It returns quickly with kernel-backed controls, then publishes slower details as a new capability generation.

Direct `/dev/cpu/*/msr` access is not attempted by the GUI. Controls that cannot be established without it remain `requires_probe`; the GUI does not invent their current value or writable range.

### 5.3 Intel kernel backend

`internal/tuning/intel/kernel` maps allow-listed sysfs interfaces to semantic controls. It receives a filesystem abstraction for deterministic tests. It may resolve only paths discovered under fixed kernel roots and cannot accept a path from the GUI protocol.

Kernel interfaces are preferred because their drivers understand platform integration and provide a narrower authority boundary than raw MSR writes.

### 5.4 Intel MSR backend

`internal/tuning/intel/msr` contains:

- a minimal random-access MSR device abstraction;
- reviewed field codecs;
- explicit CPU-model tables;
- per-control read, validate, write, and read-back operations;
- no generic `WriteMSR(address, value)` API exposed outside the package.

Every write is a masked read-modify-write that preserves unrelated and reserved bits. The backend refuses writes when the CPU identity is absent from the table, a required feature bit is absent, a lock bit is set, or the read value fails a model invariant.

### 5.5 Session helper and client

`cmd/helper` builds `undervolt-go-studio-helper`. The GUI launches exactly the installed helper path using `os/exec`, with a fixed `--session --protocol=1` argument vector:

```text
pkexec /usr/libexec/undervolt-go-studio-helper --session --protocol=1
```

No shell is involved. The GUI cannot add register addresses, paths, or commands. The GUI verifies the helper protocol and build identity returned by the handshake; the helper independently verifies CPU identity and protocol version before accepting a transaction.

`internal/privilege/protocol` defines the versioned typed messages. `internal/privilege/client` owns process launch, deadlines, heartbeats, cancellation, and response correlation. `internal/privilege/session` owns the privileged state machine and recovery record.

## 6. Control-source strategy

### 6.1 PL1, PL2, and Turbo Time Window

The primary source is the Linux powercap tree. The backend enumerates package zones beneath both `intel-rapl` and `intel-rapl-mmio`, labels constraints by their `constraint_*_name`, and snapshots all writable sources that can participate in the effective limit.

It does not assume that constraint 0 and 1 always mean the same thing on every driver. Empty values are unavailable. A maximum value of zero is treated as unspecified unless kernel documentation for that exact interface defines otherwise.

When duplicate MSR and MMIO package constraints are writable, the helper applies the semantic target to all participating sources in a backend-defined order and verifies each one. This prevents an unchanged secondary limiter from making the displayed result misleading. The effective value shown in the UI is the most restrictive verified active constraint, while diagnostics list every source.

Direct RAPL MSR fallback is allowed only for model `0xC6` when no writable powercap constraint exists and the model table defines the unit, limit, enable, clamp, time-window, and lock fields. The fallback never overrides a set lock bit.

### 6.2 Energy-performance preference

EPP uses the per-policy `energy_performance_preference` files exposed by `intel_pstate`. The capability enumerates only values listed by `energy_performance_available_preferences`. Apply snapshots every affected policy and writes a consistent preference to all selected policies unless the future UI explicitly exposes per-policy tuning.

If the active governor rejects non-performance EPP values, the control reports `kernel_blocked` with that reason; the application does not silently change the governor.

### 6.3 Thermal limit

The preferred source on the target machine is the `intel_tcc_cooling` attribute `tcc_offset_degree_celsius`. The user edits an effective thermal ceiling in °C. The backend reads package `TjMax` from the trusted core-temperature interface and converts:

```text
TCC offset = TjMax - requested effective ceiling
```

Both the offset and effective ceiling are snapshotted and verified. Direct temperature-target MSR fallback is model-table-only and must honor lock, writable-range, and reserved-bit rules.

### 6.4 P-core and E-core ratios

Core type is discovered through architectural hybrid-topology information and cross-checked against policy maximum frequencies. CPU numbering is not assumed to place P-cores before E-cores.

Ratio controls are exposed only after the privileged probe identifies model-table registers and decodes a self-consistent current configuration. The UI groups ratios by active-core-count semantics when the hardware provides them, and exposes P-core and E-core domains separately.

The first milestone supports lowering an existing ratio or restoring the snapshot. Raising above the captured firmware value is rejected. Writes occur after power and thermal limits and before voltage offset. Every domain is read back before the next domain is changed.

### 6.5 Voltage offset

Voltage control is an attempted capability, not a promise that firmware permits undervolting. The model-specific backend performs a read-only capability transaction through the reviewed Intel overclocking mailbox implementation for model `0xC6`. It distinguishes:

- mailbox unsupported;
- overclocking/undervolt protection active;
- plane readable but write locked;
- writable with a verified negative-offset range.

The GUI allows zero or negative offsets only. Apply moves from the current value toward the requested value in 10 mV-or-smaller representable steps, verifies every step, and waits for a helper health interval between steps. Voltage is applied last and restored first.

A kernel panic, machine check, or total hardware lock cannot be repaired by user-space code. If the system remains alive, pipe loss or lease expiry rolls back automatically. If the machine hard-locks, reboot is the recovery mechanism; volatile MSR settings are then expected to reset, and the next helper session audits all kernel-backed controls before permitting another Apply.

## 7. Asynchronous data flow

1. Opening Tune subscribes only to the three header metrics and starts cancellable capability discovery.
2. Fast kernel results render immediately. Slower or unavailable groups retain explicit loading or `requires_probe` states.
3. User edits update an immutable local stage and validation result. No privileged process exists yet.
4. Review captures the current capability generation and displays old, requested, normalization, and warning data.
5. Confirmation launches `pkexec` and the fixed helper command.
6. The helper performs a handshake, re-identifies the CPU and microcode, probes privileged capabilities, and revalidates the entire requested change set.
7. If privileged discovery changes the review materially, Apply stops and the GUI presents a revised review. The user must confirm again; authorization alone never implies acceptance of newly discovered values.
8. The helper captures the complete stock snapshot, writes an atomic recovery record, applies in safe order, and emits progress events.
9. Each write is read back. The helper reports both requested and effective values, including firmware clamping.
10. After success, the GUI renews a ten-second lease over the same pipe. Closing the pipe, missing the lease, explicit Revert, or a fatal session error starts rollback.

Telemetry providers and capability discovery never run on Fyne's main thread. Every operation has a context and deadline. Responses include a request ID and generation; a late result cannot overwrite a newer view-model state. The scheduler coalesces duplicate requests and uses latest-value-wins delivery for live metrics.

## 8. Session protocol and state machine

The transport is a private stdin/stdout pipe to the child helper. Messages are length-bounded JSON frames with a protocol version, request ID, message type, and typed payload. Stderr is reserved for bounded helper diagnostics and is never parsed as protocol data.

Allowed client messages are:

- `hello`;
- `probe_privileged`;
- `begin_transaction` with semantic control changes and capability generation;
- `renew_lease`;
- `revert`;
- `close_session`.

Allowed helper events include:

- `capabilities`;
- `review_changed`;
- `transaction_progress`;
- `transaction_applied`;
- `transaction_failed`;
- `rollback_progress`;
- `rollback_complete`;
- `rollback_incomplete`.

The helper state machine is:

```text
starting -> probed -> snapshot_saved -> applying -> active -> rolling_back -> closed
                                  |          |          |
                                  +----------+----------+-> rolling_back on failure
```

Only one active transaction is allowed per helper process. An out-of-order, duplicate, oversized, or unknown message is rejected. Protocol input cannot select a raw backend or bypass semantic validation.

## 9. Transaction and recovery semantics

Hardware writes are not truly atomic, so the application implements failure-atomic behavior:

1. re-read all affected controls and relevant locks;
2. validate individual and cross-field constraints;
3. create a recovery record under `/run/undervolt-go-studio` using root ownership, mode `0600`, write-then-rename, and `fsync`;
4. apply safety-reducing power and thermal changes;
5. apply ratio reductions;
6. apply voltage offset last, in bounded steps;
7. read back each write and record the effective value;
8. enter the active lease only after every required verification succeeds.

Rollback runs in reverse order: voltage, ratios, thermal policy, EPP, time windows, and power limits, with each source restored to its own captured value. Any failure starts rollback immediately. Successful rollback removes the recovery record only after verification.

On startup, the helper checks for a matching recovery record before accepting new changes. If the record matches the current hardware identity and boot, it attempts recovery. A record from an earlier boot is retained as an audit event but volatile MSR writes are not replayed; kernel-backed values are re-read and discrepancies are reported.

Signals that can be handled, normal GUI exit, EOF, protocol failure, and lease expiry all initiate rollback. `SIGKILL`, kernel failure, power loss, and a complete hardware lock cannot be handled in process; the UI and documentation state this limitation plainly.

## 10. Failure presentation

- Cancelling `pkexec` leaves the staged values untouched and reports that nothing was applied.
- A missing helper, protocol mismatch, or executable-identity failure disables Apply with an installation repair message.
- Firmware-clamped values are successful-but-adjusted results, not false successes: the UI shows Requested and Effective.
- A rejected or failed write rolls back the entire transaction rather than leaving unrelated successful changes active.
- A stale metric remains visibly stale with its last timestamp; zero is never substituted.
- An incomplete rollback produces a persistent critical banner listing every verified remaining value, plus Retry rollback and Reboot to reset actions.
- The user log begins with semantic events such as “PL1 restored to 44 W.” Expandable details may include source path, register field, errno, raw before/after values, helper build, CPUID, and microcode.

## 11. Testing strategy

### 11.1 Unit and golden-vector tests

Tests cover:

- powercap discovery, duplicate MSR/MMIO zones, empty fields, and zero-as-unknown maxima;
- power and time-unit conversion with exact representability checks;
- TjMax/TCC-offset conversion;
- topology classification without relying on CPU numbering;
- ratio field ordering and reserved-bit preservation;
- signed voltage-offset encoding, boundary rejection, and step planning;
- capability-state and user-message mapping;
- recovery-record permissions, atomic replacement, and boot identity.

Reviewed before/after register vectors for family 6/model `0xC6` are fixtures. No CI test opens a real MSR device.

### 11.2 Integration and failure injection

A fake filesystem and fake MSR device execute the full helper state machine without root. Every read, write, read-back, recovery-record operation, protocol frame, and lease transition can fail independently. Tests assert reverse-order rollback and the exact final snapshot after each injected failure.

Protocol tests cover malformed JSON, oversized frames, unknown message types, duplicate IDs, deadline expiry, stale generations, helper EOF, client EOF, and version mismatch. Race tests churn discovery cancellation, staged edits, helper events, and page activation. UI tests cover loading, staged, revised-review, applying, clamped, active, stale, rolling-back, failed, and rollback-incomplete states.

### 11.3 Target-hardware validation

The opt-in suite is guarded by an explicit environment switch and interactive confirmation. It runs on the 275HX in this order:

1. read-only identity and capability probe;
2. snapshot/read-back without mutation;
3. conservative PL1/PL2 change and restore;
4. TCC offset change and restore;
5. EPP change and restore;
6. one-step ratio reduction and restore, only if writable;
7. negative voltage steps of at most 10 mV and restore, only if the privileged probe reports writable;
8. forced GUI termination, pipe loss, lease expiry, and injected partial-write rollback;
9. a 30-minute combined CPU/GPU stress session once the packaged stress subsystem is integrated.

Each mutation records stock, requested, normalized, and effective values plus temperature, package power, effective P/E-core clocks, throttling counters, helper events, and final restored values. Voltage testing stops immediately on machine-check evidence, helper health failure, or a configured thermal threshold.

### 11.4 Readiness gates

The Intel backend milestone is implementation-complete when:

- all automated tests and race tests pass;
- unknown CPU models cannot reach raw MSR writes;
- every supported 275HX control passes write, read-back, and rollback;
- every unavailable 275HX control has a precise, stable reason;
- `pkexec` appears only after explicit confirmation;
- killing the GUI and expiring the lease restore the captured snapshot;
- no Tune interaction blocks the Fyne main thread;
- a hardware-validation report proves the machine returned to its initial values.

Release readiness additionally requires the later packaged 30-minute combined CPU/GPU run to complete without UI stalls and to produce a complete session report.

## 12. Documentation sources

Implementation details must be checked against primary sources before adding a model-table entry:

- [Intel 64 and IA-32 Architectures Software Developer’s Manual](https://www.intel.com/content/www/us/en/developer/articles/technical/intel-sdm.html), especially Volume 4 for model-specific registers;
- [Intel Core Ultra 9 275HX product specification](https://www.intel.com/content/www/us/en/products/sku/242293/intel-core-ultra-9-processor-275hx-36m-cache-up-to-5-40-ghz/specifications.html);
- [Linux kernel power-capping framework documentation](https://docs.kernel.org/power/powercap/powercap.html);
- [Linux `intel_pstate` documentation](https://docs.kernel.org/admin-guide/pm/intel_pstate.html);
- the exact kernel source for `intel_rapl`, `intel_tcc_cooling`, and related interfaces in the supported build environment.

Reverse-engineered behavior may inform an experiment but cannot by itself authorize a write path. Any undocumented mailbox operation requires a narrow implementation, golden vectors, explicit model gating, read-back, and a user-visible experimental label until validated on the target machine.

## 13. Explicit decisions

- Layout: A — XTU Workbench.
- Privilege transport: fixed `pkexec` child helper over a private typed pipe, not a resident D-Bus service.
- Root lifetime: only the confirmed tuning session; the GUI and stress workers remain unprivileged.
- Discovery: kernel-first, asynchronous, lazy, and capability-driven.
- Raw access: model-allow-listed MSR fallback only; no arbitrary register API.
- Apply behavior: staged, reviewed, transactional, read-back verified, and temporary.
- Lease: ten seconds, renewed independently of rendering; loss triggers rollback.
- Ordering: power/thermal, ratios, then voltage; rollback is the reverse.
- Voltage policy: zero or negative offsets only, applied in small verified steps.
- Persistence: no boot-time auto-apply in this milestone.
- Hardware target: family 6/model `0xC6` stepping 2 Core Ultra 9 275HX, without treating model name alone as sufficient identity.
