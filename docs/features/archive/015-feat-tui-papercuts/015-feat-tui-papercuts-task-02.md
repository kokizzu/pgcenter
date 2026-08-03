---
status: planned                    # planned -> in_progress -> done
depends_on: []                     # ID задач-зависимостей (строки: ["01", "02"])
wave: 1                            # волна параллельного выполнения
skills: [code-writing]             # МАССИВ скиллов для загрузки
verify: bash — `make test`         # инструмент верификации (опционально: curl, bash, user)
reviewers: [dev-code-reviewer, dev-test-reviewer]  # явно указать. Пусто = fallback на defaults
teammate_name:                     # имя агента-исполнителя (опционально; если не задано — генерируется по описанию задачи)
---

# Task 02: Bold the numeric values in the verbose sections

## Required Skills

Перед выполнением задачи загрузи:
- `/skill:code-writing` — [skills/code-writing/SKILL.md](~/.claude/skills/code-writing/SKILL.md)

## Description

Every base row of both top panels wraps its numeric values in `\033[37;1m…\033[0m` (SGR 37 = white
fg, 1 = bold): `top/stat.go:280` (`%cpu`), `:288` (mem), `:296` (swap), `:472` (activity), `:481`
(autovacuum), `:489` (statements). The verbose rows added by feature [010] —
`renderSysstatVerbose` (`top/stat.go:330-388`) and `renderPgstatVerbose` (`top/stat.go:528-607`) —
print their values plain. The result is that pressing `v` produces a block that reads visually
weaker than the four rows above it, which the project owner flagged during visual review.

This task closes that omission: wrap the numeric values of both verbose renderers in the same
sequence, and leave everything that is *not* a value plain. "Not a value" means two things
(tech-spec Decision 9):

1. **Degraded renderings.** The `n/a` sentinels — bare `naLiteral` and `naReserve(width)` — stay
   plain. Bold must read as "there is a real number here"; bolding `n/a` too would destroy exactly
   the signal the degradation design was built for.
2. **Identifier fields.** In the filesyst row, `device`, `mountpoint` and `fstype` stay plain —
   they are identifiers, not numbers, and base line 1 of each panel (`stat.go:272`, the
   `formatInfoString` line at `:466`) bolds nothing.

The structural rule that makes this hard to get wrong: **bold goes on the value branch, never on
the sentinel branch**. Both renderers are built as value-or-sentinel pairs — `naInt`
(`top/stat.go:519`) internally, and the locals `writeMs`/`syncMs`/`maxw` (bgwr/ckpt row) and
`size`/`growth`/`hit`/`lag`/`retain`/`backlog` (databases/replication rows) via a default `naReserve`
assignment overwritten inside an `if`. Wrap where the number is *produced*, not the variable
afterwards, and the sentinel stays plain automatically.

The test side is where the real work is. SGR sequences are zero-width on screen, so the alignment
invariants (`naReserve` / `ReserveWidth` / `SizeWidth`) are preserved *visually* but not *byte-wise*
— and `Test_renderPgstat_verboseNAWidthStatic` (`top/stat_test.go:406-509`) proves alignment by
comparing **byte offsets** via `strings.Index` between the value state and the `n/a` state. The value
side gains 12 bytes the sentinel does not, so those assertions fail. That test is the regression test
for resolved tech debt **[012]** (`docs/tech-debt.md:279-289`) — it must be **repaired, not deleted**,
by measuring visible offsets instead of byte offsets, reusing the SGR regexp that already exists in
this file.

## What to do

- Add a single unexported helper in `top/stat.go` that wraps a rendered value in the base-row SGR
  sequence, documented as to why the `n/a` sentinels deliberately do not go through it. One helper —
  do not retype the escape pair at ~30 sites.
- Wrap every numeric value site of `renderSysstatVerbose`, per the enumeration in code-research
  §16.2: the active-device/interface counts in **both** the degraded and the value branch (the count
  is a real number even when the deltas are not), max-util percentages (the `%%` stays outside the
  span), the four `pretty.RateUnitPrefixed` rates, the completed-ops counters, the err/coll
  composite as **one** span, and the filesyst `size`/`used`/`use%`. Leave the five/four bare
  `naLiteral` arguments and the whole degraded filesyst row plain, and leave device/mountpoint/fstype
  plain.
- Move the bold **into `naInt`'s value branch** (`top/stat.go:519-524`) so the seven workload numbers
  are covered without touching their call sites, and update the helper's doc comment to say the
  sentinel path is deliberately unwrapped.
