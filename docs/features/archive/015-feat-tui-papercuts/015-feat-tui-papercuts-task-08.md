---
status: planned
depends_on: ["06", "07"]
wave: 4
skills: [pre-deploy-qa]
verify: "bash — stand run per patterns.md «Driving the TUI on a remote test stand», both capture modes, three terminal geometries"
reviewers: []
teammate_name:
---

# Task 08: Stand verification run

## Required Skills

Перед выполнением задачи загрузи:
- `/skill:pre-deploy-qa` — [skills/pre-deploy-qa/SKILL.md](~/.claude/skills/pre-deploy-qa/SKILL.md)

## Description

Tasks 1–7 proved that the pure functions compute the right numbers and that the render cores emit the
right strings. This task proves that the seven user-visible behaviours actually appear on a real
terminal — which is the only evidence that exists for them, because the tech-spec states plainly that
there are **no automated E2E tests**: a live terminal cannot be driven from `go test` and
`gocui.View` cannot be constructed in a unit test. The stand run is that missing layer, which is why
it is a wave of its own rather than an afterthought appended to the last code task.

Four of the checks are reachable by nothing else in the feature:

- **Bold rendering in the verbose panels** (Task 2). A unit test asserts that the renderer emits an
  SGR wrapper; only an escape-preserving capture shows that the terminal receives it and that the
  `n/a` sentinels stay plain.
- **The absent blank line in verbose** (Task 1). The arithmetic is unit-tested; whether the row
  between the cmdline and the table is actually gone is a property of the drawn screen.
- **Auto-scroll on a narrow terminal** (Task 4). `scrollOffsetFor` is unit-tested in isolation; that
  sorting by an off-screen column visibly brings it into the window needs a terminal narrow enough to
  have an off-screen column at all.
- **The longest dialog at 80 columns** (Task 7). `dialogInputX0` is asserted never to return
  `x0 >= x1`; that the mask dialog — the 93-character prompt, opened with `n` — no longer tears the
  UI down on an 80-column terminal with an active filter indicator is the case that **breaks today**,
  and it breaks in the drawing library, not in the arithmetic.

**This task reports; it does not edit code.** A QA task carries no reviewers by catalog, so a fix
landing here would enter the tree unreviewed. Every defect goes back to the task that owns the file,
through that task's own reviewers and the standard review cycle — the ownership map is in Details.
No stand defect may be closed by editing code in this task, however small the diff looks.

Three operational points decide whether the run is valid at all, and each of them fails silently
rather than loudly:

- **The stand address is not in this repository and is not worth writing down anywhere.** Stands are
  ephemeral, with a TTL measured in hours. Ask the project owner at the start of the run, and do not
  assume any previous run's state survived. If the stand is unavailable, that is a **blocker to
  report**, not something to work around with a local terminal — the unit layer is complete without
  it, so only this task blocks, not the feature.
- **The binary under test must be copied to the stand explicitly.** The system-wide `pgcenter` there
  is built from `master` and does not contain any of this feature. Building locally, running the
  stand's own binary and reporting green verifies nothing at all.
- **Two capture modes, and the wrong one silently invalidates the check.** `capture-pane -p` strips
  escape sequences — correct for layout, text and indicator content. `capture-pane -p -e` keeps them
  and is the **only** way to check bold: without `-e` a missing `\033[37;1m` is indistinguishable
  from a present one, so a colour regression passes.

Three geometries are exercised deliberately, because each isolates a class the others cannot reach: a
wide default for content and layout, a narrow one (`-x 60 -y 12`) for the column window and the
verbose height guard, and `-x 80` for the longest dialog.

## What to do

### A. Access, binary, load

1. **Ask the project owner for the stand address and connection parameters** at the start of the run
   (ssh target, and how `pgcenter top` should connect — host/port/user/database). Nothing here is
   stored in the repo. If the stand is not available, stop and report a blocker; do not substitute a
   local terminal for it.
