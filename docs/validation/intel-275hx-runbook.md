# Intel Core Ultra 9 275HX tuning validation

`tuning-smoke` is a developer validation harness for the Intel backend. End users should use the Studio GUI, which starts the narrow privileged helper only after **Authenticate & apply** is confirmed.

The default invocation is read-only and does not require root:

```bash
go run ./cmd/tuning-smoke --output build/275hx-read-only.json
```

Build the separately inspectable binaries with:

```bash
go build -o build/undervolt-go-studio-helper ./cmd/helper
go build -o build/tuning-smoke ./cmd/tuning-smoke
```

The mutating validation is deliberately awkward to start. It requires all of the following in the same process:

- effective UID 0;
- Intel family 6, model `0xC6`, stepping 2 (`GenuineIntel`);
- a completed read-only capability probe without errors;
- a writable JSON report path;
- the exact confirmation phrase `I UNDERSTAND TEMPORARY CPU TUNING`.

Only after reviewing the read-only report, run:

```bash
pkexec build/tuning-smoke --mutate --confirm 'I UNDERSTAND TEMPORARY CPU TUNING' --output build/275hx-mutation.json
```

The harness and GUI helper share an exclusive recovery lock. The harness tries only controls reported as supported: lower PL1/PL2 by one practical 1 W review step, lower the thermal ceiling by one degree, then choose the most energy-saving EPP policy only when the current kernel policy permits it. Power writes require exact read-back; a mismatch fails the transaction and triggers restoration.

Tau remains read-only because the kernel's microsecond unit does not establish representable time windows. P-core ratios and core/cache voltage remain read-only because a safe writability/lock/stock-restoration probe is not available, including for model `0xC6` stepping 2. Their codecs and bounded operations are exercised only against fixture devices. E-core ratios remain read-only because their register layout is unverified. The harness therefore skips all of these controls.

Each case saves recovery state before any write, applies with read-back verification, restores immediately, verifies the restored values, and persists the report before continuing. It stops after the first failure. An earlier-boot record or other unresolved recovery discrepancy blocks mutation until audited. Do not run mutating mode from automated CI.

On the validated 275HX host, active HWP `intel_pstate` with the `performance` governor rejects non-performance EPP writes. Discovery therefore marks EPP `kernel_blocked` until the user selects a balanced system power profile outside Studio; Studio never changes `scaling_governor` itself.
