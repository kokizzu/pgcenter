# Code Research — 015-feat-tui-papercuts

**Date:** 2026-08-02 (round 1) / 2026-08-02 (round 2, implementation level)
**Feature base:** `docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts`

> ## ⚠ Read this first — round-1 status
>
> Round 1 (§1–§9 below) was written **before the approved user-spec existed** and assumed a
> four-item scope whose first item was **pause on `Space`**. The pause has since been split into a
> separate feature ([016]/[020]); the approved spec (`015-feat-tui-papercuts.md`) now has **seven**
> items. Round-1 status:
>
> | Round-1 content | Status |
> |---|---|
> | §1 "What a drain and discard pause must touch" (`:72-91`) | **INVALID — out of scope.** Belongs to [016]. |
> | §4.5 Keybindings — the whole `Space`/`KeySpace` analysis | **INVALID for this feature.** Two findings survive and now apply to the new `\` key: `execKeybindings` runs *every* match (not first-match-wins) and `viewName == ""` matches all views ⇒ scope `\` to `"sysstat"`. |
> | §5 table row "Item 1 → breaks nothing" | Superseded — see §13 below. |
> | §7 items 1, 2, 3 (blocking/ordering: `statCh`, `uiExit`, pause flag race) | **INVALID — out of scope.** |
> | §7 item 4 ("item 1's flag crosses goroutines") | **INVALID.** With the pause gone, *nothing* in this feature crosses goroutines. |
> | §7 items 5, 6 (pre-existing races at `ui.go:82-86` and in the cmdline timer) | **Still valid**, and item 6 is now directly in scope — see §10.4. |
> | §7 items 7–14, 16–19 | **Still valid.** |
> | §9 gocui API notes | Still valid; extended in §17. |
> | "26 printCmdline call sites" (§4.1, §7, spec Risks §319) | **WRONG. There are 44.** Full list in §10.2. |
>
> Everything from **§10 onward is round 2** and is the authoritative, implementation-level answer
> set for the seven-item spec.

**Round-1 scope:** four independent TUI improvements in the `top/` package — pause on `Space`,
auto-scroll to the sort column, persistent active-filter indicator, refresh interval in the header.

No spec file existed at round-1 time; inputs were the roadmap section
`### [015]` in `docs/roadmap-0.12.0.md` and `015-feat-tui-papercuts-interview.yml`.

---

## 1. Entry Points

### The event / render loop (`top/ui.go`)

`mainLoop(ctx, app)` (`top/ui.go:13`) is an infinite restart loop. Per iteration it:

1. `gocui.NewGui(gocui.OutputNormal)` → `app.ui` (`ui.go:19-24`)
2. `app.ui.SetManagerFunc(layout(app))` (`ui.go:27`)
3. `keybindings(app)` (`ui.go:30`)
4. spawns `doWork(ctx, app)` in a goroutine (`ui.go:38-42`)
5. blocks in `app.ui.MainLoop()` (`ui.go:45`)
6. on a non-`ErrQuit` error: stores `app.uiError`, rate-checks (5 errors / 1s), `cancel()`, `wg.Wait()`, restarts from step 1.

`doWork(ctx, app)` (`top/ui.go:71-100`) — this is the **render driver goroutine**:

```go
statCh := make(chan stat.Stat)              // ui.go:73 — UNBUFFERED
go collectStat(ctx, app.db, statCh, app.config.viewCh)   // ui.go:77
app.config.view.Refresh = time.Second       // ui.go:82
app.config.viewCh <- app.config.view        // ui.go:83
app.config.view.Refresh = 0                 // ui.go:86 — reset, not a per-view setting
for {
    select {
    case <-app.uiExit:  return             // ui.go:90 — pager/editor/psql escape
    case s := <-statCh: printStat(app, s, app.postgresProps)   // ui.go:93-94
    case <-ctx.Done():  wg.Wait(); return  // ui.go:95-97
    }
}
```

**Where a `stat.Stat` becomes a render:** `doWork` receives on `statCh` and calls
`printStat(app, s, props)` (`top/stat.go:157`). `printStat` does two things:

- calls `printCmdline` for the first-tick hint (outside any `g.Update`) — `stat.go:165-167`
- calls `app.ui.Update(func(g *gocui.Gui) error {...})` (`stat.go:169`) which clears and repaints
  `sysstat`, `pgstat`, `dbstat` and optionally `extra`.

### Goroutine map and synchronisation

| Goroutine | Runs | Touches |
|---|---|---|
| gocui MainLoop (`gui.go:351`) | key handlers, `layout()`, **and every `g.Update` closure body** | `app.config.*`, gocui views |
| `doWork` | the `select` loop, `printStat` **entry** | `statCh` receive, `app.config` (read) |
| `collectStat` (`top/stat.go:25`) | collector | `statCh` send, `viewCh` receive |
| per-call `printCmdline` timer goroutine (`ui.go:227-230`) | `v.Clear()` after 2s | gocui cmdline view buffer |
| per-call `g.Update` sender (`gocui/gui.go:312`) | `go func(){ g.userEvents <- ... }` | — |

**Key fact:** `gocui.Gui.Update` only *enqueues* the closure onto `g.userEvents`
(`gocui/gui.go:311-313`); `MainLoop` executes it (`gui.go:376-379`). So **the body of the `g.Update`
closure in `printStat` runs in the same goroutine as key handlers and `layout()`**. This is why the
codebase can read `app.config.verbose` / `app.config.scrollOffset` from both key handlers and the
render path without any lock — the invariant is documented at `top/layout.go:118-119` and
`top/stat.go:667-683`.

**The synchronisation between collector and UI is the unbuffered `statCh` itself.** `collectStat`
sends and blocks (`stat.go:72-79`) until `doWork` receives. `viewCh` (`top/config.go:29`,
`make(chan view.View)`) is also unbuffered — every key handler that mutates the view ends with
`config.viewCh <- config.view`, which blocks the gocui goroutine until `collectStat` picks it up.

### What a "drain and discard" pause must touch

- The only place a frame is consumed is `ui.go:93`. A pause that keeps the collector honest must
  still execute `case s := <-statCh:` and simply **not** call `printStat`. That is literally
  "drain and discard" — no extra channel, no goroutine.
- **Race hazard (this is the one real concurrency problem in the batch):** the pause flag is
  *written* by the `Space` key handler (gocui goroutine) and *read* by `doWork` (a different
  goroutine). Unlike `config.verbose` / `config.scrollOffset`, this crosses goroutines.
  `make test` runs with `-race` (project CLAUDE.md), so a plain `bool` on `config` will be flagged.
  Options: `sync/atomic.Bool` on `config`, a `sync.Mutex`, or a dedicated `pauseCh chan bool` added
  to `doWork`'s `select`. An atomic field on `config` is the smallest change consistent with the
  existing "state lives on `config`" pattern (ADR [009] "Scroll offset on top.config").
- **Restart survival:** `doWork` is re-created on every UI restart (pager/editor return, UI error
  recovery — `ui.go:16-68`), and `statCh` is re-created with it (`ui.go:73`). Pause state kept in a
  `doWork` local variable is lost on those paths; kept on `config` it survives. Decide deliberately
  which is wanted (likely: `config`, so the pause survives a `less` round-trip — or explicitly
  cleared, but then the reset must be in `doWork`, not in a local).
- `<-app.uiExit` (`ui.go:90`) must still be reachable while paused, otherwise `l`/`C`/`~`/`G`
  deadlock during pause. Keeping the flag as a guard *inside* the `statCh` case (rather than as a
  separate blocking wait) preserves this for free.

### Shared mutable state (`top/config.go`)

```go
type config struct {
    view         view.View       // active view (a VALUE copy, not a pointer)
    views        view.Views      // map[string]view.View — per-view persisted state
    queryOptions query.Options
    viewCh       chan view.View  // unbuffered
    logtail      stat.Logfile
    dialog       dialogType      // top/config.go:16
    menu         menuStyle
    procMask     int
    scrollOffset int             // top/config.go:19 — ephemeral, reset on view switch
    verbose      bool            // top/config.go:20 — persistent, mirrored into every views entry
}
```

`newConfig()` (`config.go:24-31`) creates `views` and the unbuffered `viewCh`. `app` holds
`*config` (`top/top.go:35`), so it is shared by pointer everywhere.

**All four items will want new fields here.** The existing precedent distinguishes:
- ephemeral / view-independent UI state → `config` (`scrollOffset`, ADR [009])
- persistent-across-switches state → `config` **plus** mirrored into every `views` entry
  (`verbose`, `top/verbose.go:19-24`).

---

## 2. Data Layer

No database/schema work. The relevant "data model" is `internal/view/view.go:10-32`:

```go
type View struct {
    Name       string
    Ncols      int                      // right border for OrderKey
    OrderKey   int                      // index of the sort column   <- item 2
    OrderDesc  bool
    Cols       []string                 // filled by alignViewToResult
    ColsWidth  map[int]int              // filled by alignViewToResult <- item 2
    Aligned    bool
    Msg        string
    Filters    map[int]*regexp.Regexp   // key = column index          <- item 3
    Refresh    time.Duration            // <- item 4 (see the trap below)
    ShowExtra, CollectExtra int
    Verbose, IOAvailable, DelayAcctAvailable, NotRecordable bool
}
```

`view.New()` (`view.go:38-361`) registers 26 views. Default `OrderKey` is 0 for all but four:
`replslots` 4 (`view.go:159`), `stat_io` 4 (`view.go:171`), `stat_io_time` 4 (`view.go:184`),
`statements_jit` 2 (`view.go:233`).

**`Refresh` is not persistent state — this is the trap for item 4.** Both writers set it, send it,
then immediately zero it:

- `top/ui.go:82-86` — `Refresh = time.Second` → `viewCh <-` → `Refresh = 0`
- `top/config_view.go:454-456` (`changeRefresh`) — set → `viewCh <-` → `Refresh = 0`

The reset is deliberate ("should not be saved as a per-view setting", `config_view.go:452-453`), and
`Test_changeRefresh` at `top/config_view_test.go:694-696` explicitly asserts
`config.view.Refresh` is *not* left at 0 in the invalid-input cases, i.e. the field is treated as a
transient courier. **At rest `config.view.Refresh == 0` always.** The only durable copy of the
interval is the `refresh` local variable inside `collectStat` (`top/stat.go:39`, updated at
`stat.go:93-95`) — in the collector goroutine, unreachable from the render path.

⇒ Item 4 needs a **new field** (e.g. `config.refresh time.Duration`), it cannot read
`config.view.Refresh`.

---

## 3. Similar Features

| Precedent | Where | What to reuse |
|---|---|---|
| **009 horizontal scroll** | `top/stat.go:751-852` (`visibleColumns`), `top/config_view.go:51-76` | Pure window function + render-time clamp/write-back. Item 2 plugs directly into this. |
| **010 verbose mode** | `top/verbose.go`, `top/layout.go:33`, `top/stat.go:268` | A boolean on `config` toggled by a key handler, read in the render path; pure `topBandLayout` extracted for testability. Closest template for item 1's flag (minus the cross-goroutine race). |
| **`printDbstat` → `renderDbstat`** | `top/stat.go:646-693` | The io.Writer-core split that makes render unit-testable without a terminal. |
| **`printSysstat` → `renderSysstat`** | `top/stat.go:258-310` | Same split for the header. Item 4 changes `renderSysstat`. |
| **First-tick cmdline hint** | `top/stat.go:144-167` (`firstTickHint`) | The only existing example of the render path writing to the cmdline every tick; its doc comment (`stat.go:159-164`) states the cmdline is *event-driven and never rewritten on refresh* — items 1 and 3 change that assumption. |
| **`showExtra` write-into-all-views** | `top/extra.go:70-75`, mirrored by `toggleVerbose` `top/verbose.go:19-24` | Pattern for state that must survive a view switch. |

---

## 4. Integration Points

### 4.1 The cmdline (`printCmdline`) — items 1 and 3

```go
// top/ui.go:207
func printCmdline(g *gocui.Gui, format string, s ...any) {
    if g == nil { return }                     // ui.go:209-211 — nil-safe, tests pass nil
    g.Update(func(g *gocui.Gui) error {
        v, err := g.View("cmdline")            // ui.go:215
        v.Clear()                              // ui.go:218 — always clears first
        fmt.Fprintf(v, format, s...)           // ui.go:219
        if format != "" {                      // ui.go:225
            t := time.NewTimer(2 * time.Second)
            go func() { <-t.C; v.Clear() }()   // ui.go:227-230
        }
        return nil
    })
}
```

Answers to the research questions:

- **Lifetime: every message is transient — it self-erases after 2 seconds.** Any non-empty format
  arms a timer whose goroutine calls `v.Clear()`. There is **no persistent status area**, and no
  notion of transient vs. persistent. Items 1 and 3 both need something that does not exist yet.
- **Who calls it (26 call sites):** `top/ui.go:154` (saved UI error on view creation),
  `top/ui.go:178` (verbose height-guard hint), `top/stat.go:166` (first-tick hint),
  `top/stat.go:229,237` (logtail errors), `top/config_view.go:108,138,156,260,293,295,297,299,340,428,430`,
  `top/dialog.go:61,67,72,115,152,162`, `top/extra.go:37,50,53,57`, `top/verbose.go:29,31`,
  `top/pglog.go:16,22`, plus `pgconfig.go`, `psql.go`, `signal.go`, `report.go`, `menu.go`.
- **On the next refresh tick:** nothing happens. `printStat`'s `g.Update` closure clears
  `sysstat`/`pgstat`/`dbstat`/`extra` but **never touches `cmdline`** (`top/stat.go:169-252`). So a
  message survives a tick and dies only on its own 2s timer or on the next `printCmdline`.
- **On a view switch:** `switchViewTo` ends with `printCmdline(g, "%s", app.config.view.Msg)`
  (`config_view.go:156`) — the view's `Msg` overwrites whatever was there.
- **While a dialog is open — three real hazards for a persistent indicator:**
  1. `dialogOpen` **appends** the prompt to the cmdline without clearing:
     `fmt.Fprint(p, prompt)` at `dialog.go:92`. A persistent `PAUSED`/`FILTER` text already in the
     buffer would sit in front of the prompt.
  2. The dialog input view is positioned at `x0 = len(prompt)-1` (`dialog.go:79`), computed on the
     assumption the cmdline starts empty. Leading indicator text shifts the visible prompt right
     while the input box stays put → misaligned entry field.
  3. A `printCmdline` timer armed *before* the dialog opened will fire `v.Clear()` mid-dialog and
     erase the prompt.
