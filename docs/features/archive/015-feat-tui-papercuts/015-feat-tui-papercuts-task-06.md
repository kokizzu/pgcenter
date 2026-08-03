---
status: planned
depends_on: ["04", "05"]
wave: 3
skills: [code-writing]
verify: bash
reviewers: [dev-code-reviewer, dev-test-reviewer]
teammate_name:
---

# Task 06: Show the refresh interval in the header

## Required Skills

Перед выполнением задачи загрузи:
- `/skill:code-writing` — [skills/code-writing/SKILL.md](~/.claude/skills/code-writing/SKILL.md)

## Description

The refresh interval set through the `z` hotkey is invisible once the dialog closes: `changeRefresh`
returns `"Refresh: ok"`, the cmdline message self-erases after two seconds, and nothing on screen
tells the user what the interval currently is. The user-spec asks for it on the **first header line
of the `sysstat` panel, right after the clock**:

```
pgcenter: 2026-08-02 17:43:27, refresh: 5s, load average: 0.15, 0.10, 0.09
```

The clock is already on that line; only the interval is missing. The header must keep its current
row count — 4 lines compact, 7 lines verbose — because the interval goes on an existing line and
adds no row.

The value cannot be read back from `view.View.Refresh`. That field is a **transient courier for the
collector**, not per-view state: both writers (`top/ui.go` in `doWork` and `changeRefresh` in
`top/config_view.go`) set it, send the view on `viewCh`, and zero it on the very next line. The
zeroing is load-bearing — `collectStat` (`top/stat.go:86-96`) treats any non-zero `Refresh` on an
incoming view as "the interval changed" and `continue`s **before** the ShowExtra / Verbose /
CollectExtra branches. Keeping `Refresh` populated so the renderer could read it would make every
later view update look like a refresh change and silently skip those branches. At rest
`config.view.Refresh == 0` always.

So this task adds a durable display copy on `config`, seeds it from a single shared default constant
before any goroutine starts, keeps it in sync in `changeRefresh`, and threads it into the sysstat
renderer.

## What to do

1. Add a `refresh time.Duration` field to `config` (`top/config.go`) with a comment stating why
   `view.View.Refresh` cannot be read back. Add the `defaultRefresh` constant in the same file — one
   shared literal for the initial interval.
2. Seed `app.config.refresh = defaultRefresh` in `app.setup()` (`top/top.go`), right after the
   default view is chosen. This runs from `RunMain` before `mainLoop` starts any goroutine, so it is
   race-free.
3. Make `doWork` (`top/ui.go`) send `defaultRefresh` instead of its own `time.Second` literal, so the
   value shown in the header and the value the collector is seeded with cannot drift apart. Do **not**
   initialise `config.refresh` there and do **not** make that line read `config.refresh` — `doWork`
   runs in a different goroutine from every reader and writer of the field.
4. In `changeRefresh` (`top/config_view.go`), write the durable copy inside the success branch,
   alongside the existing `config.view.Refresh` assignment. The existing three lines — set courier,
   send on `viewCh`, zero courier — stay exactly as they are.
5. Thread the interval into the renderer: `printSysstat` and `renderSysstat` (`top/stat.go`) gain a
   `refresh time.Duration` parameter; the single production call site in `printStat` passes
   `app.config.refresh`.
6. Render the interval on line 1 of `renderSysstat`, between the timestamp and the load average,
   formatted **explicitly in seconds** (`%ds` over `int(refresh/time.Second)`), never via
   `Duration.String()`.
7. Update the four test call sites of `renderSysstat` (plus the production call in `printSysstat`) and the line-1 regex in
   `Test_renderSysstat_compact`; add an assertion on the new field to `Test_changeRefresh`.

## TDD Anchor

Tests to write BEFORE the implementation. Write → run → see them fail → implement → see them pass.

- `top/stat_test.go::Test_renderSysstat_compact` — line 1 matches
  `^pgcenter: \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}, refresh: \d+s, load average: 1\.23, 0\.45, 6\.78$`
  (the existing regex, tightened — not relaxed to a prefix match), and the compact output is still
  exactly 4 lines.
- `top/stat_test.go::Test_renderSysstat_refreshFormat` (new) — the `%ds` form at the three boundary
  values: `1*time.Second` → `refresh: 1s`, `60*time.Second` → `refresh: 60s`,
  `300*time.Second` → `refresh: 300s`. This is the test that fails if anyone reaches for
  `Duration.String()` (`"1m0s"`, `"5m0s"`).
- `top/stat_test.go::Test_renderSysstat_compactUnchanged` — line counts unchanged with the new
  parameter: 4 compact, 7 verbose, and the first 4 verbose lines byte-identical to the compact ones.
- `top/config_view_test.go::Test_changeRefresh` — valid case (`"5"`): in addition to the existing
  return-string and `viewCh` assertions, `config.refresh == 5*time.Second`.
- `top/config_view_test.go::Test_changeRefresh` — invalid inputs (`""`, `"a"`, `"0.5"`, `"-1"`,
  `"0"`, `"301"`) leave `config.refresh` untouched: the durable copy is written only on the success
  path.
