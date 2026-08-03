# Metrics Summary: tui-papercuts

## Context

| Dimension | Value |
|-----------|-------|
| Model | Opus 5 (1M context) |
| Feature size | M |
| Started | 2026-08-02 |
| Completed | 2026-08-03 |

## Timeline

| Phase | Duration (min) | Touch Time (min) |
|-------|---------------|------------------|
| User Spec | 91 | 91 |
| Tech Spec | 146 | 146 |
| Task Decomposition | 21 | 21 |
| Feature Execution | — (spans an overnight gap) | — |
| Done | 0 | 0 |
| **Total measured** | **258** | **258** |

Human wait time was not instrumented in this run, and execution wall-clock spans an idle overnight
gap, so flow efficiency would be misleading and is deliberately omitted rather than computed from
numbers that do not mean what the formula assumes.

## Quality

| Metric | Value |
|--------|-------|
| Validation rounds | user_spec: 3, tech_spec: 3, task_decomposition: 2 |
| Validation findings (crit/major/minor) | 10 / 36 / 104 |
| Review rounds | feature_execution: 1 |
| Defects found on the stand | 2 (1 fixed, 1 reclassified as pre-existing) |
| Acceptance verdict | GO — 27/27 user-spec, 11/11 tech-spec criteria |

The finding counts aggregate every validator report in the feature directory, including repeat
rounds, so the same issue can appear twice — once when raised, once when confirmed closed. They
measure review effort, not defect density.

## Volume

| Metric | Value |
|--------|-------|
| Interview questions | 14 |
| Tasks | 9 (in 5 waves) |
| Agents spawned | 38 |
| Commits (feature branch) | 13 |
| Code change | 15 files, +2022 / −126 |
| Stand captures | 30 |

## What the numbers do not show

Three of the ten critical findings were caught **before** any code was written, and each would have
cost a rework cycle: a missing reset that would have leaked scroll state across screens, waves
partitioned by file region instead of by file (three agents writing one file concurrently in a
single working tree), and an acceptance criterion pinned to a grep count that later tasks were
guaranteed to invalidate.

The single most valuable act of the run was not planned: the stand agent built a second binary from
`master` and ran every scenario twice. That reclassified three of five findings as pre-existing —
without it they would have been filed against this feature.
