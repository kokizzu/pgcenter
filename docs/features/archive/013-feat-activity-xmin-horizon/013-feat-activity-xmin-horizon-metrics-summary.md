# Metrics Summary: activity-xmin-horizon

## Context

| Dimension | Value |
|-----------|-------|
| Model | claude-opus-5[1m] |
| Feature size | M |
| Started | 2026-07-25 |
| Completed | 2026-07-26 |

## Timeline

| Phase | Duration (min) | Wait Time (min) |
|-------|---------------|-----------------|
| User Spec | ~423 | 0 |
| Tech Spec | ~85 | 0 |
| Task Decomposition | ~105 | 0 |
| Feature Execution | ~25 | 0 |
| Done | ~20 | 0 |
| **Total** | **~658** | **0** |

Note: the session spanned a deliberate 105-minute pause while a usage limit reset. That interval is
inside the lead time but is not work, so the numbers above overstate elapsed effort.

## Quality

| Metric | Value |
|--------|-------|
| Validation rounds | user_spec: 2, tech_spec: 2, task_decomposition: 2 |
| Validation findings (crit/major/minor) | 11 / 37 / 88 |
| Review findings (crit/major/minor) | 0 / 3 / 13 |
| First pass rate | 50% (task 03 self-reviewed clean; the rest needed a fix round) |

## Volume

| Metric | Value |
|--------|-------|
| Interview questions | 8 |
| Tasks | 6 (in 3 waves) |
| Agents spawned | ~28 |
| Commits | 6 on the feature branch, squashed to 1 on develop |

## What the numbers do not show

The dominant cost of this feature was not implementation — it was **six successive rounds of catching
claimed coverage that did not exist**. Each was caught by a different mechanism:

1. Completeness validator: "goldens prove the sort change is safe" — they never touch it.
2. Security auditor: the fourth caveat was named in Risks but absent from the criteria enumerating it.
3. Architect: "the string half is checked by hand in Task 6" — no such check existed in Task 6.
4. Task creator: the replay test would have passed against the unfixed comparator.
5. Task creator: `PostgresV13` "needs adding" — it already existed.
6. Test reviewer: the feature's own headline invariant had no automated cover at all; mutants breaking
   both central rules passed the entire suite.

The last one is the sharpest: the feature that spent this much effort on not claiming absent coverage
had left exactly that gap at its centre, and only mutation testing found it. **A stated fact about
coverage is worth nothing until something red has been observed.**

Second-order lesson: two of the three implementation defects that reached review were regressions
introduced by fixes for other defects — the sort-key restore and the zero-width guard. Both were only
caught because reviewers reproduced against `develop` rather than reading the diff.