2. **Build the feature binary locally** (`make build` → `./bin/pgcenter`) and **copy it to the
   stand**. Invoke it by explicit path for the whole run. Verify the copied binary is the one under
   test — compare `pgcenter --version` output or the file checksum against the local build, and
   record that check. Never invoke a bare `pgcenter`.
3. **Create enough activity on the stand for the screens to have rows and for filters to match**: a
   handful of background `psql` sessions, some idle in transaction, against the same cluster. A
   filter set on a column where every row matches, or none, makes several checks unreadable.

### B. Wide geometry — content, indicator, messages, bold, dialogs

Session: `tmux new-session -d -s cap -x 190 -y 52`, running the copied binary. After every keypress
allow at least one refresh interval before capturing, or the capture shows the previous frame.

4. **Refresh interval in the header.** Capture the compact header (`-p`). Confirm line 1 carries
   `refresh: <N>s` with the seeded default **before any `z`**. Then `z`, enter a different value,
   confirm the header changes immediately. Confirm the header row count is unchanged: **4 lines
   compact, 7 lines verbose** (`v` toggles verbose).
5. **Filter indicator.** Set a filter with `/` on the activity screen. Confirm `[F:<column>]` appears
   in the cmdline. Scroll the window with `]` until the filtered column's header is off-screen and
   confirm the indicator is **still there** — that is the footgun the whole indicator exists for.
   Set a second filter on another column and confirm both names are listed: `[F:datname,usename]`.
6. **Indicator survives the clear timer.** With a filter active, trigger any transient message (`z`
   then Enter is enough), wait more than two seconds, capture. The message must be gone and the
   indicator must remain.
7. **Indicator is per-screen.** Switch to a screen with no filters (`d`, `t`, …) and confirm the
   indicator is absent; switch back and confirm it returns.
8. **Bad regex.** Enter an invalid regular expression in the filter dialog. Confirm the cmdline shows
   the error, no new name joins the indicator, and previously set filters are still listed.