- **Existing convention worth preserving:** several handlers document that `printCmdline` must be
  called **exactly once per execution path**, because a second call overwrites the first before the
  user can read it (`top/config_view.go:288-290`, `top/stat.go:159-162`). Items 1 + 3 must coexist
  with each other *and* with transient messages — some composition rule is required (e.g. a
  `renderCmdline(config)` that composes `[PAUSED] [filter: N] <transient>` and is called from both
  the key handlers and the tick).
- **Latent bug to be aware of:** the 2s timer goroutine calls `v.Clear()` directly, **outside**
  `g.Update`, i.e. from a non-gocui goroutine touching the view buffer. It is a pre-existing data
  race (not currently caught because no test drives a live `Gui`). Any redesign of the cmdline
  should not deepen it.

### 4.2 Filters — item 3

- **Storage:** `view.Filters map[int]*regexp.Regexp`, key = **absolute column index**
  (`internal/view/view.go:24`). Per-view, persisted across switches by `viewSwitchHandler`
  (`config_view.go:241`), which writes the whole `config.view` value back into `config.views`.
- **Set / cleared:** `setFilter(answer string, view view.View) string` (`top/config_view.go:116-131`),
  invoked from `dialogFinish` case `dialogFilter` (`dialog.go:127`), which is reached via the `/`
  key (`keybindings.go:60`).
  - empty answer or `"\n"` → `delete(view.Filters, view.OrderKey)`, returns
    `"Filters: regular expression cleared"` (`config_view.go:118-121`)
  - bad regexp → `"Filters: <err>"`, nothing stored (`config_view.go:124-127`)
  - otherwise `view.Filters[view.OrderKey] = re`, returns `"Filters: ok"` (`config_view.go:129-130`)
  - the `view` parameter is passed **by value**, but `Filters` is a map header so mutation is
    visible to the caller. It works, but is subtle.
- **Multiple columns can be filtered at once — yes.** The key is `OrderKey` at the moment `/` is
  pressed, so moving the sort column and pressing `/` again adds a *second* entry. Nothing clears
  all filters at once.
- **Applied at render:** `renderDbstat` computes `isFilterRequired(config.view.Filters)`
  (`stat.go:692`, helper at `stat.go:1145-1152` — true if any value is non-nil) and passes it to
  `printStatData` (`stat.go:928`), whose per-row loop (`stat.go:948-958`) is **OR across filtered
  columns**: the first matching filtered column sets `doPrint=true` and breaks; a non-match sets
  `doPrint=false` and keeps scanning.
- **The `*` marker:** `printHeaderCell` (`top/stat.go:897-921`), condition at `stat.go:902`:
  `config.view.Filters[i] != nil && config.view.Filters[i].String() != ""` → prefix `"*"` to the
  column name. Note `isFilterRequired` does **not** check `.String() != ""`, a small asymmetry
  (unreachable today, since an empty answer deletes the key rather than storing an empty regexp).
- **What an indicator could show:** everything needed is on `config.view`:
  - count → `len(view.Filters)` (careful: entries are deleted on clear, so `len` is accurate;
    but use the same non-nil test as `isFilterRequired` to stay consistent)
  - column names → `config.view.Cols[i]` (populated by `alignViewToResult`, `stat.go:641`) or
    `s.Result.Cols[i]` at render time
  - the pattern → `re.String()`
  Given cmdline width and that patterns can be long, `filter: colname=~re` for one filter and
  `filters: 3 columns` for many is the natural shape — but that is a spec decision.
- **Pre-existing footgun the indicator will make visible (and should probably address in the spec):**
  clearing is keyed on the *current* `OrderKey`. A filter set on column 3 cannot be cleared while
  the sort column is 5 — pressing `/` + Enter there deletes a non-existent key and reports
  "regular expression cleared" anyway. With an indicator showing "3 filters active" the user will
  immediately hit this.

### 4.3 Sort column vs. scroll window — item 2

**Independence confirmed.** They are separate fields mutated by separate handlers:

- `OrderKey`: `orderKeyLeft` (`config_view.go:22-32`) / `orderKeyRight` (`config_view.go:35-45`),
  bound to `KeyArrowLeft`/`KeyArrowRight` on `sysstat` (`keybindings.go:22-23`). Both wrap around
  using `config.view.Ncols`.
- `scrollOffset`: `scrollLeft` (`config_view.go:51-58`) / `scrollRight` (`config_view.go:65-76`),
  bound to `'['`/`']'` (`keybindings.go:26-27`).
- The orthogonality is pinned by a test: `Test_scrollOrthogonalToSort`
  (`top/config_view_test.go:147-193`) asserts scroll handlers leave `OrderKey` untouched *and* sort
  handlers leave `scrollOffset` untouched. **Item 2 will break the second half of this test** — the
  sort handlers will no longer be required to leave scroll alone (though if the recentre is done as
  a deferred flag rather than an immediate offset change, the handler itself still does not touch
  `scrollOffset` and the test may survive verbatim; worth confirming during implementation).

**Where the recentre must live.** `view.ColsWidth` is only meaningful after `alignViewToResult`
(`top/stat.go:635-643`) has run against a real `stat.PGresult`, and that happens inside
`printDbstat` (`stat.go:658`) — after the key handler has long returned. Confirmed: the key handler
has no access to column widths or terminal width. So the offset adjustment must happen at render
time.

**The exact insertion point** is `renderDbstat` (`top/stat.go:671-693`), immediately *before*:

```go
win := visibleColumns(s.Result.Ncols, config.view.ColsWidth, termWidth, config.scrollOffset)  // stat.go:676
config.scrollOffset = win.clamped                                                              // stat.go:683
```

i.e. the flow becomes: consume the recentre request → derive a new `config.scrollOffset` such that
`OrderKey ∈ [win.first, win.last]` (or `OrderKey == 0`, which is always visible as the frozen
column) → clear the flag → call `visibleColumns` → existing write-back. `renderDbstat` already takes
`termWidth` as a parameter and already writes back into `config`, so this fits the 009 architecture
without a signature change and stays unit-testable through the existing `renderDbstat(&buf, cfg, s, 40)`
harness (`stat_test.go:1069`).

**State carrying the request:** a one-shot boolean on `config` (e.g. `recenterOrderKey bool`), set by
`orderKeyLeft`/`orderKeyRight` and cleared by `renderDbstat`. **No race:** both writer (key handler)
and reader (`g.Update` closure body) run in the gocui goroutine — same argument as `scrollOffset`
(`top/stat.go:678-683`) and `config.verbose` (`top/layout.go:118-119`). Contrast with item 1, which
genuinely crosses goroutines.

**Computation notes:**
- Column 0 is frozen and always visible (`stat.go:860-863`); `OrderKey == 0` needs no scroll.
- `visibleColumns` is pure and cheap, so the minimum offset making `OrderKey` visible can be found
  by probing it (`for off := 0; off <= ncols-1; off++`) or computed directly with a backward walk;
  probing reuses the single source of truth and avoids duplicating the marker-reservation logic
  (which ADR [009] "Partial last column + marker reservation in both walk directions" shows is
  easy to get subtly wrong).
- Prefer the *minimum* movement (only scroll if `OrderKey` is currently outside the window),
  otherwise every `←`/`→` press would jerk the window even when the target is already visible.
- **`config.view.Ncols` (used by the wrap-around in the handlers) and `s.Result.Ncols` (used by
  `visibleColumns`) can disagree for one frame after a view switch** — that is the exact issue #99
  class documented at `top/stat.go:629-634`. Clamp against `s.Result.Ncols` in the render path.

### 4.4 The sysstat header — item 4

`renderSysstat` (`top/stat.go:268`):

```go
func renderSysstat(w io.Writer, s stat.Stat, verbose bool, local bool, dataDir string) error
    fmt.Fprintf(w, "pgcenter: %s, load average: %.2f, %.2f, %.2f\n",
        time.Now().Format("2006-01-02 15:04:05"), ...)   // stat.go:272-274
```

Call chain: `printStat` → `printSysstat(v, s, app.config.verbose, app.db.Local, props.DataDirectory)`
(`stat.go:175`) → `renderSysstat` (`stat.go:259`). **It has no access to `config` today** — only the
three scalars the caller extracts. So item 4 requires either a new parameter
(`refresh time.Duration`, matching how `verbose` is already threaded) or a formatted string.
Adding a parameter is the consistent choice; it changes:

- `printSysstat` signature (`stat.go:258`) and its one call site (`stat.go:175`)
- `renderSysstat` signature (`stat.go:268`)
- **7 test call sites** — see §5.

Target format per the interview (`ui_ux_requirements`):
`pgcenter: <date time>, refresh: 1s, load average: ...`

**Race check on reading the interval from the render side:**
- `collectStat` reads the interval into its own local `refresh` (`top/stat.go:39`, updated at
  `stat.go:93-95`) — that copy lives entirely in the collector goroutine and is not shared.
- The `z` path (`changeRefresh`, `config_view.go:438-459`) runs in the gocui goroutine via
  `dialogFinish`; `renderSysstat` runs inside the `printStat` `g.Update` closure, also gocui
  goroutine. A new `config.refresh` field written there and read there is **race-free**.
- ⚠️ **But do not initialise it at `top/ui.go:82`.** That line runs in the `doWork` goroutine, so
  writing a new `config.refresh` there would be a genuine cross-goroutine write racing with
  gocui-goroutine reads. Initialise in `app.setup()` (`top/top.go:52-76`, runs before any goroutine
  starts) or as a package constant default. (Note `ui.go:82-86` already writes
  `app.config.view.Refresh` from `doWork` while `layout()` reads `app.config.view.ShowExtra` from
  the gocui goroutine — a narrow pre-existing race, see §7.)

### 4.5 Keybindings — item 1

`top/keybindings.go:17-91` is a flat `[]key{{viewname, key, handler}, ...}` slice registered in a
loop via `app.ui.SetKeybinding(k.viewname, k.key, gocui.ModNone, k.handler)` (`keybindings.go:85`).
`app.ui.InputEsc = true` (`keybindings.go:82`).

Registered view scopes: `""` (global — only `KeyCtrlC`, `KeyCtrlQ`), `"sysstat"` (all normal
hotkeys), `"dialog"` (Esc, Enter), `"menu"` (Esc, ↑, ↓, Enter), `"help"` (Esc, `q`).

**Is `Space` free? Yes — no binding uses it anywhere** (grep confirms no `KeySpace` and no `' '` in
`top/`). But two mechanics matter:

1. **Bind `gocui.KeySpace`, not the rune `' '`.** termbox delivers space as
   `ev.Key = 0x20 (KeySpace)`, `ev.Ch = 0` — the ASCII branch at
   `nsf/termbox-go@v1.1.1/termbox.go:573-579` treats every byte `<= KeySpace (0x20)` as a functional
   key and sets `Ch = 0`. gocui's `matchKeypress` requires `kb.key == key && kb.ch == ch`
   (`gocui/keybinding.go:31-33`), so a rune binding `' '` (key=0, ch=' ') can never match.
2. **Register it on `"sysstat"`, never globally (`""`).** `execKeybindings`
   (`gocui/gui.go:622-636`) iterates **all** bindings and runs **every** match — it is not
   first-match-wins — and `matchView` returns `true` unconditionally for `viewName == ""`
   (`keybinding.go:36-41`). Worse, when any binding matched, `onKey` skips
   `g.currentView.Editor.Edit(...)` (`gui.go:597-602`). A global `Space` binding would therefore
   (a) fire while a dialog is open and (b) **make it impossible to type a space character into any
   dialog** — filter regexes, the queryid prompt, the age prompt. `gocui`'s default editor maps
   `KeySpace` → `EditWrite(' ')` at `gocui/edit.go:34`.
   The `menu` and `help` views have no `Space` binding, and since `sysstat` is not the current view
   while they are open (`menu.go`, `help.go:67`, `dialog.go:101`), a `sysstat`-scoped `Space` is
   inert there — the desired behaviour.

**Help text:** `helpTemplate` at `top/help.go:10-47`. New hotkeys go in the `general actions:` block
(the `[,]` scroll line at `help.go:23` is the 009 precedent) or `other actions:` (`help.go:40-45`,
where `z` lives). There is **no test asserting help content**, so this is documentation-only, but it
is where users discover the key.

---

## 5. Existing Tests

