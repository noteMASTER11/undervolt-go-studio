# Final safety-review fix report

Date: 2026-09-11, Asia/Tbilisi. Base: `fa1c7bc`.

Scope: the 13 numbered findings in `final-review-findings.md`, interpreted against the authoritative Intel tuning backend design and implementation plan. This is one coherent safety wave, not a new feature milestone. All findings are resolved in this wave. The existing Overview/Monitor/Hardware snapshot extension is retained unchanged, as accepted by review.

## Safety rulings and user-visible cost

- New P-core ratio and core/cache voltage Apply is disabled unconditionally in the production MSR driver, including GenuineIntel family 6/model `0xC6`/stepping 2. Successful reads cannot prove writability, firmware lock state, or safe stock restoration. Ratio values are read-only when readable; otherwise the kernel-blocked reason remains visible. Voltage values and bounds remain unknown; discovery issues no mailbox commands, since even a voltage read command would require an MSR write. Low-level codecs/operations remain fixture-tested, not available to new production transactions. E-core ratios remain read-only because the layout is unverified.
- PL1/PL2 use practical 1 W review increments. These are not a promise that every hardware request is representable: writes still require exact read-back, with mismatch causing rollback. Tau stays read-only with `time_resolution_unverified`; the sysfs microsecond unit alone cannot justify editable time-window resolution.
- Recovery-only exception, explicitly confirmed by the parent: a previously durable same-machine/same-boot MSR snapshot may still be restored under the exclusive lock and strict C6/stepping-2 gate. Abandoning a possibly active prior setting is worse than retaining this narrow recovery path. New Apply cannot create such records. Voltage snapshots must be non-null, finite, non-positive, within -250..0 mV, and exactly representable before any device access. Ratio recovery requires the reviewed eight-P-core topology, a valid non-increasing vector, and exact full-register read-back. For this eight-entry layout all eight bytes are ratio fields; apply codecs retain reserved/unselected bytes for narrower fixture layouts.
- Incomplete rollback stays visible and blocks new tuning. Retry remains available; Reboot to reset opens instructions only and never initiates reboot. Unresolved prior-boot audit records block rather than replay volatile settings or silently delete evidence. That can require explicit maintenance after a reboot.
- README, validation runbook, and the historical read-only evidence page distinguish these current restrictions from older illustrative fixtures. No writable ratio/voltage support or completed hardware mutation validation is claimed for the 275HX.

## Per-finding resolution and regression evidence

### 1 — Critical: persist possibly-modified state before writes

`Applied` now means possibly modified. The recovery record is saved with that flag before each operation's first write, and the flag is retained until restoration verifies. A write followed by failed read-back therefore participates in rollback. Failed rollback returns a live transaction, preserves the recovery record, and supports retry. The first regression failed because no durable arm was present during Apply and the record was lost after failed verification. It now passes.

Coverage: `TestRecoveryArmedBeforeWriteAndRetainedAfterFailedVerification`, `TestEngineRollsBackAppliedOperationsWhenLaterApplyFails`, `TestFailedApplyReturnsRetryableIncompleteRollback`, and existing forward/reverse ordering and same-boot recovery tests.

### 2 — Important: one recovery/hardware owner

Added a nonblocking exclusive filesystem lock in the recovery directory. The helper acquires it before discovery/recovery and retains it through session shutdown/final rollback. The opt-in mutating smoke path uses the same lock. The stable lock inode is never unlinked; directory mode is checked and symlink following for the lock file is denied. Read-only smoke does not acquire or create the production lock.

Coverage: `TestRecoveryLockExcludesAnotherOwnerUntilClose` verifies a second independently opened descriptor is refused while ownership exists, then succeeds after close. Test storage is temporary, not the production recovery directory.

### 3 — Important: hydrate, recover, reprobe, publish

The helper state machine now hydrates driver source tables, performs recovery, rejects stale-boot/discrepant results, and reprobes before publishing stock capabilities. It no longer discards `RecoveryResult`. Hydration/recovery errors fail closed.

Coverage: `TestServerHydratesRecoversAndReprobes`, `TestServerBlocksUnresolvedRecovery`, and `TestHelperRestoresHydratedKernelSnapshotBeforePublishingStock`. The latter composes the real helper backend, powercap driver, recovery engine, and temporary file store with fake sysfs, proving restoration to 44 W precedes published stock and record removal.

### 4 — Important: revalidate immediately before Begin

