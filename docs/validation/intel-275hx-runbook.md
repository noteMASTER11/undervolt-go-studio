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

The harness tries only supported controls, in this order: lower PL1/PL2 by one advertised step, lower the thermal ceiling by one degree, choose the most energy-saving advertised EPP policy, lower every verified P-core ratio by one bin, and apply an additional −10 mV core/cache offset. Each case captures stock values, applies with read-back verification, restores immediately, verifies the restored values, and persists the report before continuing. It stops after the first failure. Do not run mutating mode from automated CI.