Framework: `testify/assert`, standard `go test`. `make test` runs with the race detector and
coverage. **`top/` tests run locally without PostgreSQL** — explicitly warned in
`.claude/skills/project-knowledge/patterns.md:127` ("These `top` tests DO run locally without
Postgres... When touching `top/menu.go` or `top/config_view.go`, grep `top/*_test.go` for the
function you changed before assuming it is untested").

| File | Lines | Covers |
|---|---|---|
| `top/stat_test.go` | 1093 | `renderSysstat` (7 tests), `renderPgstat` (6), `visibleColumns` (2 incl. a property test), `printStatHeader`/`printStatData`/`renderDbstat` (8), `alignViewToResult`, `formatInfoString`, `formatError`, `firstTickHint` |
| `top/config_view_test.go` | 699 | all `config_view.go` handlers: order keys, scroll, view switches, widths, sort order, filters, refresh, idle-conns, sys-tables, age |
| `top/layout_test.go` | 76 | `topBandLayout` table test |
| `top/verbose_test.go` | 70 | `toggleVerbose` |
| `top/top_test.go`, `config_test.go`, `menu_test.go`, `errrate_test.go`, `reload_test.go`, `reset_test.go`, `signal_test.go`, `report_test.go` | — | misc |

**No test file exists for `top/ui.go`, `top/keybindings.go`, `top/dialog.go` or `top/help.go`.**
`printCmdline`, `keybindings()`, `doWork` and `mainLoop` are entirely untested — items 1 and 3 land
mostly in untested territory, and driving a real `gocui.Gui` in a unit test is not viable. The
project's answer to this (009, 010) is to extract the decision logic into a pure function and test
that: `firstTickHint(s stat.Stat) (string, bool)` (`top/stat.go:149`) is the exact miniature
precedent for a cmdline-composition helper.

Representative signatures:

```go
// top/stat_test.go:1060 — render-time write-back, no terminal needed
func Test_printDbstat_clampsScrollOffset(t *testing.T) {
    cfg := makeRenderConfig(6, 10); cfg.scrollOffset = 1 << 20
    err := renderDbstat(&buf, cfg, makeRenderResult(6, 2), 40)
    assert.Equal(t, 2, cfg.scrollOffset)
}

// top/config_view_test.go:147 — handler tests must drain the unbuffered viewCh from a goroutine
func Test_scrollOrthogonalToSort(t *testing.T) { ... }
```

Helpers to reuse: `makeRenderConfig(ncols, width int) *config` (`stat_test.go:830`) and
`makeRenderResult(ncols, nrows int)`.

### Tests a change will break

| Change | Breaks |
|---|---|
| Item 4: new field in the sysstat line 1 | **`Test_renderSysstat_compact`** (`stat_test.go:43`) — the line-1 regex at `stat_test.go:65-67` pins `^pgcenter: <ts>, load average: ...$` exactly. Must be updated. |
| Item 4: `renderSysstat` signature | **4 call sites in tests**: `stat_test.go:59` (`Test_renderSysstat_compact`), `stat_test.go:150` (helper `verboseSysstatLines`, shared by the five `_verbose*` tests at 157/180/203/235/258), `stat_test.go:284-285` (`_compactUnchanged`); plus `top/stat.go:259` (`printSysstat`). The helper absorbs five tests, so the edit is small — all are compile errors, mechanical to fix. |
| Item 4: line count | `Test_renderSysstat_compactUnchanged` (`stat_test.go:275`) asserts 4 compact / 7 verbose lines. Safe **as long as the interval goes on the existing line 1** and does not add a line. |
| Item 2 | `Test_scrollOrthogonalToSort` (`config_view_test.go:147`) — the "sort-keeps-scroll" half (lines 173-192). Survives if the recentre is a deferred flag consumed in `renderDbstat`; breaks if the handler mutates `scrollOffset` directly. |
| Item 2 | `Test_printDbstat_clampsScrollOffset` (`stat_test.go:1060`) and every `renderDbstat`-based test — safe only if the recentre flag defaults to false in `makeRenderConfig`. |
| Item 1 | Nothing existing (no keybinding or `doWork` tests). |
| Item 3 | Nothing existing (`Test_setFilter` at `config_view_test.go:333` pins only the four return strings). |

No count-based test in `top/` is affected — `Test_selectMenuStyle`, `Test_statementsNextView`,
`Test_switchViewTo` and `record.Test_filterViews` all key on views/menu entries, and this feature
adds none.

---

## 6. Shared Utilities

| Symbol | Location | Purpose |
|---|---|---|
| `printCmdline(g, format, ...)` | `top/ui.go:207` | Only cmdline writer. Nil-safe. Auto-clears after 2s. |
| `visibleColumns(ncols, colsWidth, termWidth, offset) columnWindow` | `top/stat.go:751` | Pure. Single source of truth for the visible window + offset clamp. |
| `columnWindow{first,last,clamped,hiddenLeft,hiddenRight}` | `top/stat.go:725` | Window value type. |
| `alignViewToResult(config, r)` | `top/stat.go:635` | Fills `view.Cols` / `view.ColsWidth` from real data. Runs before every render. |
| `isFilterRequired(map[int]*regexp.Regexp) bool` | `top/stat.go:1145` | Any non-nil filter. |
| `firstTickHint(s) (string, bool)` | `top/stat.go:149` | Pure cmdline-decision helper — template for items 1/3. |
| `topBandLayout(verbose, maxY)` | `top/layout.go:33` | Pure band geometry. |
| `viewSwitchHandler(config, name)` | `top/config_view.go:240` | Persists current view, loads the next, resets `scrollOffset`. |
| `math.Min` / `math.Max` | `internal/math` | Used for clamping in `config_view.go`, `stat.go`. |
| `pretty.*` (`Size`, `SizeWidth`, `ReserveWidth`, `RateUnitPrefixed`, `Ceil`) | `internal/pretty` | Fixed-width formatting so labels do not shift between ticks — relevant if item 4's interval is formatted for a stable header width. |

---

## 7. Potential Problems

**Blocking / ordering**

1. **`statCh` unbuffered (`ui.go:73`) — already in the roadmap, restated for completeness.** Skipping
   the render without receiving would block `collectStat` at `stat.go:73` and deliver a stale frame
   on resume. Drain-and-discard in the `case s := <-statCh:` branch is the fix.
2. **`viewCh` is also unbuffered** (`config.go:29`). Every key handler ends with
   `config.viewCh <- config.view`, which blocks the **gocui goroutine** until `collectStat` reaches
   its `select` (`stat.go:84-140`). During pause the collector still ticks, so this stays live — but
   any pause design that stops the collector (explicitly rejected) would freeze the whole UI on the
   next keypress. This is a second, independent reason the "gate the render only" decision is right.
3. **`app.uiExit` (`ui.go:90`) must stay reachable while paused** — `l`, `C`, `~`, `G` send on an
   unbuffered `uiExit` from the gocui goroutine and deadlock the UI if `doWork` is not in its
   `select`.

**Races**

4. **Item 1's flag genuinely crosses goroutines** (gocui writer, `doWork` reader). `make test` runs
   `-race`. Needs `atomic.Bool` / mutex / channel. Items 2, 3, 4 do **not** cross goroutines
   (all readers/writers are in the gocui goroutine) — do not over-synchronise them.
5. **Pre-existing race, `top/ui.go:82-86`:** `doWork` writes `app.config.view.Refresh` (and reads
   `app.config.view` to send it) while the gocui goroutine reads `app.config.view.ShowExtra` in
   `layout()` (`ui.go:186`). Narrow (one write at startup) and currently unobserved. Item 4 must not
   widen it — initialise any new `config.refresh` in `app.setup()` instead.
6. **Pre-existing race in `printCmdline`'s clear timer** (`ui.go:227-230`): `v.Clear()` from a plain
   goroutine, outside `g.Update`, mutating the gocui view buffer. Items 1 and 3 will interact with
   this timer directly.

**Cmdline design**

7. **There is no persistent status concept.** Two persistent indicators (PAUSED, filter) plus
   transient messages plus dialog prompts all share one single-line view that every writer clears
   first. Without a composition point this becomes a last-writer-wins mess. Concretely:
   `switchViewTo` overwrites with `view.Msg` (`config_view.go:156`), `layout()` may inject
   "terminal too short for verbose mode" (`ui.go:178`), `printStat` may inject "collecting..."
   (`stat.go:166`), and every 2s an orphan timer wipes the line.
8. **Dialog geometry depends on an empty cmdline** — `dialog.go:79` computes the input box x-origin
   from `len(prompt)`, and `dialog.go:92` appends without clearing. A persistent prefix breaks the
   alignment of every dialog.
9. **The `*` header marker and a cmdline filter indicator can disagree** — `printHeaderCell` requires
   `.String() != ""` (`stat.go:902`), `isFilterRequired` does not (`stat.go:1145-1152`). Use one
   predicate for both.

**Filters**

10. **Filters are only clearable from the column that set them** (`setFilter` keys on
    `view.OrderKey`, `config_view.go:119`). An indicator makes stale filters visible without giving
    the user a way to clear them without hunting for the right column. Consider whether a
    "clear all filters" affordance belongs in this feature or is explicitly out of scope.
11. **Filters persist across view switches** (`viewSwitchHandler` saves the whole view,
    `config_view.go:241`). Leaving a screen and returning restores the filter — the indicator must
    be re-rendered on return, not only when the filter is set.

**Auto-scroll**

12. **`config.view.Ncols` vs `s.Result.Ncols`** can disagree for one frame after a view switch —
    the documented issue #99 class (`stat.go:629-634`). `orderKeyRight` wraps on `view.Ncols`
    (`config_view.go:38`), `visibleColumns` uses `s.Result.Ncols`. Clamp before indexing.
13. **`config.scrollOffset = 0` on view switch** (`config_view.go:243`) and in
    `switchViewToProcPidStat` (`config_view.go:257`) — the interview decided *not* to recentre on
    view entry, so the four non-zero default `OrderKey`s (`replslots` 4, `stat_io` 4,
    `stat_io_time` 4, `statements_jit` 2) start with the sort column possibly off-screen on a very
    narrow terminal. Self-corrects on the first arrow press. Consciously accepted — do not
    re-litigate, but the spec should state it as a known limitation.
14. **The recentre flag must not survive a view switch.** `viewSwitchHandler` and
    `switchViewToProcPidStat` both already reset `scrollOffset`; a stale recentre request would fire
    on the new screen's first frame. Either reset it in both paths (ADR [009] "Reset offset on both
    view-switch paths" is the precedent — one reset in `viewSwitchHandler` alone **misses**
    `switchViewToProcPidStat`) or make it harmless by construction.

**Relevant ADRs (settled — do not propose alternatives)**

- `[009] Scroll offset on top.config, not on view.View` (`docs/decisions-log.md:478`) — new
  ephemeral UI state (pause, recentre request) belongs on `config`, not `view.View`.
- `[009] Reset offset on both view-switch paths` (`:510`) — `switchViewToProcPidStat` bypasses
  `viewSwitchHandler`; anything reset on switch needs resetting in both, and **before** the
  `db.Local` guard for testability.
- `[009] Manual column window, not gocui viewport scroll` (`:494`) — item 2 must work through
  `visibleColumns`, not gocui origin-x.
- `[009] Partial last column + marker reservation in both walk directions` (`:526`) — the window
  semantics are "start-in-budget"; a column can be partially visible. Item 2's notion of
  "OrderKey is visible" must match this (a partially visible sort column counts as visible, or the
  recentre will fight `maxOffset`).
- `[009] Hotkeys `[` / `]`, not Shift/Ctrl/Alt + arrow` (`:542`) — termbox cannot distinguish
  Shift/Ctrl+key; only `ModNone`/`ModAlt` exist. Confirms plain `Space`.
- `[010] view.Verbose + config.verbose dual boolean` (`:590`) — the precedent for a flag that must
  reach both the render path and the collector. Pause needs only the render path, so it should
  **not** ride `viewCh`.

**Tech debt in touched modules** (`docs/tech-debt.md`)

- `[025] PGresult.sort does not bounds-check its sort key` (Severity: Medium-ish, `:206`) — directly
  adjacent to item 2: `OrderKey` is the sort key, and this feature makes `OrderKey` drive the scroll
  window too. Read the entry before touching the order-key path; do not worsen it. Not required to
  fix here.
- `[021] Column widths not recomputed after a mid-archive version change` (`:252`) — report-side,
  but same `ColsWidth`/`alignViewToResult` machinery item 2 depends on.
- `[019] Nine tests skip every version when one cluster is unavailable` (`:113`) — not in `top/`,
  but affects how green a `make test` run looks.
- `[016] Collector/parsers swallow errors silently` (`:156`) — relevant to item 1: a query error
  during pause is delivered as `stat.Stat{Error: ...}` on `statCh` and would be **discarded
  unseen** by a naive drain. Decide whether error frames bypass the pause gate (`printDbstat`
  renders `s.Error` at `stat.go:648-655`), or the user can sit on a paused screen while the
  connection is gone.

**Not mentioned in the feature description**

15. **`profile` and `record`/`report` are unaffected.** `pgcenter profile` (`profile/`) and
    `record`/`report` do not import `top/` and have their own render paths; this feature adds no
    view, no column and no query, so `record.Test_filterViews` counts are untouched.
    `report`'s own printer is separate from `top/stat.go`.
16. **The `S` / `procpidstat` path bypasses `viewSwitchHandler`** (`config_view.go:253-303`, ADR
    `[009]:510`). Any per-view reset introduced by this feature must be duplicated there, placed
    **before** the `!app.db.Local` guard (`config_view.go:259`) so it is reachable in tests without
    a live PostgreSQL — see `Test_switchViewToProcPidStatResetsScrollOffset`
    (`config_view_test.go:219-231`).
17. **Verbose mode (010) interaction:** verbose grows the top band and pushes `cmdline` down
    (`layout.go:39-50`), and `layout()` emits its own cmdline hint on the compact-fallback flip
    (`ui.go:176-183`). A persistent indicator must survive that hint and vice versa. Verbose also
    adds 3 lines to `renderSysstat` — item 4's field goes on line 1, above the verbose block, so no
    geometry change.
18. **Pager / editor / psql round-trip:** `l`, `C`, `~`, `G` (via `printQueryReport`,
    `report.go:147`) send on `uiExit`, `doWork` returns, `g.Close()` runs, then `mainLoop` rebuilds
    the Gui, re-registers keybindings and starts a **fresh** `doWork` with a **fresh** `statCh`
    (`ui.go:16-73`). State on `config` survives; state in `doWork` locals does not. Also, the
    previous `collectStat` goroutine is left blocked on the old `statCh` until `ctx` is cancelled —
    pre-existing, but relevant if the pause implementation adds anything to that goroutine.
19. **`config.view.Refresh` is zeroed by design** (`ui.go:86`, `config_view.go:456`) — item 4 must
    not "fix" that by keeping it set; `collectStat`'s change-detection branch
    (`stat.go:93-96`) treats a non-zero `Refresh` on an incoming view as a refresh change and
    `continue`s *before* the ShowExtra/Verbose/Reset branches. Leaving `Refresh` populated would
    make every subsequent view update look like a refresh change and silently skip extra-stats and
    verbose handling. The branch-order comment at `stat.go:86-90` says explicitly the order is
    load-bearing.

---

## 8. Constraints & Infrastructure

- **Go 1.25+**, `github.com/jroimartin/gocui v0.5.0` (unmaintained upstream),
  `github.com/nsf/termbox-go v1.1.1`, `pgx/v5`, `testify`.
- **gocui/termbox modifier support is `ModNone` / `ModAlt` only** — no Shift/Ctrl combinations
  (ADR `[009]:542`). Confirmed at `gocui/keybinding.go` and termbox event extraction
  (`termbox.go:573-579`).
