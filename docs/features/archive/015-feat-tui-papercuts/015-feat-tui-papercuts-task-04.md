---
status: planned
depends_on: ["02", "03"]
wave: 2
skills: [code-writing]
verify: bash — `make test`
reviewers: [dev-code-reviewer, dev-test-reviewer]
teammate_name:
---

# Task 04: Auto-scroll the column window to the sort column

## Required Skills

Перед выполнением задачи загрузи:
- `/skill:code-writing` — [skills/code-writing/SKILL.md](~/.claude/skills/code-writing/SKILL.md)

## Description

Since feature [009] introduced horizontal scrolling, the sort column (moved with `←`/`→`, stored as
`config.view.OrderKey`) and the visible column window (moved with `[`/`]`, stored as
`config.scrollOffset`) are fully independent. Sorting by a column that lies outside the visible window
therefore produces **no visible effect at all**: the data is re-sorted, but the column the user just
selected is off-screen, so nothing on the terminal changes.

This task closes that gap with a **one-shot request**: the sort handlers (`orderKeyLeft`,
`orderKeyRight`) raise a flag on `config`; `renderDbstat` consumes and clears it on the next frame,
and if the sort column is outside the current window it moves the offset by the **minimum** needed to
bring the column into view. Because the flag is consumed exactly once, manual `[`/`]` scrolling
afterwards is never undone by the next refresh — the [009] invariant that manual scroll persists
across ticks stays intact.

The offset is computed at render time rather than in the key handler because column widths are only
known after `alignViewToResult` has run against real data (`printDbstat`, `top/stat.go:658`). The
minimum offset is found by **probing the existing `visibleColumns`**, never by reimplementing its
marker-reservation arithmetic — ADR `[009] Partial last column + marker reservation in both walk
directions` records that logic as subtle enough to have shipped a bug invisible to unit tests
(Decision 4 of the tech-spec).

The flag lives on `top.config`, not on `view.View`: per ADR `[009] Scroll offset on top.config, not on
view.View`, per-view state is deliberately persisted by `viewSwitchHandler` into `config.views` and
would survive screen switches, which is wrong for ephemeral UI state. For the same reason the flag is
cleared on **both** screen-switch paths (Decision 8) — a pending request left across a switch would be
consumed by the first render of the *new* screen and scroll it to a sort column the user never chose
there.

## What to do

1. Add the `autoScrollToOrderKey bool` field to `config` in `top/config.go`, next to `scrollOffset` /
   `verbose`, with a comment stating it is a one-shot request set by the sort handlers, consumed by
   `renderDbstat`, and ephemeral like `scrollOffset` (reset on both view-switch paths).

2. In `top/config_view.go`, set the flag in both sort handlers — `orderKeyLeft` and `orderKeyRight` —
   **after** the wrap-around adjustment and **before** the `config.viewCh <- config.view` send. The
   handlers must not touch `config.scrollOffset`.

3. In `top/config_view.go`, clear the flag on both switch paths, each on the line following the
   existing `scrollOffset = 0` reset:
   - `viewSwitchHandler`;
   - `switchViewToProcPidStat` — **before** the `!app.db.Local` guard, so it stays observable in tests
     without a live PostgreSQL (the `Test_switchViewToProcPidStatResetsScrollOffset` precedent).