Begin now reprobes and compares machine/generation immediately before Apply. A changed result returns fresh review data without applying or terminating the usable session. Helper probing rechecks CPUID identity. Capability generation includes an opaque backend-only source revision covering resolved source identity and relevant raw values, microcode version, online CPUs, and Intel P-state state. Powercap includes every participating constraint and bound, even a source hidden by the effective minimum. EPP includes policy/governor/driver inputs; TCC includes both interfaces and the precise temperature-reference identity. Raw source paths are excluded from capability JSON.

Coverage: `TestServerReprobesBeforeBeginAndReturnsChangedReview`, `TestGenerationChangesWhenHiddenPowercapSourceChanges`, `TestRevisionChangesForSameValueSourceReplacementAndFirmware`, and `TestTCCRevisionIncludesTemperatureReferenceIdentity`. Regressions distinguish changed backing sources even when the displayed effective value is unchanged.

### 5 — Important: practical power targets; unknown Tau resolution

Powercap reports 1 W review steps for adjustable power limits instead of 1 µW. Tau is read-only, has no editable range, and cannot be prepared even when a source exposes bounds. Exact power read-back remains mandatory; no arbitrary quantization tolerance conceals clamping. Consequently the smoke harness selects 43 W rather than 43.999999 W from a 44 W starting point.

Coverage: added 1 W and read-only/no-Tau-Prepare assertions in powercap tests, including `TestProbeCombinesMSRAndMMIOPackageConstraints`, plus existing multi-source apply/read-back/restore tests and smoke safety-gate tests.

### 6 — Important: persistent session events and terminal state

The client now exposes Events/Done, forwards unsolicited lease/rollback messages, stops renewals on termination, rejects reuse of dead sessions, and conservatively marks unknown termination incomplete. The desktop service watches that lifetime, discards dead clients and privileged caches, and propagates persistent events to the view model independently of individual Apply calls. Successful previous rollback proof is reset before a new Begin so it cannot clear an unknown later transaction.

Coverage: `TestClientDeliversUnsolicitedRollbackAndRejectsDeadSession`, `TestNewBeginCannotReusePreviousRollbackProofAfterEOF`, `TestDesktopTuneServiceForwardsTerminationAndDropsDeadClient`, and `TestTuneLeaseRollbackClearsActiveSession`. Persistent state also survives page activation/navigation.

### 7 — Important: honest incomplete rollback, remaining values, retry and close

Added structured rollback outcomes with verified Remaining values and explicit Unverified control IDs. Remaining readings are obtained from all captured sources of the exact operation, not a fresh partial discovery that might silently omit a failed source. No requested/last-applied value is passed off as a verified remaining value. The server retains an incomplete transaction for retry, including incomplete rollback during Apply before an ID was returned. The client and desktop service preserve that structured error and retain the recovery path. Desktop Close is serialized and returns failure without finalizing or closing the window; close interception displays the error. The view model preserves the critical details across navigation/reset and refuses new edits while unresolved. The page shows persistent details, Retry rollback / recovery, and Reboot to reset instructions. Layout refresh prevents outcome text and recovery buttons overlapping.

Coverage: `TestServerTransmitsEffectiveAndRetryableRollbackOutcome`, `TestRollbackResponseRetainsStructuredFailure`, `TestClientRetriesIncompleteApplyInsteadOfReturningFalseRevertSuccess`, `TestRollbackNeverReportsPartialPowercapProbeAsVerifiedRemaining` (the initial regression caught an omitted failed source falsely reported as verified), `TestDesktopTuneServiceRetainsClientOnIncompleteClose`, `TestDesktopCloseDoesNotDiscardRecoveryAfterHelperLoss`, `TestWindowCloseFailureRemainsVisibleAndKeepsWindowOpen`, `TestTunePersistsRollbackDetailsAcrossNavigationAndReset`, and `TestTuneRendersVerifiedResultsAndRecoveryActions`.

### 8 — Important: retain/transmit/render effective values

Active transactions retain successful semantic read-back values and expose a defensive copy. Applied responses transmit them; service/view-model state retains them; the workbench displays Requested and Verified separately. The fixture intentionally requests 40 W and returns 39.5 W to prove the distinction survives every relevant layer. This UI fixture does not weaken exact production powercap verification.

Coverage: `TestTransactionRetainsVerifiedEffectiveValues`, `TestServerTransmitsEffectiveAndRetryableRollbackOutcome`, `TestDesktopTuneServicePublishesSemanticApplyEvent`, and `TestTuneRendersVerifiedResultsAndRecoveryActions`.