- **`gocui.OutputNormal`** (`ui.go:19`) — 8-color ANSI SGR only; existing header/highlight sequences
  are hand-written (`stat.go:911-919`).
- **`app.ui.InputEsc = true`** (`keybindings.go:82`) — Esc is delivered as a key, no Alt-prefix
  parsing.
- **Build / test:** `make build`, `make test` (race + coverage), `make lint` (golangci-lint + gosec),
  `make vuln` (govulncheck). Debt item `[013]` notes golangci-lint v1 config vs a locally installed
  v2, so lint effectively runs in CI.
- **gitleaks pre-commit hook** is required (global CLAUDE.md).
- **Branching:** work on `develop`, squash-merge to `master`, push to `release` to ship.
- No env variables, migrations or deployment changes for this feature. `$PAGER` / `$EDITOR` are read
  by `pglog.go:27` and `pgconfig.go` on the UI-exit paths.

---

## 9. External Libraries

Only `gocui` matters, and only the parts already inspected above. No Context7 lookup was useful —
`jroimartin/gocui` is archived upstream and pinned at v0.5.0; the module source in
`$GOMODCACHE/github.com/jroimartin/gocui@v0.5.0` is authoritative. Key APIs for this feature:

```go
func (g *Gui) SetKeybinding(viewname string, key interface{}, mod Modifier, handler func(*Gui, *View) error) error  // gui.go:249
func (g *Gui) Update(f func(*Gui) error)          // gui.go:311 — ENQUEUES onto g.userEvents; body runs in MainLoop's goroutine
func (g *Gui) MainLoop() error                    // gui.go:351 — single loop over tbEvents + userEvents
func (g *Gui) execKeybindings(v *View, ev *termbox.Event) (matched bool, err error)  // gui.go:622 — runs EVERY match, not first-match
func (kb *keybinding) matchView(v *View) bool     // keybinding.go:36 — viewName "" matches ALL views
func (kb *keybinding) matchKeypress(key Key, ch rune, mod Modifier) bool  // keybinding.go:31 — key AND ch must both match
const KeySpace = Key(termbox.KeySpace)            // keybinding.go:124 == 0x20
func (e *Editor) Edit(v *View, key Key, ch rune, mod Modifier)  // edit.go:34 — KeySpace -> EditWrite(' ')
```

Behavioural consequences already covered in §4.5: bind `gocui.KeySpace` (not `' '`), scope it to
`"sysstat"` (not `""`), and note that `onKey` suppresses the editor when any binding matched
(`gui.go:597-602`).

---
---

# Updated: 2026-08-02 — round 2, implementation level

Scope: the seven items of the approved user-spec. Everything below is verified against the working
tree at `develop` @ `80133bc`. Line numbers are current-file, pre-change.

---

## 10. Cmdline composition (spec item: filter indicator + dialog prompt path)

### 10.1 What exists today

```go
// top/ui.go:207
func printCmdline(g *gocui.Gui, format string, s ...any) {
	if g == nil { return }                                    // :209-211 nil-safe
	g.Update(func(g *gocui.Gui) error {
		v, err := g.View("cmdline")                           // :214
		v.Clear()                                             // :218  <- unconditional wipe
		_, err = fmt.Fprintf(v, format, s...)                 // :219
		if format != "" {                                     // :225
			t := time.NewTimer(2 * time.Second)
			go func() { <-t.C; v.Clear() }()                  // :226-231  <- wipe, no g.Update
		}
		return nil
	})
}
```

Three structural facts drive the design:

1. **`printCmdline` has `g` but not `config`.** There is no way to compose a prefix from
   `config.view.Filters` inside it without either changing the signature or introducing an ambient
   source. `top/` currently has **zero package-level `var`s** (`grep -rn '^var ' top/*.go` excluding
   tests → empty), so an ambient pointer would be a new precedent for the package.
2. **The body of `g.Update`'s closure runs in the gocui MainLoop goroutine**
   (`gocui/gui.go:311-313` enqueues, `gui.go:376-379` executes) — but **`printCmdline`'s own body
   runs in the caller's goroutine**, which for `top/stat.go:166` is `doWork`. So any `config` read
   must happen *inside* the closure, never in the function body.
3. **The 2s timer's `v.Clear()` runs outside `g.Update`** (`ui.go:227-230`) — a pre-existing data
   race on the view buffer. The spec's "indicator must survive the timer" requirement lands exactly
   here, and the spec's own mitigation ("перерисовку инициировать штатным механизмом обновления")
   is achievable by routing the timer through `g.Update`, which *removes* the race rather than
   deepening it.

### 10.2 The 44 call sites (round 1 said 26 — that was wrong)

`grep -rn "printCmdline(" top/ --include=*.go` → 45 hits, one of which is the definition
(`ui.go:207`). **44 call sites**, none of which need to change under the recommended design.

| File | Lines | Count | Has `*config` in scope? | Goroutine of the *body* | Needs change? |
|---|---|---|---|---|---|
| `top/config_view.go` | 108, 138, 156, 260, 293, 295, 297, 299, 340, 428, 430 | 11 | yes (`config` / `app`) | gocui | no |
| `top/dialog.go` | 61, 67, 72, 115, 152, 162 | 6 | yes (`app`) | gocui | no¹ |
| `top/extra.go` | 37, 50, 53, 57, 77 | 5 | yes (`app`) | gocui | no |
| `top/menu.go` | 112, 155, 175, 193, 203 | 5 | yes (`app` / `config`) | gocui | no |
| `top/pgconfig.go` | 33, 39, 44, 72, 79, 85 | 6 | **no** (`showPgConfig(db, uiExit)`) | gocui | no |
| `top/pglog.go` | 16, 22 | 2 | **no** (`showPgLog(db, ver, uiExit)`) | gocui | no |
| `top/reset.go` | 36 | 1 | **no** (`resetStat(db, schema)`) | gocui | no |
| `top/signal.go` | 144 | 1 | yes (`config`) | gocui | no |
| `top/stat.go` | 166, 229, 237 | 3 | yes (`app`) | **`doWork` for :166**; gocui for :229/:237 (inside the `printStat` `g.Update` closure) | no |
| `top/ui.go` | 154, 178 | 2 | yes (`app`) | gocui (inside `layout()`) | no |
| `top/verbose.go` | 29, 31 | 2 | yes (`app`) | gocui | no |

¹ `dialog.go:115` (`printCmdline(g, "")`) keeps its signature but **changes meaning**: instead of
clearing the line it will render the prefix-only line. That is the desired behaviour (indicator
survives dialog close) and requires no edit.

**The only write that must change is not a `printCmdline` call at all:** `top/dialog.go:92`
`fmt.Fprint(p, prompt)` — a raw append into the cmdline view that bypasses composition entirely.

Three call sites (`pgconfig.go`, `pglog.go`, `reset.go`, 9 calls total) have **no** `*config` in
scope. This is the decisive argument against the "add a `*config` parameter" variant: it would
cascade into `showPgConfig`/`showPgLog`/`resetStat` signatures, `top/keybindings.go:44,50,51` and
`top/reset_test.go` / `top/signal_test.go`.

### 10.3 Proposed design — pure core + ambient state + two writers

**Pure functions (unit-testable, no gocui, no `config`):**

```go
// cmdlineToken is one token of the cmdline reserved prefix. variants holds the token's renderings
// ordered from longest to shortest; composeCmdline degrades a token to a shorter variant only when
// the line does not fit. A token that must never shrink supplies exactly ONE variant — that is how
// [PAUSED] will be added by feature [016] without touching composeCmdline.
type cmdlineToken struct{ variants []string }

// composeCmdline builds the final cmdline line: tokens concatenated left to right, then a single
// space, then msg — the whole clamped to width RUNES (not bytes; the ellipsis is multi-byte).
// Truncation ladder, in order: (1) msg is cut; (2) the RIGHTMOST token steps through its variants;
// (3) tokens are dropped from the right; (4) last resort, hard rune-truncate. Pure.
func composeCmdline(tokens []cmdlineToken, msg string, width int) string

// filterToken builds the "[F:datname,usename]" token from a view's filters and the column names
// known at that moment. ok is false when no filter is active. Filter indices outside cols are
// SKIPPED (issue #99 class — see §11). Names are emitted in ascending column-index order: Go map
// iteration is randomised, so the key set must be sorted or the indicator flickers between frames.
// The active-filter predicate must match printHeaderCell (top/stat.go:902):
// re != nil && re.String() != "".
// Variants produced: ["[F:datname,usename]", "[F:datname,…]", "[F:…]"] — exactly the spec ladder.
func filterToken(filters map[int]*regexp.Regexp, cols []string) (cmdlineToken, bool)
```

The spec's AC "покрыта unit-тестом на двух токенах: `[PAUSED][F:datname] сообщение`, без правки
самой функции" is satisfied literally: the test calls
`composeCmdline([]cmdlineToken{{variants: []string{"[PAUSED]"}}, filterTok}, "сообщение", 80)`.

**State read (gocui goroutine only):**

```go
// cmdlineTokens collects the active reserved-prefix tokens from config. MUST be called only from
// the gocui MainLoop goroutine: key handlers, layout(), or inside a g.Update closure. It reads
// config.view.Filters and config.view.Cols, both of which are written exclusively from that same
// goroutine (setFilter via dialogFinish, clearFilters, viewSwitchHandler, and alignViewToResult
// inside printStat's g.Update closure).
func cmdlineTokens(config *config) []cmdlineToken
```

**Ambient source (the reason no call site changes):**

```go
// cmdlineCfg is the ambient config that printCmdline composes the reserved prefix from. It is set
// ONCE by newApp (top/top.go:44-49), before any goroutine exists, and is DEREFERENCED ONLY inside
// g.Update closures — i.e. always in the gocui MainLoop goroutine. Nil-safe: unit tests build a
// bare config via newConfig() and pass a nil *gocui.Gui, so neither path reaches a deref.
var cmdlineCfg *config
```

**Writers:**

```go
// printCmdline prints a transient message on the cmdline behind the reserved prefix, and arms the
// 2s timer that restores the prefix-only line. Signature UNCHANGED — all 44 call sites compile and
// behave as before, plus the prefix.
func printCmdline(g *gocui.Gui, format string, s ...any)

// printCmdlinePersist prints on the cmdline WITHOUT arming the clear timer. Used by the dialog
// prompt (top/dialog.go:92), which must survive until the dialog is closed — arming the timer there
// would erase the prompt under the user's cursor after two seconds.
func printCmdlinePersist(g *gocui.Gui, format string, s ...any)

// writeCmdline is the shared core of both: compose prefix + message, clamp to the cmdline view's
// width, write, and optionally arm the restore timer.
func writeCmdline(g *gocui.Gui, arm bool, format string, s ...any)
```

Skeleton, with the goroutine boundary marked:

```go
func writeCmdline(g *gocui.Gui, arm bool, format string, s ...any) {
	if g == nil { return }                       // preserved from ui.go:209-211
	msg := fmt.Sprintf(format, s...)             // caller's goroutine — NO config access here
	g.Update(func(g *gocui.Gui) error {          // ---- gocui MainLoop goroutine from here ----
		v, err := g.View("cmdline")
		if err != nil { return fmt.Errorf("set focus on cmdline failed: %w", err) }
		width, _ := v.Size()                     // == terminal width: SetView("cmdline", -1, .., maxX, ..)
		v.Clear()
		if _, err := fmt.Fprint(v, composeCmdline(cmdlineTokens(cmdlineCfg), msg, width)); err != nil {
			return fmt.Errorf("print on cmdline failed: %w", err)
		}
		if arm && msg != "" {
			t := time.NewTimer(2 * time.Second)
			go func() {                          // ---- timer goroutine: touches NOTHING shared ----
				<-t.C
				g.Update(func(g *gocui.Gui) error {   // ---- back on the gocui goroutine ----
					v, err := g.View("cmdline")
					if err != nil { return nil }
					width, _ := v.Size()
					v.Clear()
					_, err = fmt.Fprint(v, composeCmdline(cmdlineTokens(cmdlineCfg), "", width))
					return err
				})
				return nil
			}()
		}
		return nil
	})
}
```

Where each read happens:

| Read | Site | Goroutine |
|---|---|---|
| `config.view.Filters`, `config.view.Cols` | inside `writeCmdline`'s `g.Update` closure | gocui MainLoop |
| same, for the timer restore | inside the timer's `g.Update` closure | gocui MainLoop |
| same, for the dialog x0 computation | `dialogOpen` body (a key handler) | gocui MainLoop |
| `config.view.Cols` write | `alignViewToResult` (`stat.go:640`) inside `printStat`'s `g.Update` closure (`stat.go:169`) | gocui MainLoop |
| `config.view.Filters` write | `setFilter` via `dialogFinish` (`dialog.go:127`), new `clearFilters`, `viewSwitchHandler` (`config_view.go:242`) | gocui MainLoop |

⇒ **No cross-goroutine access is introduced, and the existing timer race is removed.** The only
`printCmdline` body that runs off the gocui goroutine (`stat.go:166`, in `doWork`) never touches
`config` — it only formats the message string and enqueues.

### 10.4 Two residual behaviours worth an explicit decision in the tech-spec

- **Stale-timer clobber (pre-existing).** Two messages within 2s arm two timers; the first fires and
  wipes the second message early. Today the same hazard exists (`v.Clear()`). A generation counter
  incremented inside the `g.Update` closure and compared by the restore closure would fix it
  race-free (both sides are on the gocui goroutine). Optional; not required by any AC.