- Wrap every numeric value site of `renderPgstatVerbose`, per code-research §16.3: the databases
  `size`/`growth`/`hit` assignments *inside* their `if` branches, the always-real `DatabasesCount`,
  the three workers `%s/%d` composites (one span each, matching `stat.go:481`), the replication
  `wal size`, `lag`/`retain`/`backlog` inside their `if` branches, the slots count and the
  `senders/receivers` pair, and the `writeMs`/`syncMs`/`maxw` assignments **inside the `if hp`
  branch** plus the `timed/req` composite. Every `naReserve` default and the `naLiteral` defaults at
  `:594` stay untouched.
- Update the affected goldens and substring assertions in `top/stat_test.go` deliberately — derive
  each expected string from what the layout rules say it must be, not by copy-pasting whatever the
  new implementation prints.
- Repair `Test_renderPgstat_verboseNAWidthStatic` so its offset comparisons run over SGR-stripped
  lines, using the **existing** `ansiEscape` regexp (`top/stat_test.go:939`) that
  `visibleRuneLen` (`:941-945`) is already built on. Do not introduce a second SGR stripper, and do
  not weaken or remove any of its assertions.
- Add positive/negative coverage that locks the convention itself: values carry the escape pair,
  sentinels and identifier fields do not.

## TDD Anchor

Тесты, которые нужно написать ДО реализации. Пишем → запускаем → убеждаемся что падают → пишем код → убеждаемся что проходят.

- `top/stat_test.go::Test_renderPgstatVerbose_boldOnValues` — with `HasPrev` true and every
  `*Valid` flag set, **count the bold spans per row and assert the exact expected number**, not mere
  presence: workload 7, databases 4, workers 3, replication 6, bgwr/ckpt 4. Asserting only that a row
  *contains* `\033[37;1m` would pass with a single value bolded and the rest plain, which is exactly
  the half-done state the acceptance criteria forbid. Additionally the visible text (after stripping
  via `ansiEscape`) must be byte-identical to today's golden — bold adds no visible characters.
- `top/stat_test.go::Test_renderPgstatVerbose_sentinelsNotBold` — with `HasPrev` false and every
  `*Valid` flag false, no `n/a` occurrence in the five rows is preceded by `\033[37;1m`; asserted
  per sentinel, not just as "row contains n/a".
- `top/stat_test.go::Test_renderSysstatVerbose_boldOnValues` — iostat/nicstat/filesyst value
  branches wrap their numbers; the `%` of "max util" and the `/` between err and coll fall as
  specified (percent sign outside the span, err/coll as one span).
- `top/stat_test.go::Test_renderSysstatVerbose_identifiersNotBold` — the filesyst row's device,
  mountpoint and fstype carry no escape sequence, while `size`/`used`/`use%` on the same row do.
- `top/stat_test.go::Test_renderSysstat_verboseFirstTickNA` (existing, extend) — in the degraded
  iostat/nicstat branch the device count **is** bold while the five/four `n/a` args are not; the
  `"0% max util"` substring assertion at `:229` is re-expressed against the SGR-stripped line.
- `top/stat_test.go::Test_renderPgstat_verboseNAWidthStatic` (existing, repair) — all three offset
  groups (cache hit ratio `:429-432`, tps `:439-442`, the five Size fields `:504-507`) compare
  `strings.Index` over `ansiEscape`-stripped rows; group (a) (value A vs value B) and group (b)
  (value vs n/a) both hold. This is the [012] regression test — assertions preserved one-for-one.
- `top/stat_test.go::Test_renderSysstat_compactUnchanged`, `::Test_renderPgstat_compactUnchanged`
  (existing) — stay green untouched: bold is verbose-only, the compact prefix is unchanged.

## Acceptance Criteria

- [ ] Every numeric value in both verbose renderers is wrapped in `\033[37;1m…\033[0m`, matching the
      base rows' convention.
- [ ] No `n/a` rendering is wrapped — neither the bare `naLiteral` sites (`stat.go:337, 357, 382,
      594`) nor any `naReserve(width)` result, including the one inside `naInt`.
- [ ] The filesyst identifier fields (device, mountpoint, fstype) render plain.
- [ ] `naInt` returns a bolded value and a plain sentinel; none of its seven call sites changed.
- [ ] Composite values follow the base-row rule: `%s/%d` workers, `%s/%s` timed/req and
      `%d/%d` senders/receivers are one span each; slots/retain is two separate spans (an
      always-real count next to a possibly-n/a size).
- [ ] Visible width is unchanged: for every verbose row, the `ansiEscape`-stripped output equals the
      pre-change output byte for byte.
