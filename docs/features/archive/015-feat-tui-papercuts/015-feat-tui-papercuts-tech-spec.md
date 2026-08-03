---
created: 2026-08-02
status: approved
branch: feature/tui-papercuts
size: M
---

# Tech Spec: TUI papercuts — устранение фрикций интерактивного режима

## Solution

Seven independent UI fixes in the `top/` package, delivered through the pattern this project already
uses for untestable TUI code (ADR [009], [010]): extract every non-trivial decision into a **pure
function** with plain inputs, unit-test that function, and leave the gocui-facing code as thin
plumbing. Four such functions are new — `composeCmdline`, `filterToken`, `scrollOffsetFor`,
`dialogInputX0` — which is what makes items landing in `top/ui.go`, `top/keybindings.go`,
`top/dialog.go` and `top/help.go` testable at all: those four files have **no test files today**.

No SQL, no new views, no new columns. The `internal/` tree is untouched except for reads. Almost
everything runs in the gocui MainLoop goroutine and needs no synchronisation; the one exception is the
cmdline clear timer, which today calls `v.Clear()` from a bare goroutine — a pre-existing race. It is
**moved onto `g.Update`**, which removes the race, and gated on an atomic UI-generation counter so it
cannot outlive the `Gui` it was armed against (Decisions 10 and 14). That counter is the feature's
only synchronisation primitive.

## Architecture

### What we're building/modifying

- **`composeCmdline` + `filterToken` + `cmdlineTokens` (new, `top/ui.go`)** — the single composition
  point for the cmdline: a reserved token prefix followed by a transient message, clamped to the
  terminal width by a defined truncation ladder.
- **`printCmdline` / `printCmdlinePersist` / `writeCmdline` (`top/ui.go`)** — two writers over one
  core. `printCmdline` keeps its signature and its 2-second clear timer; `printCmdlinePersist` writes
  without arming the timer, for the dialog prompt.
- **`setCmdlineConfig` + package-level `cmdlineCfg` (new, `top/ui.go` + `top/top.go`)** — the ambient
  config the composer reads its state from, so none of the 44 `printCmdline` call sites change.
- **`scrollOffsetFor` (new, `top/stat.go`)** — computes the minimum scroll offset that brings the
  sort column into the visible window, by probing the existing `visibleColumns`.
- **`config.autoScrollToOrderKey` (new field, `top/config.go`)** — one-shot request set by the sort
  handlers, consumed at render time.
- **`config.refresh` (new field, `top/config.go`)** — the durable copy of the refresh interval,
  which `view.View.Refresh` cannot provide because it is zeroed immediately after being sent.
