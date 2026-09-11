# Intel Core Ultra 9 275HX hardware verification

Date: 2026-09-11 (Asia/Tbilisi)
Validated implementation: `47dcbb5` (`fix: handle kernel-blocked EPP recovery`)

## Final hardware result

The final guarded smoke run exited 0. It applied PL1 at 43 W and restored 44 W, then applied a 94 °C thermal limit and restored 95 °C. Every transition used exact read-back. The recovery journal was absent after completion.

An initial run exposed a real kernel restriction: active HWP `intel_pstate` with the `performance` governor rejects non-performance EPP writes with `EBUSY`. The failed write left all policies at their captured `default` value. Recovery now treats an already-matching snapshot as verified without issuing a redundant write, and discovery reports EPP as `kernel_blocked` with reason code `intel_pstate_performance_governor`. The final run therefore skipped EPP instead of presenting it as writable.

PL2 remains read-only at 44 W because its safe upper bound is unknown. Tau remains read-only because the representable time-window resolution is unverified. P-core ratios, E-core ratios, and core/cache voltage offsets remain read-only because safe production writes and exact stock restoration have not been established.

## Host and CPUID

- Kernel: `Linux 7.2.3-1-cachyos x86_64 GNU/Linux`
- CPUID identity: `GenuineIntel`, family `6`, model `0xC6` (198), stepping `2`, maximum basic leaf `35`
- Topology observed: 8 performance CPUs (5.3–5.4 GHz) and 16 efficiency CPUs (4.7 GHz)

## Read-only capability probe

Command:

```bash
build/tuning-smoke --output build/275hx-read-only.json
```

Result: exit 0; report written to `build/275hx-read-only.json` in `read-only` mode.

The probe found the following controls:

- Powercap: PL1 is supported at 44 W (0–55 W); PL2 is read-only at 44 W because the kernel does not expose a safe upper bound; Tau is read-only at 27.983872 s because representable time windows are unknown.
- TCC: the thermal limit is supported at 95 °C (0–105 °C).
- EPP: current value is `default`, but the control is `kernel_blocked` while `intel_pstate` uses the `performance` governor.
- MSR controls do not fabricate writable support. The P-core vector is read-only; E-core layout is unverified; core/cache voltage values and writability remain unknown.

## Automated verification

Each command below exited 0:

```bash
go test ./...
go test -race ./internal/tuning/... ./internal/privilege/... ./internal/ui/...
go vet ./...
go build ./...
go build -o build/undervolt-go-studio ./cmd/studio
go build -o build/undervolt-go-studio-helper ./cmd/helper
go build -o build/tuning-smoke ./cmd/tuning-smoke
```

The automated commands and read-only probe require no root prompt. Only the explicitly guarded hardware smoke used `pkexec`.

## Headless Tune UI review

Rendered command:

```bash
go run ./cmd/ui-snapshot --page tune --scenario intel-275hx --output build/tune-275hx.png --width 1600 --height 1000
```

Screenshot: `build/tune-275hx.png` (1600 × 1000).

The deterministic `intel-275hx` scenario renders PL1/PL2 at 44 W, a 95 °C thermal ceiling, and explicit disabled states for controls that are not safely writable. The Tune capture waits for completed discovery after its final state has rendered, rather than sleeping for a fixed interval. The snapshot path does not construct or call the `pkexec` client.

Visual inspection using the local image viewer found readable labels and disabled states, no overlapping cards, no clipped labels, a visible Pending changes rail, and no raw technical data in the primary view. The lower control cards remain accessible through the visible content scrollbar. The Fyne runtime emitted a non-fatal locale-`C` parsing diagnostic while rendering; the command still exited 0 and produced the PNG.

## Guarded mutation evidence

Command:

```bash
pkexec build/tuning-smoke --mutate --confirm 'I UNDERSTAND TEMPORARY CPU TUNING' --output build/275hx-final-smoke.json
```

Result: exit 0. Cases `power-limits` and `thermal-limit` both recorded requested, effective, and restored values. EPP was omitted because discovery marked it kernel-blocked. A privileged existence check confirmed that `/run/undervolt-go-studio/recovery-v1.json` did not remain after the run.