- `top/top_test.go::Test_app_setup` — **the seeded default**, required by the tech-spec's verify row
  for this task ("`refresh: <N>s` at 1/60/300s **and the seeded default before any `z`**"). After
  `app.setup()` returns, `app.config.refresh == defaultRefresh`. Without this the header would read
  `refresh: 0s` until the user first presses `z`, and nothing else in the suite would catch it — the
  renderer tests pass an interval in explicitly. The test already exists, already opens a test
  connection and already calls `app.setup()`, so this is one assertion added to it.
  Note `top/top_test.go` belongs to no other task in wave 3 — task 07 touches only `top/dialog.go`
  and `top/dialog_test.go` — so this creates no write collision.

## Acceptance Criteria

- [ ] The first `sysstat` line reads `pgcenter: <date time>, refresh: <N>s, load average: ...`
- [ ] The interval is formatted as whole seconds — `60s`, `300s`, never `1m0s` / `5m0s`
- [ ] Before any `z` press the header shows the seeded default (`refresh: 1s`), not `refresh: 0s`
- [ ] Pressing `z` and entering a value updates the header on the next refresh
- [ ] An invalid `z` input leaves the displayed value unchanged
- [ ] Header row count unchanged: 4 lines compact, 7 lines verbose
- [ ] `config.view.Refresh` is still zeroed immediately after every `viewCh` send — both in `doWork`
      and in `changeRefresh`
- [ ] One shared `defaultRefresh` constant is the only literal for the initial interval; the
      collector seed and the UI seed both use it
- [ ] `make test` (with `-race`) and `make lint` pass

## Context Files

**Feature artifacts:**
- [015-feat-tui-papercuts.md](docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts.md) — user-spec (item 4, "Интервал обновления в шапке", scenario 3, line-width arithmetic)
- [015-feat-tui-papercuts-tech-spec.md](docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-tech-spec.md) — tech-spec: **Decision 7**, Data Models, Task 6
- [015-feat-tui-papercuts-code-research.md](docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-code-research.md) — **§14** — the exact change set, line numbers and full list of test call sites
- [015-feat-tui-papercuts-decisions.md](docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-decisions.md) — decisions log

**Project knowledge:**
- [overview.md](.claude/skills/project-knowledge/overview.md) — what pgcenter is, supported stats
- [architecture.md](.claude/skills/project-knowledge/architecture.md) — package layout, data flow, goroutine boundaries
- [patterns.md](.claude/skills/project-knowledge/patterns.md) — "Testable TUI Rendering — pure window function + io.Writer printers", Testing conventions, Linting

**Code files:**
- [top/config.go](top/config.go) — add the `refresh` field and the `defaultRefresh` constant; needs the `time` import
- [top/top.go](top/top.go) — seed `app.config.refresh` in `app.setup()`; needs the `time` import if `defaultRefresh` is referenced with a duration expression
- [top/ui.go](top/ui.go) — `doWork` sends `defaultRefresh` instead of its own `time.Second` literal
- [top/config_view.go](top/config_view.go) — `changeRefresh` keeps the durable copy in sync
- [top/stat.go](top/stat.go) — `printSysstat` / `renderSysstat` signatures and line 1; `printStat` call site
- [top/stat_test.go](../../../top/stat_test.go) — 4 `renderSysstat` call sites (`:59`, `:150`, `:284`, `:285`), the line-1 regex, the new format test
- [top/config_view_test.go](top/config_view_test.go) — `Test_changeRefresh` assertions on the new field

## Verification Steps

- Write the TDD anchors first, run `make test`, confirm they fail for the right reason (compile error
  on the new parameter / regex mismatch / zero value), not by accident.
- Implement, then run `make test` — the whole `top/` package must be green, including every existing
  line-count assertion (`stat_test.go` 4-line and 7-line checks) and `Test_changeRefresh`.
- Run `make lint` — no new findings.
- Grep check: `grep -rn "time.Second" top/ui.go top/top.go` shows no leftover hardcoded default
  interval; `grep -n "refresh.String()" top/` is empty.
- Manual check is deferred to Task 8 (stand run) — the header line is captured there with
  `tmux capture-pane -p` after pressing `z`.

## Details

**Files:**

- `top/config.go` — the `config` struct currently ends with `scrollOffset` and `verbose` (plus
  `autoScrollToOrderKey` added by Task 4, which lands in the previous wave). Append
  `refresh time.Duration`, commented with *why* it exists: `view.Refresh` is zeroed right after the
  `viewCh` send in `ui.go` and `config_view.go`, so it cannot be read back. Add
  `const defaultRefresh = time.Second` in the same file. The file has no `time` import today — add it.
  `newConfig()` deliberately stays as it is: it is called from tests and the seed belongs in
  `app.setup()` (see below); do not seed it inside `newConfig()`.

- `top/top.go` — in `app.setup()`, right after `app.config.view = app.config.views["activity"]`, add
  `app.config.refresh = defaultRefresh`. `setup()` is called from `RunMain` before `mainLoop`, i.e.
  before any goroutine exists, so this write is definitively race-free.