- **`clearFilters` (new handler, `top/config_view.go`)** — the `\` hotkey; `setFilter` gains one
  honest branch.
- **`dialogInputX0` (new, `top/dialog.go`)** — the dialog input field's x0, computed from the real
  cmdline length with space reserved for the field itself.
- **`topBandLayout` (`top/layout.go`)** — one-line arithmetic fix, plus it becomes the source of the
  dialog's y coordinates.
- **`renderSysstat` / `renderSysstatVerbose` / `renderPgstatVerbose` (`top/stat.go`)** — the refresh
  interval on line 1, and bold on verbose numeric values.

### How it works

**Cmdline composition.** Every write to the cmdline goes through `writeCmdline`, which builds the
line as `composeCmdline(cmdlineTokens(cmdlineCfg), msg, width)` **inside a `g.Update` closure** —
that is, always on the gocui goroutine, where `config.view.Filters` and `config.view.Cols` are also
exclusively written. The message text is formatted in the caller's goroutine (one call is reached from `doWork` via
`printStat`, `top/stat.go:166`), but no `config` field is touched there. The clear
timer no longer wipes the line: it re-renders the prefix-only line through its own `g.Update`.

**Filter indicator.** `filterToken` turns `view.Filters` plus the column names into
`[F:datname,usename]`, emitting names in ascending column-index order — Go randomises map iteration,
so unsorted keys would make the indicator flicker between frames. Indices outside the known column
list are skipped rather than indexed (issue #99 class: `Cols` is `nil` before the first render and is
per-screen, since `viewSwitchHandler` stores the whole view).

**Auto-scroll.** The sort handlers set a one-shot flag. `renderDbstat` consumes it immediately before
the existing `visibleColumns` call: if the flag is set and the sort column falls outside the current
window, `scrollOffsetFor` probes `visibleColumns` for the smallest offset that admits it, writes that
into `config.scrollOffset`, and clears the flag. The existing `visibleColumns` call and the
`config.scrollOffset = win.clamped` write-back stay byte-identical, so the [009] clamp remains the
single source of truth and the marker-reservation logic is never duplicated.

The flag is **cleared on both screen-switch paths**, alongside the existing `scrollOffset` reset —
see Decision 8. A request left pending across a switch would be consumed by the first render of the
*new* screen and scroll it to a sort column the user never chose there.

**Dialog geometry.** `dialogOpen` composes the cmdline line the same way the writer will, measures it
in **runes**, and derives x0 with `minDialogInputWidth` columns reserved for the field. The prompt is
passed to the composer with a width budget that already excludes that reservation, so an overlong
prompt is **truncated** rather than overlaid by the field. The y coordinates come from
`topBandLayout(app.config.verbose, maxY)` — the same inputs `layout()` uses in the same frame, so
the two cannot disagree.

## Decisions

### Decision 1: Ambient config for the cmdline composer, set through a named setter

**Decision:** A package-level `var cmdlineCfg *config` in `top/`, written exactly once by
`setCmdlineConfig(*config)` called from **`RunMain`** (`top/top.go:21`) before any goroutine exists,
and dereferenced **only inside `g.Update` closures** or key handlers. All 44 `printCmdline` call
sites keep their signature.

**Not from `newApp`:** `newApp` is also called by `top/top_test.go:14,22`, so setting the ambient
there would let one test leave the global pointing at its own config for every test that follows.
That is safe today only because nothing in `top/` calls `t.Parallel()` — an undocumented invariant
this feature must not start depending on. `RunMain` runs once per process and is not exercised by
unit tests.

**Rationale:** The prefix must appear on every cmdline write, and the writers are reached from 19
functions across 11 files, four of which (`showPgConfig`, `editPgConfig`, `showPgLog`, `resetStat`)
have no `config` in scope at all. Threading the config explicitly costs 44 call-site edits plus four signature
changes plus their keybinding registrations — a large mechanical diff across the same files three
waves are editing concurrently, for a dependency that is genuinely process-global in a single-app
TUI. The named setter keeps the write in one greppable place with the "before goroutines only"
constraint documented on it. The linter permits this: `.golangci.yml` enables `gocritic` and `revive`
(four rules) plus the v2 defaults; `gochecknoglobals` is not enabled, and `gosec` does not flag
globals.

**Alternatives considered:** Threading `*config` through `printCmdline` (44+ edits, cross-wave
conflict risk, four helpers needing new parameters) — rejected on diff size and merge risk, not on
principle. `sync.Once` (guards against a second write that cannot happen — `newApp` runs once) —
ceremony without benefit. Note this is a **new precedent for `top/`**, which has no package-level var
today; `internal/version` and `internal/stat/procpidstat.go:18` have package vars but they hold
immutable data, not mutable state. Record as an ADR at finalization.

### Decision 2: Tokens carry ordered variants, so the pause feature adds one without touching the composer

**Decision:** `type cmdlineToken struct{ variants []string }`, variants ordered longest to shortest.
`composeCmdline` degrades the **rightmost** token to a shorter variant only when the line does not
fit; a token with exactly one variant never shrinks.

**Rationale:** The user-spec requires the truncation ladder `[F:datname,usename]` →
`[F:datname,…]` → `[F:…]`, and requires the composer to accept the future `[PAUSED]` token without
being rewritten. One variant means "never truncate", which is exactly the property `[PAUSED]` needs,
expressed in data rather than in a special case.

**Alternatives considered:** A dedicated field per token with bespoke truncation logic — rejected, it
would need editing when feature [016] lands, which the AC explicitly forbids.

### Decision 3: Truncate the prompt, never overlay it with the input field

**Decision:** The dialog composes its line with a width budget of `maxX - minDialogInputWidth - 1`,
so the prompt itself is truncated when it does not fit; x0 then lands immediately after the
(possibly truncated) prompt. `minDialogInputWidth = 10`.

**Rationale:** Simply clamping x0 would keep the dialog on screen but slide the input box over the
prompt's tail, which contradicts the user-spec's alignment criterion. Truncating the prompt satisfies
both criteria at once: the field is always usable and always starts after the visible text. Ten
columns fit a PID, a refresh value, a `y/n` answer and a short regexp; longer input scrolls inside
gocui's editor. The clamp on x0 stays as a belt-and-braces guard: with the reserved budget it should
never bite, and a test asserts `x0 < x1` across the whole input domain so a future change to the
budget cannot silently reintroduce the UI teardown.

⚠ **This supersedes `...-code-research.md` §17.2/§17.3**, which compose with a budget of `maxX` and
therefore describe the overlay behaviour rejected here. The research is otherwise accurate; where the
two disagree on this point, the tech-spec governs.

**Alternatives considered:** Clamp x0 and accept the overlay (contradicts an approved AC); shorten
the prompt strings themselves (changes user-visible text unrelated to this feature).

### Decision 4: Auto-scroll probes `visibleColumns` instead of computing the offset directly

**Decision:** `scrollOffsetFor` walks candidate offsets and asks `visibleColumns` whether the sort
column is inside the returned window, taking the first that admits it.

**Rationale:** ADR [009] "Partial last column + marker reservation in both walk directions" records
that the window math is subtle enough to have shipped a bug invisible to unit tests. Recomputing the
admission rule independently would duplicate exactly that logic; probing reuses the single source of
truth. The function is pure and cheap (`Ncols` is bounded by the widest screen, 19 columns).

**Alternatives considered:** Direct backward-walk arithmetic — rejected as a second implementation of
the marker-reservation rule.

### Decision 5: Minimum movement — the window moves only when the sort column is outside it

**Decision:** If the sort column is already visible (including partially visible, per [009]
semantics), the offset is left untouched.

**Rationale:** Otherwise every `←`/`→` press would jerk the window even when the target is already on
screen.

**Alternatives considered:** Always centring the sort column — rejected, it would move the window on
presses that need no movement and would fight the user's manual scroll position for no gain.

### Decision 6: Filter names are ordered by column index

**Decision:** `filterToken` sorts filter keys ascending before rendering names.

**Rationale:** `view.Filters` is a map; Go randomises iteration order, so an unsorted indicator would
render `[F:usename,datname]` and `[F:datname,usename]` on alternating frames. Ascending index is also
left-to-right screen order, so it reads naturally.

**Alternatives considered:** Insertion order (a map does not preserve it, and tracking it means a
second data structure); alphabetical by name (diverges from screen order for no benefit).

### Decision 7: The refresh interval is formatted explicitly, not via `Duration.String()`

**Decision:** `fmt.Sprintf("%ds", int(d/time.Second))`. The default lives in one place — a
`defaultRefresh` constant — used both where the UI state is initialised and where the collector is
seeded, so the header never shows `refresh: 0s` before the first `z`.

**Rationale:** `time.Duration(300*time.Second).String()` is `"5m0s"` and `60s` is `"1m0s"` — neither
matches the user-spec's `refresh: 300s`, and the line-width arithmetic in the user-spec rests on the
`%ds` form. Two independent literals for the default would drift apart silently, and the drift would
only show as a wrong number in the header.

**Why a new field at all:** `view.View.Refresh` cannot hold this. Both writers set it, send it on
`viewCh` and zero it immediately, because it is a courier for the collector rather than per-view
state — and that zeroing is load-bearing: the collector's branch order treats a non-zero `Refresh` on
an incoming view as "the interval changed" and skips the extra-stats and verbose branches. Keeping it
populated so the renderer could read it would silently break those branches.

**Alternatives considered:** Reading `view.View.Refresh` (always zero at rest, and making it durable
breaks the collector — above); reading the collector's own local copy (lives in another goroutine);
`Duration.String()` (wrong format); formatting at the call site of each renderer (duplicates the
conversion).

### Decision 8: The auto-scroll request is cleared on both screen-switch paths

**Decision:** `config.autoScrollToOrderKey` is cleared wherever `config.scrollOffset` is reset today
— in `viewSwitchHandler` and, separately, in `switchViewToProcPidStat`, in the latter **before** the
`db.Local` guard.

**Rationale:** A request left pending across a switch is consumed by the first render of the new
screen, scrolling it to a sort column the user never chose there. This is the same two-path trap ADR
[009] records for the scroll offset: `switchViewToProcPidStat` does not delegate to
`viewSwitchHandler`, so a single reset misses it. Placing the reset before the `db.Local` guard is
required for testability — the code after the guard needs a live PostgreSQL.

**Alternatives considered:** Reset only in `viewSwitchHandler` (misses the per-process screen —
exactly the bug ADR [009] documents); making the flag harmless by re-validating the sort column at
render time (hides a stale request instead of clearing it, and costs a check on every frame).

### Decision 9: Bold covers numeric values only; the unit inside `RateUnitPrefixed` is bolded with them

**Decision:** In the verbose sections, wrap numeric values in `\033[37;1m…\033[0m` and leave every
degraded rendering plain. Concretely, the bold goes on the **value** branch and never on the
sentinel branch:

- `naInt` (`top/stat.go:519`) decides per call between a real number and `n/a` — the bold belongs
  **inside its value branch**, which is what lets the seven workload numbers be bolded without
  touching their seven call sites.
- The same value-or-sentinel pattern repeats in the `bgwr/ckpt` row through the `writeMs`, `syncMs`
  and `maxw` locals, and in the `databases`/`replication` rows through `size`, `growth`, `hit`,
  `lag`, `retain` and `backlog`: each is assigned either a formatted number or `naLiteral` /
  `naReserve`. Bold is applied where the number is produced, not to the variable afterwards.
- The bare `naLiteral` and `naReserve` renderings stay plain.
- The filesystem identifier fields (device, mountpoint, fstype) stay plain — they are not values.
- The four `pretty.RateUnitPrefixed` sites bold the unit together with the number; see below.

**Rationale:** Bold must read as "there is a real number here", which is why degraded fields stay
plain — and identifiers are not values in the base-row sense (base line 1 and the pgstat info line
bold nothing). `RateUnitPrefixed` returns value and unit as one string; splitting it means changing a
shared formatter and its tests for a cosmetic difference the user-spec does not require.

**Alternatives considered:** Bolding `n/a` too (loses the degraded-field signal); exporting a parts
variant of `RateUnitPrefixed` (disproportionate churn in `internal/pretty`).

### Decision 10: The clear timer moves onto `g.Update`

**Decision:** The 2-second timer's callback re-renders the prefix-only line inside a `g.Update`
closure instead of calling `v.Clear()` from a bare goroutine.

**Rationale:** The indicator must survive the timer, so the timer has to render rather than erase —
and rendering requires reading `config`. Doing that from a bare goroutine would create a real race;
doing it inside `g.Update` puts the read on the gocui goroutine like every other read. As a side
effect this **removes** the pre-existing race at `top/ui.go:227-230`, where `v.Clear()` mutates a view
buffer off the gocui goroutine.

**Alternatives considered:** Keeping `v.Clear()` and re-rendering the prefix on the next cmdline write
(the indicator would vanish for an unbounded stretch — until something else writes); having the timer
mutate the view buffer directly under a mutex (a lock guarding what is otherwise a single-goroutine
invariant, and it would still fight gocui's own rendering).

### Decision 11: Dialog y coordinates come from `topBandLayout`

**Decision:** `dialogOpen` calls `topBandLayout(app.config.verbose, maxY)` and uses the returned
`cmdlineY0`/`cmdlineY1` instead of the hard-wired `3, 5`.

**Rationale:** The literals are the compact-mode values, so in verbose the prompt renders in the
cmdline at the bottom while the input field is drawn near the top, over the panels — a live bug
today. `topBandLayout` returns exactly `3, 5` in compact, so the compact path is byte-identical.
`dialogOpen` closes over `app`, so no plumbing is needed.

**Alternatives considered:** Reading the live `cmdline` view's coordinates from gocui (available, but
it makes the dialog depend on the order in which `layout()` and the key handler run within a frame);
duplicating the verbose arithmetic in `dialog.go` (a second source of truth for the geometry ADR
[010] deliberately centralised).

### Decision 12: Lengths are measured in runes

**Decision:** Every width computation in the composer and in `dialogInputX0` uses
`utf8.RuneCountInString`, not `len`.

**Rationale:** The truncation ladder emits `…` (3 bytes, 1 column) and the header emits `‹`/`›`. Byte
length would misplace the input field precisely on truncated lines — the case the clamp exists for.

**Alternatives considered:** Byte length (wrong by construction once any multi-byte rune is on the
line); a display-width library accounting for wide CJK glyphs (pgcenter renders no such text today,
and gocui itself counts cells per rune — adopting a different model would disagree with the renderer).

### Decision 13: Column names are stripped of control characters before entering the indicator

**Decision:** `filterToken` filters out control runes (including `\033`) from column names before
composing the token.

**Rationale:** Column names come from the server's row description, not from pgcenter, so a crafted
database object name can carry escape sequences. Today the header cell is always wrapped in
`\033[..m…\033[0m` (`printHeaderCell`), so an unterminated sequence heals at the cell boundary. The
indicator emits no SGR of its own, and `gocui.View.Clear()` resets the line buffer but **not** the
escape interpreter's state — so an unterminated sequence reaching the cmdline would persist for the
rest of the session rather than one frame. Zero-width control runes would also corrupt the rune count
the truncation ladder depends on. This is a new exposure created by this feature, not a pre-existing
one, so it is closed here.

**Alternatives considered:** Wrapping the token in SGR like the header does (heals the colour but not
the rune count, and hides rather than removes the injection); sanitising centrally in the query layer
(much wider blast radius, and out of this feature's scope — row *values* have the same exposure today
and are deliberately left alone).

### Decision 14: The clear timer is gated on a UI generation counter, not on a context

**Decision:** A package-level `atomic.Uint64` UI generation counter, incremented by `mainLoop` each
time it builds a new `Gui`. The timer goroutine records the generation when it is armed and, when it
fires, returns without calling `g.Update` if the generation has moved on.

**The generation must be read into a local variable _before_ `go func(...)`, not inside the
goroutine body.** A goroutine body runs at an unspecified later time, so reading the counter there
would observe whatever value is current when the timer fires — always equal to itself, making the
guard inert. The rate limiter permits up to five UI rebuilds per second, well inside the timer's
two-second life, so this is a reachable case and not a theoretical one. Capture, then compare.

**Rationale:** `g.Update` enqueues onto a 20-slot channel drained by `MainLoop`. On the restart path
— taken on a UI error *and* on every return from the pager, editor or psql — `mainLoop` abandons the
old `Gui` without closing it, so nothing drains that channel any more. Today's timer cannot leak
because `v.Clear()` returns unconditionally, so moving the callback onto `g.Update` (Decision 10) is
what introduces the hazard: this feature must close what it opens.

**Correction, found during implementation (task 05):** the leak is one level deeper than described
above. `gocui.Gui.Update` is itself `go func() { g.userEvents <- … }()` (`gui.go:311-313`), so the
**timer** goroutine does not block — it returns immediately, and the goroutine `Update` spawns is the
one that parks forever on the unread channel, holding its closure and pinning the abandoned `Gui`.
Same leak, same fix, same conclusion; only the mechanism differs from the description written before
the code was read. Recorded here rather than silently corrected, so the reasoning stays auditable.

A context cannot carry this. `RunMain` passes `context.Background()`, and the cancellable context is
created **inside** `mainLoop`'s restart loop (`top/ui.go:33`) and replaced on every iteration, while
`printCmdline` has neither context nor app in scope — which is the very reason Decision 1 exists. A
per-iteration ambient context would contradict Decision 1's write-once discipline, and the obvious
alternative reading yields a context that is never cancelled, i.e. a guard that passes review while
doing nothing.

**This is the one synchronisation primitive the feature introduces**, and it is deliberate: the
counter is genuinely written by `mainLoop`'s goroutine and read by timer goroutines. Everything else
stays single-goroutine.

**Alternatives considered:** A context guard (does not reach the timer — above). Leaving it unguarded
(a leak on a path that repeats with ordinary use, not just on errors). Closing the old `Gui` in
`mainLoop` (the right fix for the underlying bug, but it does not unblock a goroutine already parked
on the channel, and it is a wider change than this feature should make — record as tech debt).
Dropping the timer entirely in favour of expiring the message at the next render (the message would
then live until the next tick, up to 300 seconds at the maximum refresh interval).


## Data Models

No database or wire-format changes. Three additions to the in-memory UI state:

```go
// top/config.go
type config struct {
    // ...existing fields...
    scrollOffset          int           // existing, [009]
    verbose               bool          // existing, [010]
    autoScrollToOrderKey  bool          // NEW: one-shot request, set by the sort handlers,
                                        // consumed and cleared by renderDbstat
    refresh               time.Duration // NEW: durable copy of the refresh interval;
                                        // view.View.Refresh is a transient courier, zeroed after send
}

// top/ui.go
type cmdlineToken struct{ variants []string } // NEW: renderings longest-to-shortest
var cmdlineCfg *config                        // NEW: ambient, written once by setCmdlineConfig
```

`view.View` is **not** extended: per ADR [009], ephemeral view-independent UI state belongs on
`config`, otherwise it inherits the per-view persistence `viewSwitchHandler` provides.

## Dependencies

### New packages

None. `sort`, `strings`, `unicode/utf8` are stdlib and already available.

### Using existing (from project)

- `visibleColumns` (`top/stat.go`) — probed by `scrollOffsetFor`; not modified.
- `topBandLayout` (`top/layout.go`) — one-line fix, then reused as the dialog's y source.
- `internal/pretty` — unchanged; its `RateUnitPrefixed` output is wrapped as-is (Decision 9).
- `firstTickHint` (`top/stat.go:149`) — the precedent for a pure cmdline-decision helper.

## Testing Strategy

**Feature size:** M

### Unit tests

- `composeCmdline`: prefix-only line; prefix + message; message truncated first; rightmost token
  degraded through its variants; tokens dropped from the right; hard rune-truncate as last resort;
  **two tokens** (`[PAUSED]` + filter) composing without touching the function — the user-spec AC;
  a single-variant token never shrinking.
- `filterToken`: no filters → `ok == false`; one filter; several filters ordered by ascending column
  index (deterministic across runs); filter index outside `Cols` skipped; `Cols == nil`; the active
  predicate matching `printHeaderCell` (`re != nil && re.String() != ""`).
- `scrollOffsetFor`: sort column already visible → offset unchanged; column to the right → minimum
  offset that admits it; column to the left; column 0 (frozen) → no scroll; partially visible column
  counts as visible; index clamped against the current result's column count; empty result (all rows
  filtered out) → widths known from headers, offset still computable.
- The auto-scroll flag: cleared by `viewSwitchHandler` and, separately, by `switchViewToProcPidStat`
  — the latter asserted without a live PostgreSQL, which is why the reset sits before the `db.Local`
  guard (the `Test_switchViewToProcPidStatResetsScrollOffset` precedent).
- `dialogInputX0`: normal prompt; prompt longer than the terminal; prompt plus indicator; the
  reserved minimum always preserved; rune-vs-byte case with `…` in the line; a property test over the
  input domain asserting `x0 < x1` always holds — the invariant whose violation tears the UI down.
- `filterToken` sanitising: a column name carrying `\033[31m` or other control runes yields a token
  with them removed, and the rune count used by the ladder is unaffected.
- `topBandLayout`: the three changed cases plus the new height-guard threshold of 12.
- `renderSysstat`: line 1 carries `refresh: <N>s`; the `%ds` format at 1s, 60s and 300s; compact
  still 4 lines and verbose still 7.
- `renderSysstatVerbose` / `renderPgstatVerbose`: numeric values wrapped; `n/a` sentinels not
  wrapped; identifier fields not wrapped; the existing `n/a`-width invariant re-checked through the
  **already existing** `ansiEscape` / `visibleRuneLen` helpers (`top/stat_test.go:939-944`), which
  exist for exactly this purpose — do not add a second SGR stripper.
- `clearFilters` / `setFilter`: the three fixed message texts; clearing with no filters active; the
  in-place deletion visible through the stored view.

### Integration tests

None. No queries change and no live PostgreSQL is required by any code path this feature touches.

### E2E tests

None automated — a live terminal cannot be driven from `go test`, and `gocui.View` cannot be
constructed in a unit test. The equivalent coverage is the stand run below, which is a wave of its
own (Wave 4) rather than an afterthought.

## Agent Verification Plan

**Source:** user-spec "Как проверить".

### Verification approach

Two layers. `make test` (race detector) and `make lint` gate every task. Beyond that, the seven
user-visible behaviours are verified on a dedicated stand by driving the real binary inside tmux —
the regimen recorded in `.claude/skills/project-knowledge/patterns.md`, section "Driving the TUI on a
remote test stand". The stand address is requested from the owner at the start of the run: stands are
ephemeral with a TTL in hours. The freshly built binary must be copied to the stand explicitly — the
system-wide `pgcenter` there is built from `master`.

Two capture modes matter and picking the wrong one invalidates the check: `capture-pane -p` for text
and layout, `capture-pane -p -e` for anything about bold or colour. Narrow geometry (`-x 60`, `-y 12`,
`-x 80`) is how the auto-scroll, the height-guard threshold and the dialog clamp get exercised
deliberately.

### Per-task verification

| Task | verify: | What to check |
|------|---------|--------------|
| 1 | bash | `make test` — `topBandLayout` cases; guard threshold 12 |
| 2 | bash | `make test` — bold present on values, absent on sentinels and identifiers; the `n/a`-width invariant still holds through `visibleRuneLen` |
| 3 | bash | `make test` — three message texts; clearing with nothing active |
| 4 | bash | `make test` — `scrollOffsetFor` cases incl. empty result; flag cleared on both switch paths; `Test_scrollOrthogonalToSort` still green |
| 5 | bash | `make test` — `composeCmdline` ladder and the two-token case; `filterToken` deterministic ordering |
| 6 | bash | `make test` — `renderSysstat` shows `refresh: <N>s` at 1/60/300s and the seeded default before any `z` |
| 7 | bash | `make test` — `dialogInputX0` clamp and rune handling |
| 8 | bash | stand run: all seven behaviours, both capture modes, three geometries |
| 9 | bash | `make test`, `make lint`, `make vuln`; every user-spec criterion walked |

### Tools required

`bash` (make, go), `ssh` and `tmux` for the stand. No MCP tools, no browser, no live PostgreSQL for
the unit layer.

## Backward Compatibility

**Breaking changes:** no.

**Migration strategy:** N/A — no persisted format, no wire protocol, no config file. `record`/`report`
archives are untouched: no view, column or query changes, and `report` has its own render path that
does not import `top/`.

**DB migration compatibility:** N/A — no database changes.

**Consumer impact:** `pgcenter top` users see the changed behaviours by design. Two user-visible
defaults change: the verbose height-guard threshold drops from 13 to 12 terminal rows (verbose
becomes available one row earlier), and sorting now moves the column window. The latter **contradicts
a documented limitation**: `docs/features-catalog.md:189` records "no auto-scroll to the sort column"
as expected behaviour of [009]. That entry must be corrected at finalization, not merely
supplemented.

Internal signature changes, all within `top/`: `printSysstat` and `renderSysstat` gain a refresh
parameter (6 edit sites: 5 for `renderSysstat` including tests, 1 for `printSysstat`); `printCmdline` keeps its signature (Decision 1).

## Risks

| Risk | Mitigation |
|------|-----------|
| Ambient `cmdlineCfg` dereferenced outside the gocui goroutine, creating a real race | Deref only inside `g.Update` closures; the one `printCmdline` call reached from `doWork` (in `printStat`, `stat.go:166`) formats the message before enqueuing and touches no `config` field. Nil-safe for unit tests (bare `newConfig()`, nil `*gocui.Gui`). `make test` runs with `-race`. |
| Four files under change have no tests at all (`ui.go`, `keybindings.go`, `dialog.go`, `help.go`) | Every decision extracted into a pure function tested in isolation — `composeCmdline`, `filterToken`, `dialogInputX0`, plus `scrollOffsetFor`. Plumbing left too thin to hold logic. |
| Existing render tests break silently in the wrong way | They break as compile errors (signature changes) or as explicit assertion failures. The `n/a`-width invariant test compares byte offsets and must be repaired with SGR-stripping, **not deleted** — it is the regression test for resolved debt [012]. |
| Indicator flickers between frames | Filter keys sorted by column index (Decision 6), covered by a unit test asserting a deterministic string. |
| Two tasks in one wave editing the same file at once | Waves are **partitioned by file, not by region**: tasks run in parallel in a single working tree, each committing on its own, so two agents in one file means stale reads and overwritten edits — not a merge conflict resolved later. No two tasks in any wave share a file; see the wave-partition table below the task list. |
| Stand unavailable when the verification task runs (ephemeral, hours-long TTL) | Ask the owner at the start of the run; the unit layer is complete without it, so only the stand task blocks, not the feature. |
| Dialog view keeps its coordinates if the terminal is resized while a dialog is open | Pre-existing, out of scope, recorded as a known limitation. |

## Acceptance Criteria

- [ ] `make test` green with the race detector; `make lint` and `make vuln` clean.
- [ ] No cross-goroutine access introduced; the pre-existing cmdline-timer race is removed.
- [ ] Four new pure functions exist and are unit-tested independently of gocui.
- [ ] All 44 existing `printCmdline` call sites compile unchanged.
- [ ] `Test_scrollOrthogonalToSort` still passes, with its now-false comment corrected and an
      assertion on the new flag added.
- [ ] The `n/a`-width invariant test is repaired (SGR-stripped comparison), not removed.
- [ ] Every acceptance criterion from the user-spec is verified — unit-tested where possible, on the
      stand where it is visual.
- [ ] A column name carrying escape sequences cannot leave the terminal in a changed state through
      the indicator.
- [ ] `dialogInputX0` never produces `x0 >= x1`, asserted across the input domain.
- [ ] Two documentation corrections made at finalization, both for statements this feature makes
      false: `docs/features-catalog.md:189` ([009]'s "no auto-scroll to the sort column") and the
      "printCmdline() — Mutual Exclusion" section of `.claude/skills/project-knowledge/patterns.md`
      (a second call no longer discards the first's prefix, and the clear timer no longer erases).
- [ ] One tech-debt entry recorded at finalization: row values reach the terminal unsanitised through
      `printDataCell`, which writes them without an SGR wrapper. Pre-existing and wider than this
      feature — the indicator is sanitised here (Decision 13) only because the cmdline is a
      low-frequency persistent surface where corruption lasts a session, whereas the table repaints
      every tick amid correct sequences. Registering it keeps the asymmetry deliberate rather than
      accidental.

## Implementation Tasks

Waves are partitioned so that **no two tasks in a wave touch the same file**. Tasks in a wave run in
parallel in one working tree, so file-level disjointness — not region-level — is what keeps them from
overwriting each other. The partition is tabulated after the task list.

`dev-security-auditor` is deliberately absent from the reviewer sets: the feature has no network,
auth, persistence or SQL surface, and its security-relevant properties (goroutine discipline,
terminal-escape handling, UI denial of service) are covered by the tech-spec's own security review.
Do not restore the default reviewer set during decomposition.

### Wave 1 (независимые)

#### Task 1: Remove the wasted blank line in verbose layout
- **Description:** In verbose mode a row between the cmdline and the table belongs to no view and renders empty, because the verbose branch computes the table's top independently of the cmdline while the compact branch ties them together. Align the verbose arithmetic with the compact rule; the height guard shifts with it.
- **Skill:** code-writing
- **Reviewers:** dev-code-reviewer, dev-test-reviewer
- **Verify:** bash — `make test`
- **Files to modify:** `top/layout.go`, `top/layout_test.go`
- **Files to read:** `docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-code-research.md`

#### Task 2: Bold the numeric values in the verbose sections
- **Description:** Verbose panel rows print values plain while every base row bolds them — an omission from feature [010]. Wrap the numeric values in both verbose renderers, leaving degraded sentinels and identifier fields plain.
- **Skill:** code-writing
- **Reviewers:** dev-code-reviewer, dev-test-reviewer
- **Verify:** bash — `make test`
- **Files to modify:** `top/stat.go`, `top/stat_test.go`
- **Files to read:** `docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-code-research.md`, `docs/tech-debt.md`

#### Task 3: Clear all filters with `\`
- **Description:** Filters can only be cleared from the column that set them, and clearing reports success even when nothing was removed. Add a hotkey that clears every filter of the current screen, make the messages honest in all three cases, and document the key on the help screen.
- **Skill:** code-writing
- **Reviewers:** dev-code-reviewer, dev-test-reviewer
- **Verify:** bash — `make test`
- **Files to modify:** `top/config_view.go`, `top/keybindings.go`, `top/help.go`, `top/config_view_test.go`
- **Files to read:** `docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-code-research.md`

### Wave 2 (зависит от Wave 1)

#### Task 4: Auto-scroll the column window to the sort column
- **Description:** Sorting by a column outside the visible window currently has no visible effect. Add a one-shot request set by the sort handlers and consumed at render time, which moves the window by the minimum needed to bring the sort column into view. Manual scrolling must stay free afterwards, and the pending request must not leak into another screen.
- **Skill:** code-writing
- **Reviewers:** dev-code-reviewer, dev-test-reviewer
- **Verify:** bash — `make test`
- **Files to modify:** `top/config.go`, `top/config_view.go`, `top/stat.go`, `top/config_view_test.go`, `top/stat_test.go`
- **Files to read:** `docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-code-research.md`, `docs/decisions-log.md`

#### Task 5: Cmdline composer and the persistent filter indicator
- **Description:** The cmdline has no notion of persistent state — every message self-erases after two seconds, so an active filter is invisible once its column header scrolls away. Introduce a single composition point that prefixes every cmdline write with state tokens, plus a writer variant that does not arm the clear timer for dialog prompts. The composer must accept a second token later without being rewritten.
- **Skill:** code-writing
- **Reviewers:** dev-code-reviewer, dev-test-reviewer
- **Verify:** bash — `make test`
- **Files to modify:** `top/ui.go`, `top/top.go`, `top/dialog.go`, `top/ui_test.go` (new)
- **Files to read:** `docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-code-research.md`, `top/stat.go`, `top/config_view.go`

### Wave 3 (зависит от Wave 2)

#### Task 6: Show the refresh interval in the header
- **Description:** The interval set via `z` is invisible afterwards. Add a durable copy of it to the UI state, seeded from a single shared default, and render it on the first header line after the clock. The existing transient field cannot carry it.
- **Skill:** code-writing
- **Reviewers:** dev-code-reviewer, dev-test-reviewer
- **Verify:** bash — `make test`
- **Files to modify:** `top/config.go`, `top/top.go`, `top/ui.go`, `top/config_view.go`, `top/stat.go`, `top/stat_test.go`, `top/config_view_test.go`
- **Files to read:** `docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-code-research.md`

#### Task 7: Fix dialog geometry on both axes
- **Description:** The dialog input field is positioned from the prompt length assuming an empty cmdline, and its vertical position is hard-wired to the compact layout. The first breaks once a persistent indicator exists — and already crashes the UI today with the longest prompt on a narrow terminal; the second misplaces the field in verbose mode. Derive both axes from the real state.
- **Skill:** code-writing
- **Reviewers:** dev-code-reviewer, dev-test-reviewer
- **Verify:** bash — `make test`
- **Files to modify:** `top/dialog.go`, `top/dialog_test.go` (new)
- **Files to read:** `docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-code-research.md`, `top/layout.go`, `top/ui.go`

### Wave 4 (зависит от Wave 3)

#### Task 8: Stand verification run
- **Description:** Drive the freshly built binary on the dedicated stand through tmux and verify all seven user-visible behaviours, including the ones no unit test can reach: bold rendering, the absent blank line, the auto-scroll on a narrow terminal, and the longest dialog at 80 columns. Report every defect with the capture that shows it; do not edit code in this task.
- **Skill:** pre-deploy-qa
- **Reviewers:** none
- **Verify:** bash — stand run per `patterns.md`, both capture modes, three terminal geometries
- **Files to read:** `docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts.md`, `.claude/skills/project-knowledge/patterns.md`

Runs alone in its wave: it is the only task that needs the stand, and the stand is a single ephemeral
machine with one tmux session — two QA agents on it at once would collide.

**Who fixes what this task finds.** The task reports; the fix goes back to the task that owns the
file, through that task's own reviewers and the standard review cycle. A QA task carries no reviewers
by catalog, so letting it edit code would put an unreviewed change into the tree — which is why the
earlier "fix what you find" wording was withdrawn. Ownership therefore sits with the review cycle,
not with this task: no stand defect may be closed by editing code here.

### Final Wave

#### Task 9: Pre-deploy QA
- **Description:** Acceptance testing: run the full test suite and verify every acceptance criterion from the user-spec and this tech-spec, including that the stand findings from the previous wave were resolved.
- **Skill:** pre-deploy-qa
- **Reviewers:** none
- **Verify:** bash — `make test`, `make lint`, `make vuln`; every user-spec criterion walked

### Wave partition — no file appears twice in a wave

| Wave | Task | Files touched |
|---|---|---|
| 1 | 1 — verbose blank line | `layout.go`, `layout_test.go` |
| 1 | 2 — bold in verbose | `stat.go`, `stat_test.go` |
| 1 | 3 — clear filters | `config_view.go`, `keybindings.go`, `help.go`, `config_view_test.go` |
| 2 | 4 — auto-scroll | `config.go`, `config_view.go`, `stat.go`, `config_view_test.go`, `stat_test.go` |
| 2 | 5 — cmdline composer | `ui.go`, `top.go`, `dialog.go`, `ui_test.go` |
| 3 | 6 — refresh in header | `config.go`, `top.go`, `ui.go`, `config_view.go`, `stat.go`, `stat_test.go`, `config_view_test.go` |
| 3 | 7 — dialog geometry | `dialog.go`, `dialog_test.go` |

Ordering rationale beyond file disjointness: task 7 needs the composer from task 5 (horizontal axis)
and the layout fix from task 1 (vertical axis); task 6 touches the widest file set, so it goes last
among the code tasks; task 3's user-visible effect ("the indicator goes dark") is only observable
once task 5 lands, but its own code and tests are independent, so it ships early and is verified on
the stand.