### 9 — Important: bounded metadata/output, rollback before response delivery

Protocol metadata/request IDs are limited to 128 bytes, semantic changes to nine, ratio vectors to eight, and per-session request history to 4096 IDs. Duplicate IDs are rejected. Output waits are bounded to min(250 ms, lease); timeout closes the stream where supported. Invalid active transitions roll back before any error output, and an applied-response timeout triggers rollback rather than leaving hardware active behind blocked stdout. The state machine does not depend on successful rollback-response delivery to restore. Existing framing, strict payload decoding, directions, and semantic vocabulary remain intact.

Coverage: `TestRejectsUnboundedEchoMetadata`, `TestBlockedOutputCannotHoldHardwareAfterApply` (initially timed out after 500 ms with rollback blocked behind an undrained pipe), `TestProtocolFailureReportsVerifiedRollback`, and EOF/lease tests. The repaired test stops draining the same pipe used for replies and still observes bounded rollback. No shell, raw path, or MSR-address payload was added.

### 10 — Important: bound voltage transitions after quantization

Voltage stepping now operates in signed integer mailbox units. Each transition is at most ten units, exactly 9.765625 mV, and the target is normalized once. Float interpolation can no longer create a post-quantization jump exceeding 10 mV.

Coverage: `TestVoltageTransitionsNeverExceedTenMailboxUnits`, tested with targets -20, -30, -50, -125 and -250 mV. The initial regression caught a -19.53125 to -30.2734375 mV transition (10.7421875 mV). Existing signed encoding, bounded range, plane command, failure restoration and lock-detection fixture tests remain green.

### 11 — Important: fail closed on unproved MSR writability/restoration

Applied the conservative rulings above: production Prepare always rejects ratios/voltage; read-only discovery neither fabricates zero values nor treats a readable plane as writable. Capture rejects voltage stock outside the exact supported restoration policy. The recovery-only path rejects malformed/null/out-of-range/off-grid voltage snapshots before even a mailbox read command, rejects invalid ratio snapshots, and requires the reviewed ratio topology. Unsupported models remain inaccessible.

Coverage: `TestProductionDriverCannotAuthorizeUnprovedWrites` uses a locked fake device and verifies zero discovery writes; `TestVoltageCaptureRejectsStockOutsideRestorePolicy`; `TestRecoveryRestoreRejectsInvalidSnapshotsBeforeDeviceAccess` initially caught null snapshots accepted and invalid target checks happening after mailbox access; `TestRecoveryRestoreRequiresReviewedRatioTopology` initially caught a write with the wrong P-core count. All pass after fixes. Existing strict `TestLookupModelAllowsOnlyC6Stepping2`, no-access unknown-model, exact register restoration, and reserved-bit-preserving codec tests remain green.

### 12 — Important: helper build identity handshake

Added hello acknowledgement carrying fixed identity `undervolt-go-studio-helper/intel-safety-2`. The client requires exact matching semantics before requesting capabilities; a stale helper is rejected with installation-repair guidance. Framed protocol version remains 1 and helper argv stays exactly the fixed `--session --protocol=1` command; no alternate executable/path request is accepted.

Coverage: `TestHandshakeRejectsIncompatibleHelperIdentityBeforeProbe`, `TestClientHandshakeReturnsCapabilities`, and fixed-command launcher tests.

### 13 — Minor: truthful live frequency label

Renamed the aggregate frequency metric to CPU maximum because its samples include all CPU frequency metrics, not a verified P-core subset.

Coverage: `TestTunePageUsesWorkbenchGroupsAndPendingRail` asserts the visible title; `TestTuneTelemetrySnapshotUsesFastestCPUFrequency` preserves the aggregation behavior. The refreshed screenshot visibly uses CPU maximum.

## Related session-lifetime defect found during this wave

The helper process previously inherited the per-Apply request context, whose deferred cancellation could terminate a successfully authorized active session. Process startup now observes authorization cancellation only until a successful handshake, then the pipe/lease and explicit close own its lifetime. `TestAuthorizedSessionOutlivesApplyRequestContext` uses only a harmless `cat` fixture to prove cancellation after authorization leaves the session pipe usable. No `pkexec` was launched by that test or by this wave.

## Verification record

The defect work used focused red/green tests before implementation, followed by package tests continuously. The regressions named above capture behavior at the safety boundaries; supplemental composition, race and visual checks were added after the primary red/green cycles. Final verification used `LC_ALL=en_US.UTF-8` to avoid Fyne's unrelated default-C-locale diagnostic.