9. **Clear all filters and the three messages.** Press `\` with filters active — confirm
   `Filters: cleared N filter(s)`, the indicator goes dark and the filtered-out rows come back. Press
   `\` again with nothing active — confirm `Filters: no active filters`. Press `/` with empty input
   on a column that has no filter — confirm `Filters: no filter on this column`. All three texts must
   match exactly; a success message where nothing was removed is a defect.
10. **Help screen.** `h` (or `F1`), capture, confirm the cheat-sheet documents `\`. This is the only
    place a user learns the hotkey exists.
11. **Verbose blank line.** Enable verbose with `v` and capture with `-p`. Confirm there is **no empty
    row between the cmdline and the table** — the table starts on the row immediately below.
12. **Bold in verbose — escape-preserving capture.** With verbose on, capture with `-p -e`. Confirm
    the numeric values in the verbose sections of **both** panels carry the SGR bold sequence, and
    that `n/a` sentinels and identifier fields carry **none**. Use `sed 's/\x1b\[[0-9;]*m//g'` on the
    same capture when you need the plain text for comparison. A `-p` capture here proves nothing —
    say so in the report if you ever fall back to one.
13. **Dialog geometry, horizontal.** With the indicator active, open `/`, `z` and `A` in turn.
    Confirm for each: the prompt and the input field are on **one line**, the field starts
    immediately after the prompt and never overlaps it, and typed input lands in the field.
14. **Dialog geometry, vertical.** Repeat step 13 in **verbose** mode. The prompt and field must
    still share one line — this is the axis that was hard-wired to the compact layout.
15. **Dialog prompt is not erased.** Open a dialog, wait more than two seconds without typing,
    capture. The prompt must still be on screen — printing it must not arm the clear timer.

### C. Narrow geometry — column window and the height guard

16. Recreate the session at `-x 60 -y 12`.
17. **Auto-scroll to the sort column.** Move the sort column with `←`/`→` to a column outside the
    visible window, including the **last** column. Capture and confirm the column is now visible and
    highlighted. Confirm the window does **not** jump when the sort column was already visible, and
    that a partially visible column counts as visible.
18. **Manual scroll stays free.** After an auto-scroll, move the window with `[`/`]` and wait through
    several refreshes. The window must **not** snap back — the request is one-shot.
19. **Request does not leak across screens.** Sort by an off-screen column, switch screens (including
    `S` → procpidstat, which bypasses the common handler), and confirm the new screen's window is not
    moved by the previous screen's pending request.
20. **Verbose height guard.** At exactly `-y 12` confirm verbose **turns on**. Recreate at `-y 11` and
    confirm it falls back to compact with the hint, and that the indicator survives that hint and
    vice versa.

### D. 80 columns — the longest dialog

21. Recreate the session at `-x 80` (keep the height comfortable).
22. **The case that breaks today.** On the activity screen — the mask dialog is allowed there only —
    with a filter active so the indicator occupies cmdline width, press `n` (`Set state mask for
    group backends […]: `, 93 characters, the longest prompt in the program). Confirm the dialog
    opens, the drawing is **not** torn down or recreated, the input field is on screen, and typed
    input reaches it. Confirm the truncation rule: the **prompt** is what gets cut (with an ellipsis),
    the field is not pushed off, and the indicator prefix is preserved ahead of the message. Capture
    before and after the keypress.

### E. Report and cleanup

23. Produce the stand report per the `pre-deploy-qa` skill, with a verdict and the capture that
    supports it for **each** of the seven behaviours and for every user-spec acceptance criterion
    that only a live terminal can confirm. Criteria that this run cannot reach get an explicit
    `not_verifiable` verdict naming the unit test that carries them instead — not a `passed`.
24. Route every defect per the ownership map in Details: name the owning task, quote the capture, and
    state that the fix belongs to that task's review cycle. Do not edit code.
25. **Leave the stand as found:** reset any GUC changed for the run and verify the reset with `SHOW`,
    kill the background `psql` sessions, and kill the tmux session.

## Acceptance Criteria

Coverage, not a diff — this task produces a report.

- [ ] The stand address was requested from the owner at the start of the run; if the stand was
      unavailable, that is reported as a blocker and nothing is reported as verified.
- [ ] The binary under test is the freshly built `./bin/pgcenter` copied to the stand and invoked by
      explicit path, with the identity check (version or checksum against the local build) recorded.
      No result in this report comes from the stand's system-wide `master` binary.
- [ ] All **seven** behaviours exercised on the stand with a capture each: auto-scroll to the sort
      column; the persistent filter indicator; `\` clearing all filters with the three exact
      messages; `refresh: <N>s` in the header; no blank line in verbose; bold values in the verbose
      sections; dialog geometry on both axes.
- [ ] Bold is judged from an **escape-preserving** capture (`capture-pane -p -e`) — values carry the
      SGR sequence, `n/a` sentinels and identifier fields do not. Any check made from a plain capture
      is reported as unverified, not as passed.
- [ ] All three geometries used: wide default, `-x 60 -y 12`, and `-x 80`.
- [ ] The mask dialog (`n`, the 93-character prompt) opens at 80 columns **with the indicator
      active** without tearing the drawing down, with the input field on screen and the prompt — not
      the field — truncated. Captured before and after the keypress.
- [ ] Verbose turns on at `-y 12` and falls back to compact with the hint at `-y 11`; the indicator
      survives that hint.
- [ ] Auto-scroll brings an off-screen sort column into view including the last column, does not jerk
      the window when the column is already visible, does not undo a subsequent manual `[`/`]`, and
      does not leak across a screen switch — including the `S` (procpidstat) path.
- [ ] The three filter messages match exactly: `Filters: no active filters`,
      `Filters: cleared N filter(s)`, `Filters: no filter on this column`.
- [ ] The indicator survives the two-second clear timer, survives a dialog open/close, is absent on a
      screen with no filters, and does not gain an entry from an invalid regular expression while
      keeping the previously set ones.
- [ ] The help screen documents `\`.
- [ ] Every user-spec acceptance criterion that only a live terminal can confirm has an explicit
      verdict in the report; criteria that this run cannot reach are marked `not_verifiable` with the
      unit test that carries them named.
- [ ] Every defect is recorded with the capture that shows it and routed to the owning task by number;
      no code file was modified by this task (`git status` clean apart from report artifacts).
- [ ] The stand is left as found: GUCs reset and verified with `SHOW`, helper sessions closed, tmux
      session killed.

## Context Files

**Feature artifacts:**
- [015-feat-tui-papercuts.md](015-feat-tui-papercuts.md) — user-spec; «Критерии приёмки» and the
  «Как проверить» → «Агент проверяет» table are the baseline checklist for this run, and «Граничные
  случаи» lists what is expected rather than a defect
- [015-feat-tui-papercuts-tech-spec.md](015-feat-tui-papercuts-tech-spec.md) — tech-spec; the Agent
  Verification Plan (capture modes, geometries), Task 8's ownership rule, the Risks table and the
  Acceptance Criteria
- [015-feat-tui-papercuts-decisions.md](015-feat-tui-papercuts-decisions.md) — decisions log; read it
  **before** calling anything a defect — Tasks 1–7 may have recorded a deliberate, approved deviation
- [015-feat-tui-papercuts-code-research.md](015-feat-tui-papercuts-code-research.md) — code research;
  current behaviour of the surfaces under test, useful for telling a new defect from a pre-existing one

**Project knowledge** (`.claude/skills/project-knowledge/` — this project has `overview.md` in place
of the usual `project.md`):
- [overview.md](../../../.claude/skills/project-knowledge/overview.md) — what pgcenter is, which
  screens and statistics exist, supported PostgreSQL versions
- [architecture.md](../../../.claude/skills/project-knowledge/architecture.md) — package layout, the
  `top/` UI goroutine, data flow, view registry
- [patterns.md](../../../.claude/skills/project-knowledge/patterns.md) — **the stand regimen for this
  task**, section «Driving the TUI on a remote test stand»: ephemeral stand, explicit binary copy,
  deterministic tmux geometry, the two capture modes, leave-as-found rule
- [deployment.md](../../../.claude/skills/project-knowledge/deployment.md) — build and release
  mechanics behind `make build`

**Code files (read-only — this task writes no code):**
- [top/keybindings.go](../../../top/keybindings.go) — the authoritative key map: `/` filter, `\`
  clear-all (new), `z` refresh, `A` age, `n` mask, `v` verbose, `S` procpidstat, `[`/`]` scroll,
  `←`/`→` sort column, `h`/`F1` help
- [top/dialog.go](../../../top/dialog.go) — `dialogPrompts`: the mask prompt used at 80 columns, and
  the activity-only restriction on `n`
- [top/layout.go](../../../top/layout.go) — `topBandLayout`: what the blank-line fix and the height
  guard at 12 rows are supposed to produce on screen
- [top/ui.go](../../../top/ui.go) — the cmdline composer, the indicator prefix and the truncation
  ladder being observed
- [top/stat.go](../../../top/stat.go) — header rendering (`refresh: <N>s`), the verbose renderers and
  their bold wrappers
- [top/help.go](../../../top/help.go) — the cheat-sheet text checked in step 10
- [Makefile](../../../Makefile) — `build` target producing `./bin/pgcenter`

## Verification Steps

- Confirm the stand address was obtained from the owner for **this** run, and that the copied binary
  matches the local `./bin/pgcenter` (version or checksum recorded).
- Confirm a capture exists for every one of the seven behaviours, each labelled with the geometry and
  the capture mode used.
- Confirm the bold check was made with `capture-pane -p -e` and that the report quotes the actual
  escape sequences present on values and absent on `n/a`.
- Confirm captures exist for all three geometries: the wide default, `-x 60 -y 12` and `-x 80`.
- Confirm the 80-column mask-dialog capture pair (before/after `n`) shows an intact screen with the
  input field present.
- Confirm the three filter message texts in the captures match the user-spec strings character for
  character.
- Confirm the report walks every user-spec acceptance criterion with a verdict, and that anything not
  reachable on the stand is marked `not_verifiable` with the covering unit test named.
- Confirm `git status` shows no modified source file — this task reports, it does not fix.
- Confirm the cleanup evidence: `SHOW` output for any GUC that was changed, and the tmux session gone.

## Details

**Files:** **none modified.** This task produces a report and captures only. Write the report to
`logs/working/qa-015-stand/report.json` and the captures alongside it as
`logs/working/qa-015-stand/<check-name>.txt`. Do **not** write `logs/working/qa-report.json` — that
path is git-tracked and belongs to Task 9 (final pre-deploy QA); overwriting it here would destroy
the final report's slot. Note that `logs/working/` is not in `.gitignore`, so the capture files show
up as untracked in `git status`: keep them out of the wave commit and reference them from the
decisions entry rather than committing a pile of terminal dumps.

**Dependencies:** Tasks 1–7, all of them — every behaviour under test is one of theirs. Formally
`depends_on: ["06", "07"]`, the last wave of code tasks; Tasks 1–5 are transitively complete by then.
The task also runs **alone in its wave**: the stand is a single ephemeral machine with one tmux
session, so two QA agents on it at once would collide.

**Who fixes what this run finds.** The report names the owning task; the fix lands there, through
that task's own reviewers and the standard review cycle. This task has no reviewers, so a fix made
here would enter the tree unreviewed — that is the whole reason for the rule.

| Defect seen on the stand | Owning task | Files |
|---|---|---|
| Blank row still present in verbose; verbose threshold wrong | 1 | `top/layout.go` |
| Values not bold in verbose, or `n/a` wrongly bolded | 2 | `top/stat.go` |
| `\` behaviour or any of the three message texts; missing help line | 3 | `top/config_view.go`, `top/keybindings.go`, `top/help.go` |
| Auto-scroll missing, jerky, sticky, or leaking across screens | 4 | `top/config.go`, `top/config_view.go`, `top/stat.go` |
| Indicator missing, wrongly scoped, erased by the timer, or truncated in the wrong order | 5 | `top/ui.go`, `top/top.go`, `top/dialog.go` |
| `refresh: <N>s` missing or stale; header row count changed | 6 | `top/config.go`, `top/top.go`, `top/ui.go`, `top/stat.go` |
| Dialog field misplaced on either axis; the 80-column mask dialog still tears the UI down | 7 | `top/dialog.go` |

**Keys used in this run** (from `top/keybindings.go`, `sysstat` view unless noted): `/` open filter
dialog, `\` clear all filters (new in Task 3), `z` change refresh, `A` change age, `n` set state mask
— **allowed on the `activity` screen only**, `m` show current mask, `v` toggle verbose, `S` switch to
procpidstat, `[` / `]` scroll the column window, `←` / `→` move the sort column, `<` toggle sort
direction, `h` / `F1` help (`Esc` or `q` closes it), `d` / `t` / `i` / `s` / `a` switch screens,
`Esc` cancels a dialog, `Enter` submits it, `Ctrl-C` / `Ctrl-Q` quits.

`pgcenter top` opens on the **activity** screen by default, which is convenient: the mask dialog check
needs no screen switch, and activity has enough columns to be wider than 60 terminal columns.

**Edge cases and expected non-findings** (from the user-spec's «Граничные случаи» — do not report
these as defects):

- **The sort column is already visible** → the window must not move at all. A partially visible column
  counts as visible; that is the window semantics inherited from feature [009].
- **All rows filtered out** → auto-scroll still works: column widths come from the headers.
- **Invalid regular expression** → no indicator entry appears and the cmdline shows an error;
  previously set filters remain listed. That is correct, not a swallowed filter.
- **A screen with no filters shows no indicator** → filters are stored per screen. The indicator
  showing only the current screen's filters is by design.
- **Terminal below the verbose threshold** → layout falls back to compact with a hint. The hint and
  the indicator must coexist in both directions.
- **The verbose threshold moved from 13 to 12 rows** → deliberate, a consequence of the blank-line
  fix. Verbose becoming available one row earlier is the accepted side effect.
- **Sorting now moves the column window** → this contradicts `docs/features-catalog.md:189`, which
  records "no auto-scroll to the sort column" as expected behaviour of [009]. The catalog entry is
  corrected at finalization; the new behaviour is what this feature intends. Do not report the
  contradiction as a defect — but do check the correction is on the finalization list.
- **A dialog opened before a terminal resize keeps its coordinates** → pre-existing, explicitly out of
  scope, recorded as a known limitation.

**Implementation hints:**

- `tmux new-session -d -s cap -x 190 -y 52` fixes the geometry; that determinism is the entire reason
  for using tmux rather than an interactive ssh session. Kill and recreate the session for each
  geometry rather than resizing a live one — a resize mid-run puts the UI into the resize path, which
  is a different code path and not what these checks are about.
- Send keys with `tmux send-keys -t cap`, then **wait at least one refresh interval** before
  `capture-pane`. A capture taken too early shows the previous frame and reads exactly like a
  behaviour that did not happen. Consider lowering the refresh with `z` to 1s early in the run to
  shorten every subsequent wait — and note that doing so also gives the header check a value to
  observe.
- Special keys need the tmux names: `send-keys -t cap Left` / `Right` / `Escape` / `Enter`. A literal
  backslash is safest as `send-keys -t cap '\'` (quoted) and a literal `[` / `]` likewise.