- `top/ui.go` — `doWork` currently does `app.config.view.Refresh = time.Second`, sends the view, then
  zeroes the field. Replace only the literal with `defaultRefresh`. **Do not** touch the zeroing line
  and **do not** read `app.config.refresh` here: `doWork` is a separate goroutine, `config.refresh` is
  written by `changeRefresh` on the gocui goroutine, and reading it here would create a real
  `-race` finding (research §7 notes a narrow pre-existing race of exactly this shape at these lines —
  do not widen it).

- `top/config_view.go` — `changeRefresh` validates the input (1..300) and then does three things:
  set `config.view.Refresh`, send on `config.viewCh`, zero `config.view.Refresh`. Add the durable
  copy **inside that success branch** — after validation, so an invalid input never changes what is
  displayed. All three existing lines stay byte-identical; the zeroing especially.

- `top/stat.go`:
  - `printSysstat(v *gocui.View, s stat.Stat, verbose bool, local bool, dataDir string)` and
    `renderSysstat(w io.Writer, s stat.Stat, verbose bool, local bool, dataDir string)` both gain a
    trailing `refresh time.Duration` parameter. `printSysstat` is a thin wrapper — pass it through.
  - the only production call site is inside `printStat`'s `g.Update` closure; pass
    `app.config.refresh`. Both writer and reader of the field are on the gocui goroutine
    (`changeRefresh` is reached from `dialogFinish`, a key handler), so no cross-goroutine access is
    introduced.
  - line 1 becomes `"pgcenter: %s, refresh: %ds, load average: %.2f, %.2f, %.2f\n"` with
    `int(refresh/time.Second)` as the second argument. Nothing else on the line moves.
  - `renderSysstat` has no test file changes beyond the call sites — the verbose rows are untouched.

- `top/stat_test.go` — 4 literal `renderSysstat(` call sites need the new argument (`:59`, `:150`, `:284`, `:285`; the fifth production call lives in `printSysstat`, `top/stat.go:259`) (one in
  `Test_renderSysstat_compact`, one in the `verboseSysstatLines` helper which serves five verbose
  tests, two in `Test_renderSysstat_compactUnchanged`); plus the new format test. Also update the
  line-1 regex in `Test_renderSysstat_compact` — tighten it to include `refresh: \d+s`, do not turn
  it into a prefix match. Every line-count assertion stays as is and must stay green.

- `top/config_view_test.go` — `Test_changeRefresh` passes verbatim today; add
  `assert.Equal(t, 5*time.Second, config.refresh)` to the valid subtest and an untouched-value
  assertion to the invalid subtest.

**Dependencies:**
- Task 04 (wave 2) also edits `top/config.go`, `top/config_view.go`, `top/stat.go`,
  `top/stat_test.go`, `top/config_view_test.go`; Task 05 (wave 2) edits `top/ui.go` and `top/top.go`.
  Both land before this task starts — re-read every file before editing, the line numbers quoted in
  code-research §14 are pre-Task-04/05 and will have shifted.
- No new packages. `time` is stdlib and already imported in `top/stat.go`, `top/ui.go` and
  `top/config_view.go`; it must be added to `top/config.go` and to `top/top.go` if referenced there.

**Edge cases:**
- `refresh == 0` (a bare `newConfig()` in a unit test that never went through `app.setup()`) renders
  `refresh: 0s`. That is acceptable in tests; production always goes through `setup()`. Do not add a
  fallback branch in the renderer for it — the seed is the fix.
- 60 and 300 seconds are the values `Duration.String()` gets wrong; they are pinned by the format test.
- 300 is the validated maximum, so `%d` never needs more than three digits: line 1 grows from 61 to
  at most 76 characters, still shorter than the `%cpu` line (80), so the panel's truncation threshold
  does not move.
- Invalid `z` input must leave both `config.view.Refresh` and `config.refresh` untouched.

**Implementation hints:**
- The seed constant is the whole point of Decision 7: two independent literals for the default would
  drift apart silently, and the drift would only ever surface as a wrong number in the header.
- Keep the `int(refresh/time.Second)` conversion in the renderer, not at the call site — one
  conversion, one place.
- Do not extend `view.View`: per ADR [009], ephemeral view-independent UI state belongs on `config`,
  otherwise it inherits the per-view persistence `viewSwitchHandler` provides.
- The header is rendered through the writer-based `renderSysstat` core precisely so this is testable
  without a terminal — follow that existing pattern, no gocui needed in any new test.

## Reviewers

- **dev-code-reviewer** → `docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-task-06-dev-code-reviewer-review.json`
- **dev-test-reviewer** → `docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-task-06-dev-test-reviewer-review.json`

## Post-completion

- [ ] Записать краткий отчёт в [015-feat-tui-papercuts-decisions.md](docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-decisions.md) (Summary: 1-3 предложения, ревью со ссылками на JSON, без таблиц файндингов и дампов)
- [ ] Если отклонились от спека — описать отклонение и причину
- [ ] Обновить user-spec/tech-spec если что-то изменилось

`dev-security-auditor` is deliberately omitted per the tech-spec's Implementation Tasks preamble:
the feature has no network, auth, persistence or SQL surface. Do not restore it.