- **Prefix freshness with no message.** The prefix is re-rendered only when something writes to the
  cmdline. Every path that *changes* the filter set already writes a message (`/` → `dialogFinish`
  `dialog.go:152`; `\` → new handler; view switch → `config_view.go:156` / `:299`; menu switch →
  `menu.go:155,175,193,203`). So the indicator refreshes on every state change without a per-tick
  redraw. `printStat` deliberately never touches the cmdline (`stat.go:159-164`) — leave it that way.

---

## 11. Filter-indicator data source — `view.Cols` availability (spec: "issue #99 class" edge case)

### 11.1 What is on `config.view` at key-handler time

`alignViewToResult` (`top/stat.go:635-643`) is the **only** writer of `view.Cols`:

```go
func alignViewToResult(config *config, r stat.PGresult) {
	if config.view.Aligned && len(config.view.ColsWidth) == r.Ncols { return }
	widthes, cols := align.SetAlign(r, 1000, false)
	config.view.Cols = cols          // stat.go:640
	config.view.ColsWidth = widthes  // stat.go:641
	config.view.Aligned = true
}
```

It is called from `printDbstat` (`stat.go:658`), i.e. inside `printStat`'s `g.Update` closure —
gocui goroutine. `config` is shared by pointer (`app.config`, `top/top.go:35`), so the write
persists. **At key-handler time `config.view.Cols` therefore holds the column names of the last
successful render of the current view.** That is exactly what the indicator needs.

### 11.2 The three states the bounds check must survive

| Moment | `config.view.Cols` | `config.view.Filters` | Consequence |
|---|---|---|---|
| **Very first frame, before any render** | `nil`. `view.New()` (`internal/view/view.go:38-361`) never sets `Cols` — the field is absent from all 28 literals; only `ColsWidth: map[int]int{}` and `Filters: map[int]*regexp.Regexp{}` are initialised (e.g. `view.go:47,49`). | non-nil, empty | `filterToken` returns `ok=false` (no filters) → no token. Even if a filter somehow existed, every index would be out of range → skipped. No panic. |
| **After a view switch** | `viewSwitchHandler` (`config_view.go:241-242`) saves the *whole* current `config.view` (including its `Cols`) into `config.views[name]`, then loads `config.views[c]`. So **`Cols` does NOT carry over from the previous screen** — each view carries its own, from *its* last render, or `nil` if that view has never been rendered. `switchViewToProcPidStat` does the same save at `config_view.go:277`. | per-view, preserved | Correct by construction: the names shown always belong to the view whose filters are shown. This is what makes the spec's "индикатор показывает фильтры текущего экрана" true for free. |
| **The one frame after a switch where counts disagree** | `Cols` may still be the *old* view's, because the first `stat.Stat` after a switch can carry the previous view's `Ncols` — the documented issue #99 (`stat.go:629-634`, `Test_alignViewToResult` at `stat_test.go:544`). | new view's | A filter index valid for the new view may exceed `len(Cols)`. **This is the panic path.** |

### 11.3 The required bounds check

Inside `filterToken`, per filter index `i`:

```go
if i < 0 || i >= len(cols) { continue }   // out-of-range filter is omitted from the indicator
if re == nil || re.String() == "" { continue }
names = append(names, cols[i])
```

and iterate over a **sorted** key slice, not the map, so the order is stable across frames.

Degenerate result: if every active filter index is out of range, `names` is empty → return
`ok=false` (no token at all) rather than an empty `[F:]`. This matches the spec: "фильтр с индексом
за его пределами в индикатор не попадает и не роняет отрисовку".

Note the asymmetry to keep in mind: `isFilterRequired` (`stat.go:1145-1152`) tests only `!= nil`,
while `printHeaderCell` (`stat.go:902`) also tests `.String() != ""`. Use the stricter
(`printHeaderCell`) predicate for the indicator so the `*` header marker and the `[F:…]` token can
never disagree — this is round-1 §7 item 9, still open.

---

## 12. Clear-all-filters — the `\` hotkey (spec item 3)

### 12.1 Today

```go
// top/config_view.go:116
func setFilter(answer string, view view.View) string {
	if answer == "\n" || answer == "" {
		delete(view.Filters, view.OrderKey)                       // :119
		return "Filters: regular expression cleared"              // :120  <- lies when nothing was there
	}
	re, err := regexp.Compile(answer)
	if err != nil { return fmt.Sprintf("Filters: %s", err) }      // :126
	view.Filters[view.OrderKey] = re                              // :129
	return "Filters: ok"                                          // :130
}
```

`view` is passed **by value** but `Filters` is a map header, so mutation reaches the caller
(`dialogFinish`, `dialog.go:127`). Reached by `/` (`keybindings.go:60` → `dialogOpen(app,
dialogFilter)`).

### 12.2 New handler

```go
// clearFilters removes every filter of the CURRENT view. It is the counterpart of '/' (hotkey '\'):
// '/' keys on view.OrderKey and can therefore only clear the column it is standing on, which is the
// footgun the persistent indicator makes visible. Reports honestly when there was nothing to clear.
func clearFilters(config *config) func(g *gocui.Gui, _ *gocui.View) error {
	return func(g *gocui.Gui, _ *gocui.View) error {
		n := activeFilterCount(config.view.Filters)
		if n == 0 {
			printCmdline(g, "Filters: no active filters")
			return nil
		}
		// Delete IN PLACE, do not reassign the map: config.view.Filters and
		// config.views[config.view.Name].Filters are the SAME map header (viewSwitchHandler stores the
		// view by value at config_view.go:241). Reassigning would leave the stale map on the stored
		// copy until the next switch.
		for k := range config.view.Filters { delete(config.view.Filters, k) }
		config.viewCh <- config.view
		printCmdline(g, "Filters: cleared %d filter(s)", n)
		return nil
	}
}
```

`printCmdline` is called exactly once per execution path — the convention documented at
`config_view.go:288-290` and `stat.go:159-162`.

`activeFilterCount` shares the `filterToken` predicate (`re != nil && re.String() != ""`), so the
count, the `*` marker and the indicator can never disagree.

### 12.3 Change inside `setFilter`

Yes, `setFilter` itself changes — one branch only:

```go
if answer == "\n" || answer == "" {
	if _, ok := view.Filters[view.OrderKey]; !ok {
		return "Filters: no filter on this column"      // NEW
	}
	delete(view.Filters, view.OrderKey)
	return "Filters: regular expression cleared"        // UNCHANGED — real clear still reports a real clear
}
```

The spec fixes three texts; `"Filters: regular expression cleared"` is not among them and must stay
(the spec only forbids *reporting success when nothing was removed*).

### 12.4 Keybinding and help

- `top/keybindings.go`: insert `{"sysstat", '\\', clearFilters(app.config)},` after line 60 (next to
  `'/'`). Scope **must** be `"sysstat"`, not `""` — `execKeybindings` (`gocui/gui.go:622-636`) runs
  *every* match and `matchView` returns true unconditionally for `""` (`keybinding.go:36-41`), so a
  global binding would fire while a dialog is open and, worse, `onKey` (`gui.go:597-602`) would then
  skip `Editor.Edit`, making `\` untypeable in filter regexes. Unlike `Space`, `\` (0x5C > 0x20)
  arrives as `ev.Ch = '\\'`, `ev.Key = 0`, so the **rune** binding is the correct form here.
- `top/help.go`: the `general actions:` block. Line 21 currently reads
  `Left,Right,<,/    'Left,Right' change column sort, '<' desc/asc sort toggle, '/' set filter.` —
  add a `\` line beneath it (the `[,]` line at `help.go:23` is the [009] precedent). No test asserts
  help content, so this is documentation-only, but it is the sole discovery point (spec AC).

### 12.5 Every test that breaks

| Test | File:line | Why | Fix |
|---|---|---|---|
| `Test_setFilter` | `top/config_view_test.go:333-351` | The table runs **sequentially against one `config.view` with `OrderKey = 0`**. Row 1 `"example"` sets a filter; row 2 `""` deletes it → still `"regular expression cleared"`; **row 3 `"\n"` now finds no filter → returns `"Filters: no filter on this column"`, not `"Filters: regular expression cleared"`** (line 340/`"\n"` case at line 340). Row 4 `"[0-"` unaffected. | Update the `"\n"` row's `want`, and add a comment that the table is state-dependent. Better: restructure into explicit sub-cases (`clear-existing`, `clear-absent`) so the dependency is not implicit. |
| — | — | No other test calls `setFilter`. | — |
| New | — | `clearFilters` needs its own test. It sends on the **unbuffered** `config.viewCh`, so the test must drain from a goroutine — copy the pattern from `Test_scrollOrthogonalToSort` (`config_view_test.go:158-163`) or `Test_changeRefresh` (`:668-673`). Cover: no filters → `"Filters: no active filters"` and **no send on viewCh**; N filters → `"Filters: cleared N filter(s)"` and map emptied in place. | — |

Note the no-filter path returns **before** `config.viewCh <-`, so the test's drain goroutine must
not be armed in that sub-case, or it will hang.

---

## 13. Auto-scroll to the sort column (spec item 1)

### 13.1 Exact insertion point

`renderDbstat` (`top/stat.go:671-693`) today:

```go
func renderDbstat(w io.Writer, config *config, s stat.Stat, termWidth int) error {
	win := visibleColumns(s.Result.Ncols, config.view.ColsWidth, termWidth, config.scrollOffset) // :676
	config.scrollOffset = win.clamped                                                            // :683
	err := printStatHeader(w, s, config, win)                                                    // :686
	...
	return printStatData(w, s, config, isFilterRequired(config.view.Filters), win)               // :692
}
```

Insert **between the function opening (`:671`) and the `visibleColumns` call (`:676`)**:

```go
	// One-shot auto-scroll: a sort-column change asked for the column to be brought into the
	// window. Consumed here (not in the key handler) because column widths are known only after
	// alignViewToResult has run against real data (printDbstat, stat.go:658). Clearing the flag
	// BEFORE recomputing makes it strictly one-shot, so manual [ / ] scrolling afterwards is never
	// undone on the next refresh — the [009] invariant.
	if config.autoScrollToOrderKey {
		config.autoScrollToOrderKey = false
		config.scrollOffset = scrollOffsetFor(
			s.Result.Ncols, config.view.ColsWidth, termWidth,
			config.scrollOffset, config.view.OrderKey)
	}
```

Then `:676` and `:683` stay **byte-identical**. `visibleColumns` re-clamps whatever the helper
produced, and `config.scrollOffset = win.clamped` remains the single write-back — so an
over-optimistic helper result cannot escape.

`renderDbstat`'s signature does not change ⇒ the existing test harness
`renderDbstat(&buf, cfg, makeRenderResult(6, 2), 40)` (`stat_test.go:1069`) keeps working, and
`makeRenderConfig` (`stat_test.go:830-844`) builds `&config{...}` so the new flag defaults to
`false` — **every existing `renderDbstat`/`printStatHeader`/`printStatData` test is unaffected**.

⚠ `printDbstat` returns early on `s.Error != nil` (`stat.go:648-655`) **without** reaching
`renderDbstat`. The flag therefore survives an error frame and fires on the next good frame. That is
the desirable behaviour; state it, do not "fix" it.

### 13.2 The minimum-offset helper, reusing `visibleColumns`

```go
// scrollOffsetFor returns the scroll offset that brings column orderKey into the visible window with
// the SMALLEST movement from the current offset, or offset unchanged when it is already visible.
// Column 0 is frozen and always visible (printStatHeader, stat.go:860-863), so it never scrolls.
//
// It probes visibleColumns rather than reimplementing the walk: marker reservation in both
// directions is the subtle part ([009] ADR "Partial last column + marker reservation in both walk
// directions"), and duplicating it is how the two would drift apart. ncols is at most ~30, so the
// probe is free.
//
// A partially visible column counts as visible — that is the window semantics of [009]
// (countFit, stat.go:784-794), and any stricter notion would fight maxOffset forever.
func scrollOffsetFor(ncols int, colsWidth map[int]int, termWidth, offset, orderKey int) int {
	// Bounds guard, issue #99 class: config.view.Ncols (which orderKeyLeft/Right wrap on,
	// config_view.go:26,38) and s.Result.Ncols can disagree for one frame after a view switch.
	// Clamp against the FRESH data's column count, never the view's.
	if orderKey <= 0 || orderKey >= ncols { return offset }

	win := visibleColumns(ncols, colsWidth, termWidth, offset)
	if orderKey >= win.first && orderKey <= win.last { return win.clamped }   // already visible, no jerk

	if orderKey < win.first {
		return orderKey - 1        // window.first == 1 + clamped, so this puts orderKey exactly at the left edge
	}
	for off := win.clamped + 1; off <= ncols-1; off++ {                        // walk right, stop at the first fit
		if w := visibleColumns(ncols, colsWidth, termWidth, off); orderKey <= w.last {
			return w.clamped
		}
	}
	return ncols - 1               // unreachable in practice; visibleColumns clamps it anyway
}
```

This is a pure function over `(int, map[int]int, int, int, int) → int` — directly unit-testable, as
the spec's testing section requires.

### 13.3 The flag's home and both reset sites

`top/config.go:19-20` gains a third field, next to `scrollOffset` / `verbose`:

```go
	autoScrollToOrderKey bool // One-shot request to bring the sort column into the visible window.
	                          // Set by the sort handlers, consumed by renderDbstat. Ephemeral like
	                          // scrollOffset: reset on BOTH view-switch paths.
```

ADR `[009] Scroll offset on top.config, not on view.View` (`docs/decisions-log.md:478`) settles that
new ephemeral UI state belongs on `config`, not `view.View`. Do not re-litigate.

**Set sites** — after the wrap-around, before the `viewCh` send:
- `orderKeyLeft` — `top/config_view.go:24-29`, insert after `:27`
- `orderKeyRight` — `top/config_view.go:37-42`, insert after `:40`

**Reset sites** — ADR `[009] Reset offset on both view-switch paths` (`decisions-log.md:510`); one
alone misses the other:
- `viewSwitchHandler` — `top/config_view.go:243`, on the line after `config.scrollOffset = 0`
- `switchViewToProcPidStat` — `top/config_view.go:257`, on the line after `app.config.scrollOffset = 0`
  and, critically, **before** the `!app.db.Local` guard at `:259`, so it is reachable in tests
  without a live PostgreSQL (see `Test_switchViewToProcPidStatResetsScrollOffset`,
  `config_view_test.go:219-231`).

**Race:** none. Writer = key handlers (gocui goroutine), reader = `renderDbstat` inside `printStat`'s
`g.Update` closure (gocui goroutine). Same argument as `config.scrollOffset` (`stat.go:678-683`) and
`config.verbose` (`layout.go:117-119`).

### 13.4 `Test_scrollOrthogonalToSort` (`top/config_view_test.go:147-193`) — precisely

**Still asserts, verbatim, no edit needed to pass:**
- lines 150-170, `scroll-keeps-sort-0/1`: `scrollLeft`/`scrollRight` leave `OrderKey == 7`. Untouched.
- lines 172-192, `sort-keeps-scroll-0/1`: `assert.Equal(t, 3, config.scrollOffset)` at **line 189**
  **still passes** — the handler sets a *flag*, it does not write `scrollOffset`. The write happens
  later, in `renderDbstat`, which this test never calls.

**No longer asserts (the test's meaning shrinks):**
- The comment at lines 145-146 — *"scroll and sort are independent: … sort handlers do not mutate
  scrollOffset"* — becomes **misleading**. Sorting is now only *deferredly* orthogonal: the next
  render will move the window. The comment must be amended, or the test silently claims a property
  the feature deliberately removed.
- Recommended addition inside `sort-keeps-scroll-*`: `assert.True(t, config.autoScrollToOrderKey)`
  — this pins the new contract ("the handler defers, it does not scroll") in the very test whose
  guarantee was weakened.

**Other tests:** `Test_printDbstat_clampsScrollOffset` (`stat_test.go:1060`),
`Test_render_widePartialLastColumn` (`:807`), `Test_printStatData_windowed_midOffset` (`:872`),
`Test_printStatHeader_*`, `Test_render_alignmentInvariant` (`:972`) — all build their config via
`makeRenderConfig`, so the flag is `false` and behaviour is unchanged. `Test_visibleColumns`
(`:599`) and `Test_visibleColumns_maxOffsetReachesLastColumn` (`:766`) untouched — `visibleColumns`
itself does not change.

---

## 14. Refresh interval in the header (spec item 4)

### 14.1 Why a new field is unavoidable

`view.Refresh` (`internal/view/view.go:25`) is a **transient courier**: both writers set it, send it
on `viewCh`, then immediately zero it — `top/ui.go:82-86` and `top/config_view.go:454-456`. The
reset is load-bearing: `collectStat`'s branch order (`top/stat.go:86-96`) treats any non-zero
`Refresh` on an incoming view as "the refresh changed" and `continue`s **before** the
ShowExtra/Verbose/CollectExtra branches. Leaving it populated would make every subsequent view
update look like a refresh change and silently skip extra-stats and verbose handling — the comment
at `stat.go:86-90` says the order is load-bearing. **At rest `config.view.Refresh == 0` always.**

The only durable copy today is `collectStat`'s local `refresh` (`stat.go:39`, updated at `:93-95`),
which lives in the collector goroutine and is unreachable from the render path.

### 14.2 The change set

**New field** — `top/config.go`, after `verbose` (`:20`):

```go
	refresh time.Duration // Current stats refresh interval, mirrored for display in the sysstat
	                      // header. view.Refresh is a transient courier (zeroed right after the
	                      // viewCh send, ui.go:86 / config_view.go:456), so it cannot be read back.
```

`top/config.go` must gain the `"time"` import.

**Initialisation** — `app.setup()`, `top/top.go:69`, immediately after
`app.config.view = app.config.views["activity"]`:

```go
	app.config.refresh = defaultRefresh
```

`setup()` runs from `RunMain` (`top.go:24`) **before `mainLoop` starts any goroutine**, so this is
definitively race-free. Add `const defaultRefresh = time.Second` and use it at **both** `top.go:69`
and `top/ui.go:82`, so the displayed value and the value actually sent to the collector cannot drift.

⚠ **Do not** initialise it at `ui.go:82` and **do not** make `ui.go:82` read `config.refresh`: that
line runs in the `doWork` goroutine, and `config.refresh` is written by `changeRefresh` on the gocui
goroutine — that would be a genuine cross-goroutine access under `-race`. (Round-1 §7 item 5 notes
that `ui.go:82-86` already has a narrow pre-existing race of this shape; do not widen it.)
`top/top.go` must gain the `"time"` import.

**`changeRefresh`** — `top/config_view.go:438-459`. Add one line next to `:454`:

```go
	config.view.Refresh = time.Duration(interval) * time.Second   // :454, unchanged
	config.refresh = config.view.Refresh                          // NEW — the durable display copy
	config.viewCh <- config.view                                  // :455, unchanged
	config.view.Refresh = 0                                       // :456, unchanged — DO NOT touch
```

Placement matters only in that it must be inside the success branch (after the 1..300 validation at
`:448-450`), so an invalid input never changes the displayed value.

**Race check:** `changeRefresh` is reached from `dialogFinish` (`dialog.go:147`), a gocui-goroutine
key handler. `renderSysstat` runs inside `printStat`'s `g.Update` closure (`stat.go:169-175`), also
gocui goroutine. Writer and reader are the same goroutine ⇒ **no cross-goroutine access is
introduced.**

**Signatures:**

```go
// top/stat.go:258
func printSysstat(v *gocui.View, s stat.Stat, verbose bool, local bool, dataDir string, refresh time.Duration) error
// top/stat.go:268
func renderSysstat(w io.Writer, s stat.Stat, verbose bool, local bool, dataDir string, refresh time.Duration) error
```

One production call site: `top/stat.go:175` →
`printSysstat(v, s, app.config.verbose, app.db.Local, props.DataDirectory, app.config.refresh)`.

**Line 1 format** — `top/stat.go:272-274`:

```go
	fmt.Fprintf(w, "pgcenter: %s, refresh: %ds, load average: %.2f, %.2f, %.2f\n",
		time.Now().Format("2006-01-02 15:04:05"), int(refresh/time.Second), ...)
```

⚠ **Do not use `refresh.String()`.** `time.Duration(300*time.Second).String()` is `"5m0s"`, and
`60s` is `"1m0s"`. The spec's `refresh: 300s` requires explicit seconds formatting.

Width, verified: today's line 1 is **61** chars; with `refresh: 300s` it is **76** — matching the
spec's numbers exactly, and still shorter than the panel's longest line (`%cpu`, 80). The sysstat
panel is `(maxX-1)/2` wide (`ui.go:122`), so the truncation threshold does not move.

### 14.3 Full list of test call sites to update

`renderSysstat` — 4 literal call sites in `top/stat_test.go`:

| Line | Test / helper | Note |
|---|---|---|
| 59 | `Test_renderSysstat_compact` | also the assertion change, below |
| 150 | helper `verboseSysstatLines` | absorbs 5 tests: `:157`, `:180`, `:203`, `:235`, `:258` |
| 284 | `Test_renderSysstat_compactUnchanged` (compact) | |
| 285 | `Test_renderSysstat_compactUnchanged` (verbose) | |

`printSysstat` — **no test call sites** (`grep -n "printSysstat(" top/*_test.go` → empty).

**Assertion that breaks:** `top/stat_test.go:65-67`, the line-1 regex

```
^pgcenter: \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}, load average: 1\.23, 0\.45, 6\.78$
```

must become `…, refresh: \d+s, load average: …`. Update it deliberately — do not relax it to a
prefix match.

**Assertions that stay green:** every line-count assertion —
`stat_test.go:63` (4 compact), `:168`, `:217`, `:226`, `:244`, `:266` (7 verbose), `:291` (4), `:293`
(7) — because the interval goes on the **existing** line 1 and adds no row. This is the spec AC
"число строк шапки не меняется: 4 в компактном режиме, 7 в verbose".

`Test_changeRefresh` (`top/config_view_test.go:662-699`) still passes verbatim: the valid case
(`:675`) asserts the return string and the value seen on `viewCh`; the invalid cases (`:693-697`)
assert `config.view.Refresh` is untouched — the new `config.refresh` write only happens on the
success path. **Worth adding:** `assert.Equal(t, 5*time.Second, config.refresh)` in the valid case,
so the display copy is pinned.

---

## 15. Verbose blank line (spec item 6)

### 15.1 The fix is `dbstatY0 = tallest`, confirmed

`top/layout.go:33-62`. gocui geometry: `SetView(name, x0, y0, x1, y1)` gives a **content area** of
`(x1-x0-1) × (y1-y0-1)` starting at screen cell `(x0+1, y0+1)` — `newView` + `Size()`
(`gocui/view.go:98-116`), and `setRune` clips to `Size()` (`view.go:126-130`). `Frame = false` (set
for all four views in `layout()`) suppresses the border **drawing** but does not change the
content-area arithmetic. This is the same fact `printDbstat` already relies on (`stat.go:661`).

**Compact** (`layout.go:34-36`, `tallest = 4`):

| View | y0 | y1 | content rows |
|---|---|---|---|
| sysstat | −1 | 4 | 0…3 (4 rows — matches the 4 compact lines) |
| pgstat | −1 | 4 | 0…3 (4 rows) |
| cmdline | 3 | 5 | **4** (1 row) |
| dbstat | 4 | maxY−1 | 5…maxY−2 |

No gap: cmdline row 4, dbstat starts at row 5. Note the compact literals already satisfy
`cmdlineY0 = tallest-1`, `cmdlineY1 = tallest+1`, **`dbstatY0 = tallest`**.

**Verbose today** (`layout.go:39-50`, `sysstatY1 = 7`, `pgstatY1 = 9`, `tallest = 9`, `bandTop = 8`):

| View | y0 | y1 | content rows |
|---|---|---|---|
| sysstat | −1 | 7 | 0…6 (7 rows = 4 + `sysstatVerboseExtra` 3) |
| pgstat | −1 | 9 | 0…8 (9 rows = 4 + `pgstatVerboseExtra` 5) |
| cmdline | 8 | 10 | **9** (1 row) |
| dbstat | **10** | maxY−1 | **11**…maxY−2 |

⇒ **row 10 is drawn by nobody.** That is the wasted line the spec reports. The generic formula
(`dbstatY0 = tallest`) was applied to compact but written as `tallest + 1` for verbose
(`layout.go:50`).

**Verbose after `dbstatY0 = tallest`:**

| View | y0 | y1 | content rows |
|---|---|---|---|
| sysstat | −1 | 7 | 0…6 |
| pgstat | −1 | 9 | 0…8 |
| cmdline | 8 | 10 | 9 |
| dbstat | **9** | maxY−1 | **10**…maxY−2 |

**Overlap proof, both modes.** With `bandTop = tallest − 1`, `cmdlineY0 = bandTop`,
`cmdlineY1 = bandTop + 2`, `dbstatY0 = tallest`:

- cmdline's only content row is `cmdlineY1 − 1 = tallest`.
- dbstat's first content row is `dbstatY0 + 1 = tallest + 1`. **No overlap** — dbstat starts exactly
  one row below cmdline, no gap.
- pgstat's last content row is `pgstatY1 − 1 = tallest − 1` (when pgstat is the taller panel), i.e.
  one row above cmdline. **No overlap.**
- cmdline's *frame* row `cmdlineY0 = tallest − 1` coincides with pgstat's last content row, but
  `cmdline.Frame = false` (`ui.go:158-160`) so nothing is drawn there — and this is unchanged from
  today, and identical in compact (`cmdlineY0 = 3` overlaps sysstat/pgstat content row 3, documented
  at `layout.go:8`).
- sysstat is the shorter panel in verbose (content 0…6); rows 7…8 under it are simply blank on the
  left half while pgstat fills the right half. Pre-existing and intended (the panels are
  side-by-side, `sysstat` is `(maxX-1)/2` wide, `pgstat` starts at `maxX/2`).

### 15.2 New height-guard threshold

`layout.go:57`: `if dbstatY0 + minDbstatRows > maxY-1 { …compact fallback… }` with
`minDbstatRows = 2` (header + ≥1 data row). With `dbstatY0` dropping 10 → 9:

```
9 + 2 <= maxY - 1   ⇔   maxY >= 12
```

⇒ **verbose expands from 12 terminal rows** (was 13). This is exactly the spec's accepted side
effect ("Порог verbose опускается с 13 до 12 строк"). No change to the guard *expression* is needed
— only to `dbstatY0`; the threshold moves as a consequence.

### 15.3 Exactly which cases in `top/layout_test.go` change

| Case | File:line | Today | After |
|---|---|---|---|
| `verbose` | `layout_test.go:37-40` | `maxY:50`, `dbstatY0: 10` | `dbstatY0: 9` |
| `boundary-expands` | `:53-57` | `maxY: 13`, `dbstatY0: 10`, `expanded: true` | `maxY: 12`, `dbstatY0: 9`, `expanded: true` |
| `boundary-fallback` | `:58-62` | `maxY: 12`, compact, `expanded: false` | `maxY: 11`, compact, `expanded: false` |
| `compact` | `:31-35` | unchanged | unchanged |
| `height-guard` | `:41-46` | `maxY: 5` → compact | unchanged (still well below) |
| `verbose-zero-maxY` | `:47-52` | `maxY: 0` → compact | unchanged |

Also update the test's own doc comment (`layout_test.go:16-18`), which spells out
`dbstatY0 = 10` and `maxY >= 13`, and the function doc comment (`layout.go:29-32`).

No other test touches `topBandLayout`. `top/verbose_test.go` covers `toggleVerbose` only.

---

## 16. Bold in verbose sections (spec item 7)

### 16.1 The convention to match

Base rows wrap **numeric values** in `\033[37;1m … \033[0m` (SGR 37 = white fg, 1 = bold), e.g.
`stat.go:280` (`%cpu`), `:288` (mem), `:296` (swap), `:472` (activity), `:481` (autovacuum), `:489`
(statements). Base line 1 of each panel (`stat.go:272` and the `formatInfoString` line `:466`) has
**no** bold at all. Composite `A/B` values are wrapped as a **single span**:
`\033[37;1m%2d/%d\033[0m workers/max` (`stat.go:481`), `\033[37;1m%3d/%d\033[0m conns` (`:472`).

Recommended helper (single point, keeps the sequences from being retyped 30 times):

```go
// bold wraps a rendered value in the same SGR sequence the compact summary rows use
// (top/stat.go:280 etc.). Kept as a helper so the n/a sentinels can stay deliberately unwrapped:
// bold must read as "there is a real number here".
func bold(s string) string { return "\033[37;1m" + s + "\033[0m" }
```

### 16.2 Every value site in `renderSysstatVerbose` (`top/stat.go:330-388`)

| Line | Row / branch | Value expression | Wrap? |
|---|---|---|---|
| 337 | iostat, degraded branch | `pretty.ReserveWidth(activeDiskCount(s.Diskstats), 2)` | **yes** — a real count even in this branch |
| 337 | iostat, degraded branch | the five `naLiteral` args | **no** |
| 343 | iostat, value branch | `pretty.ReserveWidth(activeDiskCount(...), 2)` | yes |
| 344 | iostat, value branch | `pretty.ReserveWidth(pretty.Ceil(d.Util), 3)` | yes (the `%%` stays outside the span) |
| 345 | iostat, value branch | `pretty.RateUnitPrefixed(d.Rsectors, …, "r", 4)` | yes — see caveat §16.5 |
| 346 | iostat, value branch | `pretty.ReserveWidth(pretty.Ceil(d.Rcompleted), 5)` | yes |
| 347 | iostat, value branch | `pretty.RateUnitPrefixed(d.Wsectors, …, "w", 4)` | yes — caveat |
| 348 | iostat, value branch | `pretty.ReserveWidth(pretty.Ceil(d.Wcompleted), 5)` | yes |
| 357 | nicstat, degraded branch | `pretty.ReserveWidth(activeNetCount(s.Netdevs), 2)` | yes |
| 357 | nicstat, degraded branch | the four `naLiteral` args | **no** |
| 363 | nicstat, value branch | `pretty.ReserveWidth(activeNetCount(...), 2)` | yes |
| 364 | nicstat, value branch | `pretty.ReserveWidth(pretty.Ceil(n.Utilization), 3)` | yes |
| 365 | nicstat, value branch | `pretty.RateUnitPrefixed(n.Rbytes/1024/128, …)` | yes — caveat |
| 366 | nicstat, value branch | `pretty.RateUnitPrefixed(n.Tbytes/1024/128, …)` | yes — caveat |
| 367-368 | nicstat, value branch | `ReserveWidth(Ceil(n.Rerrs+n.Terrs), 4)` **/** `strconv.Itoa(Ceil(n.Tcolls))` | yes — wrap the `%s/%s` composite as ONE span, matching `stat.go:472/481` |
| 377 | filesyst, value branch | `fs.Mount.Device`, `truncate(fs.Mount.Mountpoint, 10)`, `fs.Mount.Fstype` | **judgement call** — these are identifiers, not numbers. Base rows only bold numbers. Recommend leaving them plain and flagging the choice in the tech-spec. |
| 378 | filesyst, value branch | `pretty.Size(fs.Size)`, `pretty.Size(fs.Used)`, `fs.Pused` (`%3.0f`) | yes |
| 382 | filesyst, degraded branch | `naLiteral` | **no** |

### 16.3 Every value site in `renderPgstatVerbose` (`top/stat.go:528-607`)

| Line | Row | Value expression | Wrap? / how |
|---|---|---|---|
| 533-535 | workload | seven `naInt(...)` calls | **Do not wrap at the call sites.** Move `bold` *inside* `naInt` (`stat.go:519-524`): `if !hasPrev { return naReserve(width) }` (unwrapped) `; return bold(pretty.ReserveWidth(int(v), width))`. This keeps all seven call sites untouched **and** makes "n/a stays unwrapped" structurally impossible to get wrong. |
| 543 | databases | `size = pretty.SizeWidth(float64(o.TotalSize), sizeFieldWidth)` | yes — wrap only here; the `naReserve` default at `:541` stays plain |
| 545 | databases | `growth = pretty.SizeWidth(float64(o.GrowthPerSec), sizeFieldWidth)` | yes — same pattern |
| 553 | databases | `hit = fmt.Sprintf("%6.2f%%", o.CacheHitRatio)` | yes — `naReserve(cacheHitWidth)` at `:551` stays plain |
| 556 | databases | `pretty.ReserveWidth(int(o.DatabasesCount), 2)` | yes (always real) |
| 562-564 | workers | three `pretty.ReserveWidth(...)` + their `props.GucMax*` denominators | wrap each `%s/%d` composite as ONE span (matches `stat.go:481`) |
| 572 | replication | `lag = pretty.SizeWidth(...)` | yes; `naReserve` at `:570` plain |
| 576 | replication | `retain = pretty.SizeWidth(...)` | yes; `naReserve` at `:574` plain |
| 580 | replication | `backlog = pretty.SizeWidth(...)` | yes; `naReserve` at `:578` plain |
| 583 | replication | `pretty.Size(float64(o.WalSize))` | yes |
| 584 | replication | `pretty.ReserveWidth(int(o.SlotsCount), 2)` | yes (the `%s/%s` slots/retain composite mixes an always-real count with a possibly-n/a size — wrap the two **separately**, not as one span) |
| 585 | replication | `o.Senders`, `o.Receivers` (`%d/%d`) | yes, one span |
| 596-598 | bgwr/ckpt | `writeMs`, `syncMs`, `maxw` — assigned inside `if hp` | wrap **only inside the `if hp` branch**; the `naLiteral` defaults at `:594` stay plain |
| 601 | bgwr/ckpt | `pretty.ReserveWidth(int(o.CkptTimed), 2)` / `strconv.Itoa(int(o.CkptReq))` | yes, one span for the `%s/%s` composite |

### 16.4 Sentinels stay unwrapped — confirmed reachable

- `naLiteral` (`stat.go:314`) — used bare at `stat.go:337, 357, 382, 594`. All in degraded branches;
  none is wrapped by the plan above.
- `naReserve(width)` (`stat.go:509-514`) — used at `:541` (×2), `:551`, `:570`, `:574`, `:578`, and
  inside `naInt`. All are *default* assignments overwritten only in the `Valid`/`hasPrev` branch, so
  wrapping the overwrite leaves the sentinel plain automatically.
- `naInt` (`stat.go:519-524`) — the one helper that must be edited, precisely so the sentinel path
  returns `naReserve(width)` unwrapped while the value path returns `bold(...)`.

### 16.5 Caveats

- **`pretty.RateUnitPrefixed` returns value *and* unit as one string** (`internal/pretty/pretty.go:78`
  → `rateUnitParts`, `:87`). The golden `"1135 rMB/s"` shows the unit is baked in. Wrapping the
  return value therefore bolds the unit too — unlike the compact rows, where units sit outside the
  span. Options: (a) accept it (simplest, visually still "a bold number followed by a unit"); (b)
  export a parts-returning variant. **Recommend (a)**, stated explicitly as a decision.
- **Truncation / colour leak.** The sysstat and pgstat panels are half-width, so long lines are
  already truncated mid-line today (base line 2, `%cpu`, is 80 visible chars). A truncated line can
  cut between `\033[37;1m` and `\033[0m`. This is **not a new class of problem** — the compact rows
  have had it since forever — but it becomes more frequent. gocui's escape interpreter handles both
  sequences in `OutputNormal` (used by every existing bold row), so nothing new is required.
- **Visible width is unaffected.** SGR sequences are zero-width on screen, so the `naReserve` /
  `ReserveWidth` alignment invariants are preserved *visually*. They are **not** preserved
  *byte-wise* — which is what breaks the tests below.

### 16.6 Exactly which `top/stat_test.go` assertions break

**Full-line goldens — must gain the escape sequences (10 strings):**

| Line | Test |
|---|---|
| 171-173 | `Test_renderSysstat_verboseIostatMaxUtil` (iostat row) |
| 194-196 | `Test_renderSysstat_verboseNicstatConversion` (nicstat row) |
| 248-250 | `Test_renderSysstat_verboseFilesystMounted10` (filesyst row) |
| 336-338 | `Test_renderPgstat_verboseNA` (workload) — n/a fields stay plain, but nothing else on the row is a value, so this one may survive unchanged; verify |
| 339-341 | `Test_renderPgstat_verboseNA` (databases) — `DatabasesCount` becomes bold ⇒ **breaks** |
| 342-344 | `Test_renderPgstat_verboseNA` (workers) ⇒ breaks |
| 345-347 | `Test_renderPgstat_verboseNA` (replication) — `wal size`, slots count, senders/receivers ⇒ breaks |
| 348-350 | `Test_renderPgstat_verboseNA` (bgwr/ckpt) — `12/3 timed/req` ⇒ breaks |
| 378-380, 384-386, 387-389, 390-392, 393-395 | `Test_renderPgstat_verboseAvailable`, all five rows ⇒ break |

**Substring assertions that break:**

| Line | Assertion | Why |
|---|---|---|
| `stat_test.go:229` | `assert.Contains(t, lines[4], "0% max util")` | becomes `…  0\033[0m% max util` — the reset sits between the digit and the `%`. **Breaks.** |
| `:251` | `assert.NotContains(t, lines[6], "/var/lib/postgresql")` | survives (mountpoint left plain per §16.2) |
| `:252` | `assert.NotContains(t, lines[6], "75")` | survives |
| `:218-219`, `:227-228` | `Contains`/`NotContains` on `"n/a"` | **survive** — sentinels stay plain |
| `:397` | `assert.NotContains(t, buf.String(), "n/a")` | survives |
| `:267` | `assert.Equal(t, "filesyst: n/a", lines[6])` | **survives** — pure degraded branch, nothing wrapped |
| `:427-428`, `:437-438` | `Contains(valRows[5], "100.00% cache hit ratio")`, `Contains(valRows[4], " 999 tps")` | **break** — the value is now bold, so the substring is split by `\033[0m` |

**⚠ The width/alignment assertions ARE affected — this is the non-obvious one.**
`Test_renderPgstat_verboseNAWidthStatic` (`stat_test.go:406-509`) proves alignment by comparing
**byte offsets** via `strings.Index`. Two groups behave differently:

- **Group (a)**, `:499-502` — value sample A vs value sample B. Both are bolded identically, so the
  byte offsets still match. **Survives.**
- **Group (b)**, `:429-432` (cache hit ratio), `:439-442` (tps), `:504-507` (all five Size fields) —
  **value state vs n/a state**. The value gains 12 bytes (`\033[37;1m` = 8, `\033[0m` = 4) that the
  n/a sentinel does not. **All of these fail.**

The honest fix is to make the invariant explicit: the test must compare **visible** offsets, i.e.
strip SGR before indexing.

```go
// stripSGR removes ANSI SGR sequences so an assertion can measure the VISIBLE column of a label.
// The alignment invariant is about screen columns; the escape sequences are zero-width.
var sgrRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)
func stripSGR(s string) string { return sgrRe.ReplaceAllString(s, "") }
```

then index `stripSGR(valRows[5])` / `stripSGR(naRows[5])`. That preserves the regression this test
was written for (tech debt `[012]`, resolved 2026-06-25) rather than deleting it — which is what
"обновлять их осознанно, а не подгонять под фактический вывод" means here.

**Line-count assertions** (`:168`, `:192`, `:217`, `:226`, `:244`, `:266`, `:293`, `:329`, `:375`,
`:530`) all survive — bold adds no rows.

**`Test_renderSysstat_compactUnchanged` (`:275`) / `Test_renderPgstat_compactUnchanged` (`:514`)**
survive: they compare the compact prefix, which is unchanged (bold is verbose-only). But
`renderSysstat`'s signature change (§14) makes `:284-285` compile errors — mechanical.

---

## 17. Dialog geometry, both axes (spec item 7)

### 17.1 Today

```go
// top/dialog.go:76-95
maxX, _ := g.Size()
v, err := g.SetView("dialog", len(prompt)-1, 3, maxX-1, 5)     // :79
...
p, err := g.View("cmdline")                                    // :87
_, err = fmt.Fprint(p, prompt)                                 // :92  <- raw append, bypasses printCmdline
```

The literals `3` and `5` are exactly `compactCmdlineY0` / `compactCmdlineY1` (`layout.go:8-9`), so
in compact the dialog's content row is `5-1 = 4` — the same row as cmdline's content row — and the
dialog view is drawn on top because it is appended to `g.views` later (`gocui/gui.go:150`). It works
by coincidence of hard-coded numbers.

### 17.2 The x-axis: a pure function with a clamp

`SetView` rejects `x0 >= x1` with `errors.New("invalid dimensions")` — `gocui/gui.go:130-133`,
verified in the module source. Usable width of the dialog is `x1 - x0 - 1` (`view.go:114-116`).

```go
// minDialogInputWidth is the minimum number of usable columns the dialog input field must keep on
// screen. gocui usable width is x1-x0-1, so x0 is clamped to guarantee this many columns even when
// the composed cmdline line is longer than the terminal. 10 columns fit a PID, a refresh value, a
// y/n answer and a short regexp; a longer regexp scrolls within gocui's editor.
const minDialogInputWidth = 10

// dialogInputX0 computes the x0 of the dialog input view from the printed (RUNE, not byte) length
// of the composed cmdline line and the terminal width. x1 is maxX-1. The result is clamped into
// [-1, maxX-2-minDialogInputWidth] so SetView can never see x0 >= x1 — which returns "invalid
// dimensions", propagates out of the key handler through onKey -> handleEvent -> MainLoop
// (gocui/gui.go:593-595, 373-375), and makes top's mainLoop tear down and recreate the entire UI
// (top/ui.go:45-67). Pure integer arithmetic; unit-testable.
func dialogInputX0(cmdlineLen, maxX, minWidth int) int {
	x0 := cmdlineLen - 1                       // reproduce today's "start right after the text"
	if hi := maxX - 2 - minWidth; x0 > hi { x0 = hi }
	if x0 < -1 { x0 = -1 }
	return x0
}
```

⚠ `cmdlineLen` must be a **rune** count (`utf8.RuneCountInString`), not `len()`: the truncation
ladder emits `…` (3 bytes, 1 column) and the header emits `‹`/`›`. Byte length would push the field
right and, on a truncated line, past the clamp's intent.

**Degradation to state explicitly:** when the clamp bites, the input box no longer starts *after*
the prompt — it overlays the prompt's tail (the dialog view is drawn on top of cmdline). The user
sees a truncated prompt plus a usable field. That is the correct trade (a rendered dialog beats a
UI restart), but it means the spec AC *"Приглашение и поле ввода выровнены по горизонтали"* cannot
hold in the clamped case — see §19.4.

### 17.3 The y-axis: `topBandLayout` is reachable from `dialogOpen`

`dialogOpen(app *app, d dialogType) func(g *gocui.Gui, _ *gocui.View) error` (`dialog.go:46`) closes
over **`app`**, so `app.config.verbose` is available; `g.Size()` gives `maxY`. Confirmed — no
plumbing needed.

```go
	maxX, maxY := g.Size()
	_, _, cmdlineY0, cmdlineY1, _, _ := topBandLayout(app.config.verbose, maxY)

	line := composeCmdline(cmdlineTokens(app.config), prompt, maxX)
	x0 := dialogInputX0(utf8.RuneCountInString(line), maxX, minDialogInputWidth)

	v, err := g.SetView("dialog", x0, cmdlineY0, maxX-1, cmdlineY1)   // replaces dialog.go:79
	...
	printCmdlinePersist(g, "%s", prompt)                              // replaces dialog.go:92
```

`topBandLayout` returns `(sysstatY1, pgstatY1, cmdlineY0, cmdlineY1, dbstatY0, expanded)` —
`layout.go:33`. In compact it returns `3, 5` for the cmdline pair, so **the compact path is
byte-identical to today's literals**: zero behavioural change where things already work. `layout()`
computes the geometry from the *same* `(app.config.verbose, maxY)` inputs (`ui.go:119`), so the two
cannot disagree within a frame.

Both reads (`app.config.verbose`, `app.config.view.Filters/Cols`) happen in the gocui goroutine —
`dialogOpen` is a key handler.

Note `composeCmdline` is called twice per open (once here for the length, once inside
`printCmdlinePersist`'s closure). Same goroutine, same inputs, same result — acceptable; the
alternative is to have the writer return the composed length, which cannot work because `g.Update`
is asynchronous.

### 17.4 Failure mode — confirmed end to end

1. `g.SetView` returns `errors.New("invalid dimensions")` when `x0 >= x1` — `gocui/gui.go:130-133`.
2. `dialogOpen` (`dialog.go:80-85`) checks only for `gocui.ErrUnknownView`, so anything else is
   returned: `fmt.Errorf("set dialog view on layout failed: %w", err)`.
3. That error is a keybinding-handler return → `execKeybindings` → `onKey` returns it
   (`gui.go:593-595`) → `handleEvent` → **`MainLoop` returns it** (`gui.go:373-375`).
4. `top/ui.go:45-61`: the error is stashed in `app.uiError`, the error-rate check runs (5 errors /
   1 s), `cancel()`, `wg.Wait()`, and the loop **rebuilds the whole Gui** — every view is destroyed
   and recreated, `doWork` and `collectStat` restart with a fresh `statCh`.

### 17.5 The `n` claim, verified — and every other prompt at risk

Prompt lengths (`dialogPrompts`, `dialog.go:29-40`), and the terminal width below which
`x0 >= x1` today (`x0 = len(prompt)-1`, `x1 = maxX-1` ⇒ breaks when `maxX <= len(prompt)`):

| Dialog | Key | Prompt len | **Breaks at maxX ≤** | Unusable (< 10 usable cols) at maxX ≤ |
|---|---|---|---|---|
| `dialogSetMask` | `n` | **93** | **93** | 103 |
| `dialogTerminateGroup` | `K` | 60 | 60 | 70 |
| `dialogCancelGroup` | `k` | 56 | 56 | 66 |
| `dialogChangeAge` | `A` | 42 | 42 | 52 |
| `dialogChangeRefresh` | `z` | 35 | 35 | 45 |
| `dialogPgReload` | `R` | 34 | 34 | 44 |
| `dialogQueryReport` | `G` | 19 | 19 | 29 |
| `dialogTerminateBackend` | `_` | 18 | 18 | 28 |
| `dialogCancelQuery` | `-` | 15 | 15 | 25 |
| `dialogFilter` | `/` | 12 | 12 | 22 |

**Claim confirmed.** On an 80-column terminal, `n` gives `x0 = 92`, `x1 = 79` ⇒ `92 >= 79` ⇒
"invalid dimensions" ⇒ full UI recreate. It is reproducible **at startup with no setup**: the
default view is `activity` (`top/top.go:69`) and `dialogSetMask`'s view guard (`dialog.go:51-53`)
passes there. This is a live bug on `develop`, independent of this feature.

`k` and `K` break at ≤ 56 / ≤ 60 columns, and `A`/`z`/`R` on genuinely narrow terminals; the
persistent prefix lowers every one of those thresholds by the prefix width (e.g. `[F:datname]` = 11
columns + a space ⇒ `/` would break at maxX ≤ 24 instead of ≤ 12). The clamp closes all of them at
once.

### 17.6 Known limitation to record

The dialog view is positioned once, at open time. If the terminal is resized (or `v` is pressed —
though `sysstat` is not the current view while a dialog is open, so `v` cannot fire) while a dialog
is open, `layout()` re-runs and moves `cmdline`, but the `dialog` view keeps its old coordinates.
Pre-existing; out of scope; worth one line in the tech-spec.

Also, `dialogFinish`'s `strings.TrimPrefix(v.Buffer(), dialogPrompts(app.config.dialog))`
(`dialog.go:118`) is **dead code**: the prompt is written into the *cmdline* view, while `v` is the
*dialog* view, whose buffer contains only what the user typed. Harmless — do not "fix" it as a
drive-by, and do not rely on it.

---

## 18. Wave plan input — dependencies (spec item 9)

### 18.1 Dependency graph

```
[6] verbose blank line (layout.go)  ─────────────┐
                                                 ├──> [8y] dialog vertical axis
[1] cmdline composer + [2] indicator ────────────┼──> [8x] dialog horizontal axis
                                                 │
[3] clear-all-filters `\`  ──(AC verification only)┘
[4] auto-scroll        ── independent
[5] refresh in header  ── independent
[7] bold in verbose    ── independent
```

**Yes, the dialog-geometry fix depends on the cmdline composer landing first** — but only its
horizontal half. `dialogOpen` needs `composeCmdline` + `cmdlineTokens` to compute the real cmdline
length, and it needs `printCmdlinePersist` to replace the raw `fmt.Fprint` at `dialog.go:92`. The
vertical half depends instead on **item 6**, because it consumes `topBandLayout`'s `cmdlineY0/Y1`
and those values are wrong in verbose until `dbstatY0 = tallest` lands (strictly: `cmdlineY0/Y1`
themselves are already correct today; the coupling is that both changes edit `layout.go`/its test
and both are validated by the same verbose stand run — a sequencing convenience, not a hard
dependency).

**Does auto-scroll touch anything the indicator touches?** Almost nothing:

| | Auto-scroll [4] | Indicator [1]+[2] |
|---|---|---|
| `config` struct | adds `autoScrollToOrderKey` | adds nothing (uses `config.view`) |
| `config.view.ColsWidth` | reads | — |
| `config.view.Cols` | — | **reads** |
| `config.view.OrderKey` | reads | — |
| `config.view.Filters` | — | reads |
| `config.scrollOffset` | writes (render) | — |
| `top/config_view.go` | `orderKeyLeft/Right`, `viewSwitchHandler`, `switchViewToProcPidStat` | — (item 3 touches `setFilter` here) |
| `top/stat.go` | `renderDbstat` + new `scrollOffsetFor` | — |
| `top/ui.go` | — | `printCmdline` |

The only overlap is textual (both add a field to `config.go`; items 3 and 4 both edit
`config_view.go` in different functions). **No semantic coupling** — they are safe to develop in
parallel and merge with trivial conflict resolution.

### 18.2 Suggested waves

| Wave | Items | Files | Rationale |
|---|---|---|---|
| **1** | [4] auto-scroll, [5] refresh header, [6] verbose blank line, [7] bold | `config.go`, `config_view.go` (order handlers + switch), `stat.go`, `top.go`, `layout.go`, `stat_test.go`, `layout_test.go`, `config_view_test.go` | Four fully independent changes, each with its own pure function and its own test file region. The only shared file is `stat.go` ([4] in `renderDbstat`, [5] in `renderSysstat`, [7] in the two verbose renderers) — three disjoint regions. |
| **2** | [1] cmdline composer + [2] indicator, [3] clear-all-filters `\` | `ui.go`, `top.go` (ambient set), new pure funcs, `config_view.go` (`setFilter`, `clearFilters`), `keybindings.go`, `help.go`, `dialog.go:92` | [3]'s ACs ("индикатор гаснет") are only verifiable once [1]+[2] exist; [3]'s handler is otherwise independent and could move to wave 1 if the wave gets fat. |
| **3** | [8] dialog geometry, both axes | `dialog.go` | Hard dependency on wave 2 (composer + persist writer) and soft on wave 1 ([6], for verbose stand verification). |

---

## 19. What the code will not support, and round-1 corrections (spec item 10)

### 19.1 Round-1 factual errors

1. **"26 printCmdline call sites" → there are 44.** Round-1 §4.1 listed a partial set and missed
   `menu.go` (5), `pgconfig.go` (6), `reset.go` (1), `signal.go` (1) and `extra.go:77`. The spec's
   Risks section (`015-feat-tui-papercuts.md:319`) repeats the 26 figure and should be corrected —
   the mitigation ("композиция внутри самой функции вывода, чтобы места вызова не трогать") is if
   anything *strengthened* by the real number.
2. **Round-1 §5 said item 3 (indicator) "breaks nothing existing" and that `Test_setFilter` pins
   only return strings.** With the honest-message requirement (spec's "Сброс фильтров, когда
   фильтров нет"), `Test_setFilter`'s `"\n"` row (`config_view_test.go:340`) **does** break — see
   §12.5.
3. **Round-1 §5 said item 4's risk was only `renderSysstat`'s 4 test call sites.** Correct, but it
   did not flag the `time.Duration.String()` trap (§14.2) or that `Test_changeRefresh` should gain
   an assertion on the new field.
4. All pause-related analysis (§1 "drain and discard", §4.5 `Space`, §7 items 1-4) is out of scope.

### 19.2 `time.Duration.String()` will not produce the spec's format

`time.Duration(300 * time.Second).String()` is `"5m0s"`; `60s` is `"1m0s"`. The spec's
`refresh: 300s` (and the 76-character line-length arithmetic it rests on) require explicit
`fmt.Sprintf("%ds", int(d/time.Second))`. Verified. Not a blocker, just a trap.

### 19.3 Indicator ordering is unspecified and non-deterministic if taken literally

`view.Filters` is a `map[int]*regexp.Regexp`. Go randomises map iteration, so
`[F:datname,usename]` would flicker between `[F:usename,datname]` and `[F:datname,usename]` across
frames unless keys are sorted. The spec does not say; **sort ascending by column index** (which is
also left-to-right screen order, so it reads naturally). Record it as a decision.

### 19.4 One acceptance criterion cannot hold in its clamped case

Spec AC: *"Приглашение и поле ввода выровнены по горизонтали при непустой командной строке, в том
числе при активном индикаторе."* When the clamp bites (long prompt and/or narrow terminal — e.g.
`n` on 80 columns, the very case another AC demands), the input field is forced left of where the
prompt ends and overlays the prompt's tail. The two ACs are in tension. Suggested reconciliation:
scope the alignment AC to the **unclamped** case and add an explicit clamped-case AC ("поле ввода
остаётся на экране и пригодно для ввода; хвост приглашения перекрывается полем"). This is the one
place the code genuinely cannot deliver the spec as written.

### 19.5 Bold on `pretty.RateUnitPrefixed` bolds the unit too

Four sites (`stat.go:345, 347, 365, 366`). The helper returns `"1135 rMB/s"` — value and unit in one
string (`internal/pretty/pretty.go:78-95`). Base rows keep units outside the bold span. Either accept
the inconsistency or export a parts variant; the spec's "значения выделены жирным" does not decide
it. Recommend accepting; record it.

### 19.6 Filesyst identifier fields are not "values" in the base-row sense

`fs.Mount.Device` / `Mountpoint` / `Fstype` (`stat.go:377`) are strings, and neither base line 1
(`stat.go:272`) nor the pgstat info line (`:466`) bolds anything. The spec's blanket "значения
verbose-секций … выделены жирным" would, read literally, bold device names too. Recommend numbers
only; record the choice.

### 19.7 Not a blocker, but worth a line in the tech-spec

- **`top/` has no package-level `var` today.** The ambient `cmdlineCfg` is a new precedent for the
  package. The alternative (threading `*config` through `printCmdline`) costs 44 call sites plus
  three helper signatures (`showPgConfig`, `showPgLog`, `resetStat`) and their keybinding registrations
  (`keybindings.go:44, 50, 51`). The spec's own mitigation picks the ambient route; make it an ADR.
- **`top/ui.go`, `top/keybindings.go`, `top/dialog.go`, `top/help.go` have no test files at all.**
  Items 1, 2, 3 (partly) and 8 land there. The project's answer (009, 010) is to extract the
  decision into a pure function and test that — `firstTickHint` (`stat.go:149`) and `topBandLayout`
  (`layout.go:33`) are the precedents. This feature adds four such functions: `composeCmdline`,
  `filterToken`, `scrollOffsetFor`, `dialogInputX0`.
- **Active tech debt in touched modules** (`docs/tech-debt.md`, Active section is lines 8-203):
  `[016] Collector/parsers swallow errors silently` (Medium; only tangentially relevant — the
  error-frame path at `stat.go:648-655` is what makes the auto-scroll flag survive an error frame)
  and `[019] Nine tests skip every version when one cluster is unavailable` (affects how green a
  `make test` run looks, not this code). `[025]` (`PGresult.sort` bounds check) and
  `[012]` (verbose Size width-breathe) are **resolved** — round 1 listed `[025]` as active; it was
  resolved 2026-07-26. `[012]`'s regression test is the one §16.6 must repair rather than delete.
- **Settled ADRs that constrain this feature** (`docs/decisions-log.md` — do not propose
  alternatives): `[009] Scroll offset on top.config, not on view.View` (`:478`),
  `[009] Manual column window, not gocui viewport scroll` (`:494`),
  `[009] Reset offset on both view-switch paths` (`:510`),
  `[009] Partial last column + marker reservation in both walk directions` (`:526`),
  `[009] Hotkeys [ / ], not Shift/Ctrl/Alt + arrow` (`:542`) — the same termbox limitation that
  forces a bare `\`, `[009] Sort-column highlight takes priority over frozen-column bold` (`:558`),
  `[010] view.Verbose + config.verbose dual boolean` (`:590`),
  `[010] Pure topBandLayout geometry function` (`:606`),
  `[011] pretty.SizeWidth + single sizeFieldWidth = 8` (`:734`).