Final commands, all exit 0:

```bash
LC_ALL=en_US.UTF-8 go test ./... -count=1
LC_ALL=en_US.UTF-8 go test -race ./internal/tuning/... ./internal/privilege/... ./internal/ui/... -count=1
LC_ALL=en_US.UTF-8 go vet ./...
go build ./...
go build -o build/undervolt-go-studio ./cmd/studio
go build -o build/undervolt-go-studio-helper ./cmd/helper
go build -o build/tuning-smoke ./cmd/tuning-smoke
git diff --check
```

The full test command covers 26 tested packages plus the two entrypoints with no tests. The required race matrix covers all 15 tuning, privilege and UI packages. No race report, vet finding, build error, or whitespace error remained.

### Headless screenshots

Regenerated only the visibly changed Tune documentation image. Two independent runs were byte-identical:

```bash
LC_ALL=en_US.UTF-8 go run ./cmd/ui-snapshot --page tune --scenario intel-275hx --output docs/images/studio-tune.png --width 1600 --height 1000
LC_ALL=en_US.UTF-8 go run ./cmd/ui-snapshot --page tune --scenario intel-275hx --output build/tune-final-second.png --width 1600 --height 1000
cmp docs/images/studio-tune.png build/tune-final-second.png
```

An optional headless test capture generated active/incomplete states:

```bash
LC_ALL=en_US.UTF-8 STUDIO_TUNE_QA_DIR=/home/user/Documents/Codex/2026-09-10/x20/work/undervolt-go-upstream/.worktrees/foundation-telemetry/build go test ./internal/ui/pages -run TestTuneRendersVerifiedResultsAndRecoveryActions -count=1
```

All three 1600×1000 PNGs were inspected with the local image viewer. Requested/Verified labels, incomplete remaining-value text, Retry and Reboot buttons and guidance are readable; recovery actions follow the results without overlap. The primary workbench has a visible scrollbar for lower controls. No active/native windows were opened or touched. Overview, Monitor and Hardware documentation images remain unchanged.

SHA-256:

```text
2366bd47d77b4ea28f7752f4f6a7412997fc85f72e510a28bf29d4ac63c2e7ea  docs/images/studio-tune.png
ee3dba14d6243cf1eb75796ff5eed6412cd48aa7e3c94627a3fdad99a5f408e3  build/tune-active.png
479d4805490634f028f9e149f024b5b001c052d66f55fcbde4c678da0f1ce8da  build/tune-rollback-incomplete.png
```

### Read-only host observation

Executed `build/tuning-smoke --output build/275hx-final-read-only.json`, exit 0, report timestamp `2026-09-11T22:08:01.112588963+04:00`, mode `read-only`. Identity was GenuineIntel family 6/model 198/stepping 2, with 8 P CPUs and 16 E CPUs. No probe errors occurred. Observed capability generation was `a213889d17869454`.

PL1: supported, 44 W, 0–55 W, 1 W review step. PL2: read-only, 44 W, unknown upper bound. Tau: read-only, 27.983872 s, unverified time resolution. TCC: supported, 95 °C, 0–105 °C. EPP: supported, current `default`, choices `default`, `performance`, `balance_performance`, `balance_power`, `power`. P-core ratios: kernel-blocked with `msr_permission_denied`, no value. E-core ratios: read-only/unverified layout. Core/cache voltage: read-only/unverified eligibility, no value/range. These observations do not establish safe hardware writes or restoration. The synthetic screenshot's enabled PL2 is demonstration data, not this host's claim.

## Remaining concerns and operational limits

No hardware mutation, `pkexec`, opt-in mutation flag, production recovery edit, native-window interaction, or subagent was used. Device writes in tests were exclusively fake devices or temporary fixture files. Built binaries, local read-only JSON, and supplementary QA screenshots remain ignored build outputs, not release artifacts.

Automated evidence establishes the implemented failure handling, not universal firmware safety. Real kernel-backed write/read-back/restore validation remains an explicit opt-in task. A hard lock, panic or power loss cannot be undone by a running userspace process. Prior-boot audit resolution is deliberately conservative and has no automatic deletion/override UI. Users presently lose Tau, ratio and voltage editing; that is an intentional cost of refusing unproved mutation rather than a hidden availability claim. Recovery-only MSR restoration is the narrow documented exception, not a writable capability.
