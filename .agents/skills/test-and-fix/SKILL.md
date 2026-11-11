---
name: test-and-fix
description: >-
  Autonomous test-fix loop using ./scripts/tests.sh for whatshelper. Run tests,
  read summaries in artifacts/*-errors.log, apply minimal fixes, and repeat
  until green (TTL 5 per target). Use when fixing bugs, repairing failing
  tests, or validating new implementations across lint, unit, brain, api,
  build, and integration.
---

# Test & Fix Workflow (`whatshelper`)

## When to Use

Use this skill when:
- You need to verify or debug a bug fix across the codebase.
- You need to run and repair failing checks after implementing a feature or refactor.

**CRITICAL:** Never run `./scripts/tests.sh all` just to draft or inspect an implementation plan. Reserve it for execution and verification.

---

## Step 1 — Run the full suite

> **Sleep prevention (scoped to `scripts/tests.sh` only):** wrap each call with an idle inhibitor that exits together with the command. Use only `caffeinate -i` on macOS (never `-u`, `-d`, `-m`, `-s`, `-t`, and never backgrounded).
> - macOS: `caffeinate -i ./scripts/tests.sh <target>`
> - Linux (systemd): `systemd-inhibit --what=idle ./scripts/tests.sh <target>`
> - Otherwise: `./scripts/tests.sh <target>`
>
> Below, `<RUN>` stands for the wrapper above for the current OS.

```bash
# macOS
caffeinate -i ./scripts/tests.sh all

# Linux (systemd)
systemd-inhibit --what=idle ./scripts/tests.sh all
```

Check the generated error logs in `artifacts/`:
- `artifacts/lint-errors.log`
- `artifacts/unit-errors.log`
- `artifacts/brain-errors.log`
- `artifacts/api-errors.log`
- `artifacts/build-errors.log`
- `artifacts/integration-errors.log`

**If the command exits 0 and all error logs are empty (`0` bytes):**

```text
✅ ALL TESTS PASSED

Target: all
Attempts: 0 / 5
Result: ✅ Green — no fixes needed

Fixes applied: None
Remaining failures: None
```

Stop here. Do not continue.

---

## Step 2 — Triage each failure

For each failing target, inspect its error log before editing any code and classify the failure:

### Infrastructure failure → STOP IMMEDIATELY (do not retry, do not consume TTL)

Examples:
- Docker daemon not running or socket permission denied (`Operation not permitted`)
- `docker compose` missing
- Host port already in use (`8000`, `8080`, `9000`, `9001`)
- Missing `ANTHROPIC_API_KEY` / `MODEL_*` when explicitly running `./scripts/tests.sh live`

Write a blocker entry to `scripts/memory.md` and report:

```text
🚫 BLOCKED — infrastructure issue, cannot proceed

Failure: <exact error>
Required action: <what the human must do>
```

### Test / code failure → proceed to Step 3

Examples: failing Go assertions, `go vet` errors, `brain pack validate` schema/template errors, race conditions (`-race`), container startup errors in `whatshelper-brain` / `store-init`, DynamoDB/MinIO contract mismatches.

---

## Step 3 — Retry loop (TTL = 5 per failing target)

> **State Reset Rule:** When retrying `integration` (or `e2e` once implemented) after stale container state causes false failures, run `<RUN> ./scripts/tests.sh rm-db && <RUN> ./scripts/tests.sh <target>`.

```text
ATTEMPT = 1

while ATTEMPT <= 5:
  1. Read scripts/memory.md (if it exists) — do not repeat a fix that already failed for this error.
  2. Apply a minimal, focused fix (keep internal/ strictly domain-agnostic; validate packs if pack.yaml or *.tmpl changed).
  3. Run <RUN> ./scripts/tests.sh <failing-target>   # lint | unit | brain | api | build | integration
  4. Read artifacts/<failing-target>-errors.log
  5. Append an entry to scripts/memory.md.
  6. If exit code == 0 → ✅ target green. Move to next failing target.
  7. ATTEMPT++

If ATTEMPT > 5 → STOP. Write a blocker entry to scripts/memory.md and request human intervention.
```

Once every previously failing target is green, finish with one clean `<RUN> ./scripts/tests.sh all` verification.

---

## Reading logs — priority order

1. `artifacts/<target>-errors.log` — filtered concise failure summary.
2. `artifacts/full-<target>.log` or `artifacts/clean-<target>.log` — full Go test/build output without ANSI codes.
3. `artifacts/startup-docker.log` (for `build`) or `./scripts/tests.sh docker-errors` — container runtime diagnostics.

---

## Fix Scope & Project Guardrails

- Fix only the failing behavior or broken test; avoid unrelated refactors.
- Never add market/vertical-specific terms (`imóvel`, `corretor`, `visita`, `broker`, `property`, `bedrooms`, etc.) inside `internal/` (`TestCoreAgnosticism_NoVerticalTermsInInternal` enforces this).
- Never silence errors with `scripts/exclude.txt` unless the log line is genuinely benign noise.

---

## `scripts/memory.md` Entry Format

```markdown
### [<YYYY-MM-DD HH:MM>] target=<target> attempt=<N>/5 status=<fixed|failed|blocked>
- **Error:** `<short summary or file:line>`
- **Cause:** <root cause identified>
- **Change:** <file(s) edited or command run>
```