4. Add the pure helper `scrollOffsetFor(ncols int, colsWidth map[int]int, termWidth, offset, orderKey int) int`
   to `top/stat.go`, next to `visibleColumns`. It returns the offset that brings `orderKey` into the
   visible window with the smallest movement, or the offset unchanged when the column is already
   visible. It must find the answer by calling `visibleColumns` — no independent window arithmetic.
   Requirements:
   - `orderKey <= 0` (frozen column 0, or a negative/unset key) → offset unchanged.
   - `orderKey >= ncols` → offset unchanged. Bounds are checked against the **fresh result's** column
     count, never `config.view.Ncols`: the two can disagree for one frame after a view switch
     (issue #99 class).
   - Column already inside `[win.first, win.last]`, including the partially visible last column per
     [009] `countFit` semantics → return `win.clamped`, no jerk.
   - Column to the left of the window → the offset that puts it exactly at the left edge.
   - Column to the right → walk offsets rightwards from the current clamped offset and take the first
     one whose window admits the column.

5. In `renderDbstat` (`top/stat.go`), consume the flag in a block inserted **between the function
   opening and the existing `visibleColumns` call**: if the flag is set, clear it first, then assign
   `scrollOffsetFor(...)` to `config.scrollOffset`. The existing `visibleColumns` call and the
   `config.scrollOffset = win.clamped` write-back must remain **byte-identical** — `visibleColumns`
   re-clamps whatever the helper produced, so the [009] clamp stays the single source of truth.

6. Update `Test_scrollOrthogonalToSort` (`top/config_view_test.go:145-193`): its doc comment claiming
   scroll and sort are unrelated is now false — sorting is only *deferredly* orthogonal, the next
   render moves the window. Rewrite the comment to state the new contract and add
   `assert.True(t, config.autoScrollToOrderKey)` inside the `sort-keeps-scroll-*` subtests. The
   existing `assert.Equal(t, 3, config.scrollOffset)` must keep passing unchanged.

7. Add the new unit tests listed in the TDD Anchor below.

## TDD Anchor

Тесты, которые нужно написать ДО реализации. Пишем → запускаем → убеждаемся что падают → пишем код → убеждаемся что проходят.

- `top/stat_test.go::Test_scrollOffsetFor/already_visible` — sort column inside the current window →
  the returned offset equals `visibleColumns(...).clamped`, i.e. no movement.
- `top/stat_test.go::Test_scrollOffsetFor/partially_visible_counts_as_visible` — a column whose start
  is inside the budget but whose tail is truncated → treated as visible, offset unchanged.
- `top/stat_test.go::Test_scrollOffsetFor/column_to_the_right` — column past `win.last` → the returned
  offset is the **smallest** one whose window admits the column (assert both: the column is inside the
  window at the returned offset, and it is not inside the window at `returned-1`).
- `top/stat_test.go::Test_scrollOffsetFor/column_to_the_left` — column before `win.first` → the column
  lands at the left edge of the window at the returned offset.
- `top/stat_test.go::Test_scrollOffsetFor/frozen_column_zero` — `orderKey == 0` → offset returned
  unchanged (column 0 is always visible; it must never scroll).
- `top/stat_test.go::Test_scrollOffsetFor/orderKey_out_of_range` — `orderKey >= ncols` and a negative
  `orderKey` → offset returned unchanged, no panic.
- `top/stat_test.go::Test_scrollOffsetFor/empty_result` — `Ncols` from headers with zero data rows →
  widths are known from the aligned view, the offset is still computable, no panic.
- `top/stat_test.go::Test_renderDbstat_autoScrollConsumesFlag` — with the flag set and `OrderKey`
  pointing at an off-window column, one `renderDbstat` call moves `config.scrollOffset` so the sort
  column's header is present in the output, and leaves the flag `false`.
- `top/stat_test.go::Test_renderDbstat_autoScrollIsOneShot` — after the consuming render, a manual
  offset change followed by a second `renderDbstat` leaves the manual offset in place (clamped only),
  proving the request does not re-fire.
- `top/config_view_test.go::Test_orderKeySetsAutoScrollFlag` — `orderKeyLeft` and `orderKeyRight` each
  set `config.autoScrollToOrderKey` to `true` and leave `config.scrollOffset` untouched.
- `top/config_view_test.go::Test_viewSwitchResetsAutoScrollFlag` — `viewSwitchHandler` clears the flag.
- `top/config_view_test.go::Test_switchViewToProcPidStatResetsAutoScrollFlag` — the per-process path
  clears the flag with `db.Local == false` (no live PostgreSQL), proving the reset sits before the
  guard.
- `top/config_view_test.go::Test_scrollOrthogonalToSort` — existing test: `sort-keeps-scroll-*` keeps
  asserting `scrollOffset == 3` **and** now asserts the flag is set.

## Acceptance Criteria

- [ ] `scrollOffsetFor` has a defined answer when **no offset can admit the sort column** — the
      window is empty because the terminal is too narrow for the frozen column plus any
      scrollable one. It must return the current offset unchanged (leave the window alone)
      rather than loop, panic or land on an arbitrary edge. A unit case pins this: `termWidth`
      smaller than the frozen column's own width.


- [ ] `config.autoScrollToOrderKey` exists on `top.config` (not on `view.View`) and is documented as a
      one-shot, ephemeral request.
- [ ] `orderKeyLeft` and `orderKeyRight` set the flag and still do not write `config.scrollOffset`.
- [ ] `scrollOffsetFor` is a pure function, computes the minimum offset by probing `visibleColumns`,
      and contains no independent marker-reservation arithmetic.
- [ ] A sort column already visible — including partially visible — leaves the offset unmoved.
- [ ] `orderKey` is bounds-checked against the fresh result's column count; out-of-range and column 0
      return the offset unchanged without panicking.
- [ ] `renderDbstat` clears the flag before recomputing, so the request fires exactly once and manual
      `[`/`]` scrolling afterwards is never undone by the next refresh.
- [ ] The existing `visibleColumns` call and the `config.scrollOffset = win.clamped` write-back in
      `renderDbstat` are byte-identical to before.
- [ ] The flag is cleared in `viewSwitchHandler` and, separately, in `switchViewToProcPidStat` before
      the `db.Local` guard.
- [ ] `Test_scrollOrthogonalToSort` passes, with its now-false comment corrected and an assertion on
      the new flag added.
- [ ] All existing `renderDbstat` / `printStatHeader` / `printStatData` / `visibleColumns` tests pass
      unchanged.
- [ ] `make test` green with the race detector; `make lint` clean.

## Context Files

**Feature artifacts:**
- [015-feat-tui-papercuts.md](docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts.md) — user-spec
- [015-feat-tui-papercuts-tech-spec.md](docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-tech-spec.md) — tech-spec (Decisions 4, 5, 8; Task 4 in Wave 2)
- [015-feat-tui-papercuts-decisions.md](docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-decisions.md) — decisions log (created on completion)
- [015-feat-tui-papercuts-code-research.md](docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-code-research.md) — **§13** has the exact insertion point, the helper sketch, both reset sites and the `Test_scrollOrthogonalToSort` analysis

**Project knowledge:**
- [overview.md](.claude/skills/project-knowledge/overview.md) — what pgcenter is, target audience
- [architecture.md](.claude/skills/project-knowledge/architecture.md) — package layout, `top/` data flow, render path
- [patterns.md](.claude/skills/project-knowledge/patterns.md) — "Testable TUI Rendering — pure window function + io.Writer printers", "Sorting", "Naming Conventions", "Linting"
- [docs/decisions-log.md](docs/decisions-log.md) — ADRs `[009] Scroll offset on top.config, not on view.View`, `[009] Reset offset on both view-switch paths`, `[009] Manual column window, not gocui viewport scroll`

**Code files:**
- [top/config.go](top/config.go) — add the `autoScrollToOrderKey` field to `config`
- [top/config_view.go](top/config_view.go) — set the flag in `orderKeyLeft` / `orderKeyRight`; clear it in `viewSwitchHandler` and `switchViewToProcPidStat`
- [top/stat.go](top/stat.go) — add `scrollOffsetFor`; consume the flag in `renderDbstat`
- [top/config_view_test.go](top/config_view_test.go) — amend `Test_scrollOrthogonalToSort`, add flag set/reset tests
- [top/stat_test.go](top/stat_test.go) — add `Test_scrollOffsetFor` and the `renderDbstat` auto-scroll tests

## Verification Steps

- `make test` — full suite green with the race detector. Specifically: all `Test_scrollOffsetFor`
  subtests including the empty-result case; the flag cleared on both switch paths; the two
  `renderDbstat` auto-scroll tests; `Test_scrollOrthogonalToSort` still green.
- `make lint` — golangci-lint + gosec clean; no unused helper, no shadowed loop variable in the probe
  walk.
- `go test ./top/... -run 'Test_visibleColumns|Test_printDbstat|Test_printStatHeader|Test_printStatData|Test_render_' -count=1`
  — the pre-existing render suite must be untouched by this change.
- Visual confirmation of the behaviour on a narrow terminal is **not** part of this task — it is
  covered by Task 8 (stand verification run).

## Details

**Files:**

- `top/config.go` — the `config` struct currently ends at `scrollOffset int` (`:19`) and `verbose bool`
  (`:20`). Add `autoScrollToOrderKey bool` after them. `newConfig()` needs no change: the zero value
  `false` is correct, and every test that builds a bare `&config{...}` (e.g. `makeRenderConfig`,
  `top/stat_test.go:830`) inherits it.

- `top/config_view.go`:
  - `orderKeyLeft` (`:22-32`) and `orderKeyRight` (`:35-45`) — each decrements/increments
    `config.view.OrderKey`, wraps it, then sends on `config.viewCh`. The flag write goes between the
    wrap-around block and the send.
  - `viewSwitchHandler` (`:240-245`) — stores the outgoing view into `config.views`, loads the new
    one, then `config.scrollOffset = 0`. The flag reset goes on the next line.
  - `switchViewToProcPidStat` (`:253-...`) — `app.config.scrollOffset = 0` sits at `:257`, and the
    `if !app.db.Local` guard at `:259` returns early. The flag reset must go between them; everything
    after the guard calls `app.db.QueryRow`, which panics on a nil connection in unit tests.

- `top/stat.go`:
  - `renderDbstat` (`:671-693`) — the consuming block goes between the function opening and the
    `win := visibleColumns(...)` line. Do not reorder, rewrite or re-indent that line or the
    `config.scrollOffset = win.clamped` line below it: their exactness is an explicit acceptance
    criterion, and the clamp is what makes an over-optimistic helper result harmless.
  - `scrollOffsetFor` — put it next to `visibleColumns` (`:751`) so the two read together. The
    signature is the plain-typed `(ncols int, colsWidth map[int]int, termWidth, offset, orderKey int) int`;
    it takes no `*config` and no `stat.Stat`, which is what makes it unit-testable without gocui — the
    same shape as `visibleColumns` and `firstTickHint` (`:149`). Note that `columnWindow` exposes
    `first`, `last`, `clamped`, `hiddenLeft`, `hiddenRight`; `first > last` means an empty window (only
    the frozen column fits), so the "already visible" comparison must not accidentally treat that as a
    match.

- `top/config_view_test.go` — the existing `Test_scrollOrthogonalToSort` (`:145`) drains `viewCh` in a
  goroutine and closes it, because the handlers block on an unbuffered send. Any new test that calls a
  sort handler needs the same pattern. `Test_viewSwitchResetsScrollOffset` (`:194`) and
  `Test_switchViewToProcPidStatResetsScrollOffset` (`:213`) are the templates for the two reset tests —
  the latter builds `&app{config: newConfig(), db: &postgres.DB{Local: false}}` and asserts after an
  early return.

- `top/stat_test.go` — reuse the existing helpers rather than inventing new fixtures:
  `makeRenderConfig(ncols, width)` (`:830`) builds an already-aligned `*config` with `col0..colN-1`
  names and uniform widths, so a printed header can be matched back to an absolute column index;
  `makeRenderResult(ncols, nrows)` (`:849`) builds the matching `stat.Stat` with `rR-cC` cell values.
  `Test_visibleColumns` (`:599`) defines a local `uniformWidths(ncols, width)` helper — the same
  pattern suits `Test_scrollOffsetFor`, which needs only a widths map, not a full config.

**Dependencies:**
- Task 02 (bold in verbose sections) also edits `top/stat.go` and `top/stat_test.go`; Task 03 (clear
  filters) also edits `top/config_view.go` and `top/config_view_test.go`. Both are in Wave 1 and must
  be merged before this task starts — the waves are partitioned so no two concurrent tasks share a
  file. Start from the current tree state; re-locate the anchors above by name, since their line
  numbers may have shifted.
- No new packages. Everything used here is already imported by the target files.

**Edge cases:**
- `orderKey == 0` — column 0 is frozen and always printed (`printStatHeader`), so it must never cause
  a scroll.
- `orderKey` out of range against the fresh result — after a view switch `config.view.Ncols` and
  `s.Result.Ncols` can disagree for one frame. Clamp against the result's count.
- Empty result set (every row filtered out) — `Ncols` and `ColsWidth` still come from the aligned
  headers, so the offset stays computable; assert no panic.
- `ncols <= 1` — `visibleColumns` returns `{first: 1, last: 0}` (empty window). The helper must return
  the offset unchanged, not loop.
- Partially visible column — visible per [009] `countFit` semantics; do not treat it as needing a
  scroll, or the window would fight `maxOffset` forever.
- An error frame: `printDbstat` returns early on `s.Error != nil` (`top/stat.go:648-655`) **without**
  reaching `renderDbstat`, so a pending flag survives the error frame and fires on the next good
  frame. That is the desired behaviour — state it in a comment, do not "fix" it.

**Implementation hints:**
- This state is single-goroutine: the writer is the key handler (gocui goroutine) and the reader is
  `renderDbstat` inside `printStat`'s `g.Update` closure (also gocui). Same argument as
  `config.scrollOffset` and `config.verbose` — no synchronisation is needed, and none should be added.
- Clear the flag **before** computing the new offset, not after — that ordering is what makes the
  request strictly one-shot even if the computation returns early.
- The rightward probe is bounded by `ncols-1`; `visibleColumns` re-clamps anyway, so the loop needs no
  cleverness. `Ncols` is at most ~30 on the widest screen, so the probe cost is irrelevant.
- Comment the *why* on the consuming block: why at render time (widths known only after
  `alignViewToResult`) and why cleared first (one-shot ⇒ manual scroll survives). The surrounding code
  in `top/stat.go` documents reasoning at this density; match it.
- `docs/features-catalog.md:189` currently records "no auto-scroll to the sort column" as expected
  [009] behaviour. This task makes that false, but the correction is made at feature finalization
  (`/done`), **not** here.

## Reviewers

- **dev-code-reviewer** → `docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-task-04-dev-code-reviewer-review.json`
- **dev-test-reviewer** → `docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-task-04-dev-test-reviewer-review.json`

`dev-security-auditor` is deliberately absent — the tech-spec's Implementation Tasks section excludes
it for every task in this feature. Do not restore it.

## Post-completion

- [ ] Записать краткий отчёт в [015-feat-tui-papercuts-decisions.md](docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-decisions.md) (Summary: 1-3 предложения, ревью со ссылками на JSON, без таблиц файндингов и дампов)
- [ ] Если отклонились от спека — описать отклонение и причину
- [ ] Обновить user-spec/tech-spec если что-то изменилось