- Strip ANSI from an `-e` capture for diffing with `sed 's/\x1b\[[0-9;]*m//g'`. Keep **both** the raw
  and the stripped version of the bold capture in the report artifacts: the raw one is the evidence,
  the stripped one is what makes it readable.
- For the 80-column mask dialog, capture immediately before and immediately after the keypress. The
  failure mode today is the drawing being torn down and recreated, which looks like a blank or
  scrambled screen rather than an error message — a single "after" capture cannot show that something
  changed.
- To get an off-screen sort column at `-x 60`, press `→` repeatedly from the leftmost column and watch
  the window: the activity screen has enough columns that the last ones are far outside 60 columns.
- Keep the load-generating `psql` sessions in background processes or separate tmux windows; an
  idle-in-transaction session dies with its shell, and the screens go empty mid-run.
- Read the decisions log before judging anything a defect — an earlier task may have recorded an
  approved deviation, and reporting it as a failure sends the wave into a pointless review round.

## Reviewers

None — this task is itself a verification gate. Its output is a report, not code, so no reviewer
agents run against it. This is also why the task must not edit code: there is no review cycle here to
catch a mistake in such an edit.

## Post-completion

- [ ] Записать краткий отчёт в
      [015-feat-tui-papercuts-decisions.md](015-feat-tui-papercuts-decisions.md)
      (Summary: 1-3 предложения, ссылка на `logs/working/qa-015-stand/report.json`, без таблиц
      файндингов и дампов)
- [ ] Перечислить найденные дефекты с указанием задачи-владельца — фиксы уходят в её цикл ревью,
      не сюда
- [ ] Явно перечислить критерии со статусом `not_verifiable` и то, на чём они держатся вместо живой
      проверки
- [ ] Если стенд был недоступен — зафиксировать это как блокер, а не как «проверено»
- [ ] Если отклонились от спека — описать отклонение и причину
- [ ] Обновить user-spec/tech-spec если что-то изменилось