- [ ] `Test_renderPgstat_verboseNAWidthStatic` exists, is not weakened, and passes — offsets compared
      over SGR-stripped lines via the existing `ansiEscape` regexp.
- [ ] No second SGR-stripping helper is introduced in `top/stat_test.go`.
- [ ] `make test` passes with the race detector; `make lint` is clean.
- [ ] The compact (non-verbose) output of both panels is byte-identical to before.

## Context Files

**Feature artifacts:**
- [015-feat-tui-papercuts.md](docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts.md) — user-spec (item: verbose bold, AC line 284)
- [015-feat-tui-papercuts-tech-spec.md](docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-tech-spec.md) — tech-spec; **Decision 9** (lines 226-249) is the source of truth for what gets bolded
- [015-feat-tui-papercuts-decisions.md](docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-decisions.md) — decisions log
- [015-feat-tui-papercuts-code-research.md](docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-code-research.md) — **§16** (lines 1386-1532): every value site, both sentinel paths, the caveats, and the exact list of breaking assertions

**Project knowledge:**
- [overview.md](.claude/skills/project-knowledge/overview.md)
- [architecture.md](.claude/skills/project-knowledge/architecture.md)
- [patterns.md](.claude/skills/project-knowledge/patterns.md) — verbose display-mode section (line 62+), `pretty` formatter rules (line 101+), testing conventions, and why `capture-pane -e` is the only way to see bold

**Code files:**
- [top/stat.go](top/stat.go) — modify: `naInt`, `renderSysstatVerbose`, `renderPgstatVerbose`; add the bold helper
- [top/stat_test.go](top/stat_test.go) — modify: verbose goldens, substring assertions, the width-static test; add the convention tests
- [docs/tech-debt.md](docs/tech-debt.md) — read: entry **[012]** (lines 279-289), the debt whose regression test must survive this task
- [internal/pretty/pretty.go](internal/pretty/pretty.go) — read only; **unchanged** (Decision 9)

## Verification Steps

- Run `make test` — the race-detector suite passes, including the repaired
  `Test_renderPgstat_verboseNAWidthStatic`.
- Run `go test ./top/ -run 'Test_render' -v` — every verbose render test passes; confirm the new
  convention tests fail before the implementation and pass after (RED → GREEN).
- Run `make lint` — clean.
- Confirm by inspection that no `\033[37;1m` literal appears anywhere near an `naLiteral` /
  `naReserve` expression in `top/stat.go`.
- Manual on-screen confirmation via tmux `capture-pane -p -e` is **Task 9's** scope, not this task's
  — do not run the stand here.

## Details

<!-- All details for task execution — technical, organizational, any other. -->

**Files:**

- `top/stat.go`
  - `naInt` (`:519-524`) — currently: `if !hasPrev { return naReserve(width) }; return
    pretty.ReserveWidth(int(v), width)`. Bold the second return only. This is the single edit that
    covers all seven workload fields and makes "n/a stays plain" structurally impossible to get
    wrong. Extend the doc comment accordingly.
  - `renderSysstatVerbose` (`:330-388`) — three rows, each with a degraded and a value branch. Value
    sites per code-research §16.2: `:337` device count (degraded branch — still bold), `:343-348`
    (count, util, two `RateUnitPrefixed`, two completed counters), `:357` net device count (degraded
    branch — still bold), `:363-368` (count, util, two rates, err/coll as one span), `:378`
    (`pretty.Size(fs.Size)`, `pretty.Size(fs.Used)`, `fs.Pused` via `%3.0f`). Plain: all bare
    `naLiteral` args, `:377` identifiers, `:382` degraded filesyst row.
  - `renderPgstatVerbose` (`:528-607`) — value sites per §16.3: `:543` size, `:545` growth, `:553`
    hit, `:556` `DatabasesCount`, `:562-564` three workers composites, `:572` lag, `:576` retain,
    `:580` backlog, `:583` wal size, `:584` slots count, `:585` senders/receivers, `:596-598`
    writeMs/syncMs/maxw (inside `if hp`), `:601` timed/req composite. Plain: the `naReserve`
    defaults at `:541` (×2), `:551`, `:570`, `:574`, `:578`, and the `naLiteral` defaults at `:594`.
  - New helper — a one-line `bold(s string) string` next to the other verbose helpers, with a
    comment naming `stat.go:280` as the convention it mirrors and stating that sentinels stay
    unwrapped on purpose.
