# Intel Core Ultra 9 275HX read-only verification

Date: 2026-09-11 (Asia/Tbilisi)
Verification baseline: `241f515ac21a415403176bae19daba5b875e4a11` (`fix: hydrate drivers before smoke recovery`)

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

- Powercap: PL1 is supported at 44 W (0–55 W); PL2 is read-only at 44 W and turbo time is read-only at 27.983872 s because the kernel does not expose writable upper bounds.
- TCC: the thermal limit is supported at 95 °C (0–105 °C).
- EPP: energy preference is supported, currently `default`, with `default`, `performance`, `balance_performance`, `balance_power`, and `power` choices.
- MSR controls do not fabricate values. P-core ratios and core/cache voltage offsets report `kernel_blocked` with `msr_permission_denied` when `/dev/cpu/0/msr` cannot be opened; E-core ratios are read-only because their register layout is not verified.

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

No root prompt occurred during those commands or the read-only probe.

## Headless Tune UI review

Rendered command:

```bash
go run ./cmd/ui-snapshot --page tune --scenario intel-275hx --output build/tune-275hx.png --width 1600 --height 1000
```

Screenshot: `build/tune-275hx.png` (1600 × 1000).

The deterministic `intel-275hx` scenario renders PL1/PL2 at 44 W, a 95 °C thermal ceiling, EPP choices, a supported P-core ratio vector, and a firmware-locked core voltage control using the injected Tune service. The locked-voltage capability deliberately leaves `Current` unset and omits `Range`; the primary UI therefore does not imply that a 0 mV value is available. The Tune capture waits for completed discovery after its final state has rendered, rather than sleeping for a fixed interval. The snapshot path does not construct or call the `pkexec` client.

Visual inspection using the local image viewer found readable labels and disabled states, no overlapping cards, no clipped labels, a visible Pending changes rail, and no raw technical data in the primary view. The lower control cards remain accessible through the visible content scrollbar. The Fyne runtime emitted a non-fatal locale-`C` parsing diagnostic while rendering; the command still exited 0 and produced the PNG.

## Safety statement

This verification used only the default read-only `tuning-smoke` mode and a Fyne headless test canvas. It did not use `--mutate`, `pkexec`, native/active windows, or any hardware write operation.