- `top/stat_test.go`
  - Full-line goldens that must gain the escapes: `:171-173` (iostat), `:194-196` (nicstat),
    `:248-250` (filesyst), `:339-341` (databases n/a — `DatabasesCount` becomes bold),
    `:342-344` (workers), `:345-347` (replication), `:348-350` (bgwr/ckpt), and all five rows of
    `Test_renderPgstat_verboseAvailable` (`:378-380`, `:384-386`, `:387-389`, `:390-392`,
    `:393-395`). The workload golden at `:336-338` is the pure-n/a row — verify it survives
    unchanged rather than assuming it.
  - Substring assertions that break: `:229` `"0% max util"` (the reset now sits between the digit
    and the `%`), `:427-428` `"100.00% cache hit ratio"`, `:437-438` `" 999 tps"`. Re-express these
    against the SGR-stripped line.
  - Assertions that survive and must not be touched: `:218-219`, `:227-228`, `:251-252`, `:267`
    (`"filesyst: n/a"` — pure degraded branch), `:397` (`NotContains "n/a"`), and every line-count
    assertion (`:168, :192, :217, :226, :244, :266, :293, :329, :375, :530`) — bold adds no rows.
  - `Test_renderPgstat_verboseNAWidthStatic` (`:406-509`): group (a) at `:499-502` compares two
    value samples and technically survives, group (b) at `:429-432`, `:439-442` and `:504-507`
    compares value vs n/a and fails. Strip both sides uniformly so the whole test states one
    invariant — *visible* column position — rather than two.

**Dependencies:** none. Wave 1, `depends_on: []`. Tasks 1 and 3 run in parallel but touch
`top/layout.go` / `top/config_view.go` / `top/keybindings.go` / `top/help.go` — no file overlap with
this task. `internal/pretty` is not modified. Task 4 (wave 2) and Task 6 (wave 3) also touch
`top/stat.go` / `top/stat_test.go`, so keep this change tight and self-contained to avoid churn for
them; in particular `renderSysstat`'s signature changes in Task 6 — do not pre-empt it here.

**Edge cases:**
- Degraded iostat/nicstat branches still print a real device count — it must be bold there too,
  which is the one place where "degraded branch" and "plain" come apart.
- `pretty.RateUnitPrefixed` returns value **and** unit as one string (`internal/pretty/pretty.go:78`
  → `rateUnitParts`, `:87`; golden `"1135 rMB/s"`), so the four rate sites bold the unit along with
  the number. Decision 9 accepts this explicitly — do **not** change the formatter or add a
  parts-returning variant.
- Slots/retain (`:584` + `retain`) mixes an always-real count with a possibly-n/a size — two
  separate spans, unlike the other composites.
- The bgwr write/sync composite also yields two adjacent spans in the `hp` branch and two plain
  values otherwise; that asymmetry with `timed/req` (one span) is intentional, driven by which side
  can degrade.
- Truncation can cut a line between `\033[37;1m` and `\033[0m` on a half-width panel. This is not a
  new class of problem — the compact rows have had it since forever — and gocui's escape interpreter
  handles both sequences in `OutputNormal`. Nothing extra is required; do not add guards.
- `syncMs` is deliberately tight-width (`strconv.Itoa`, no reserve) per the A/B composite rule —
  bolding it does not change that; do not "fix" its width here.

**Implementation hints:**
- Work outside-in on the tests: write the convention tests first, watch them fail, then make the
  three `stat.go` edits, then update goldens.
- When updating a golden, reconstruct it from the format string and the reserve widths, then compare
  — a golden regenerated from the output silently ratifies bugs.
- The visible-width acceptance criterion is cheap to assert directly: strip the escapes from the new
  output and compare against the old golden string.
- For the width-static test, a tiny local wrapper over the existing `ansiEscape` regexp keeps the
  three call groups readable without introducing a second stripper — `visibleRuneLen` returns a rune
  count and cannot be reused as-is for offsets.

## Reviewers

- **dev-code-reviewer** → `docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-task-02-dev-code-reviewer-review.json`
- **dev-test-reviewer** → `docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-task-02-dev-test-reviewer-review.json`

`dev-security-auditor` is deliberately absent (tech-spec, Implementation Tasks preamble): this
feature has no network, auth, persistence or SQL surface, and its terminal-escape handling is
covered by the tech-spec's own security review. Do not restore the default reviewer set.

## Post-completion

- [ ] Записать краткий отчёт в [015-feat-tui-papercuts-decisions.md](docs/features/015-feat-tui-papercuts/015-feat-tui-papercuts-decisions.md) (Summary: 1-3 предложения, ревью со ссылками на JSON, без таблиц файндингов и дампов)
- [ ] Если отклонились от спека — описать отклонение и причину
- [ ] Обновить user-spec/tech-spec если что-то изменилось
