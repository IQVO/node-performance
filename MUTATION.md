# Mutation testing: residual mutants

Measured with gremlins v0.6.0, one worker, one `gremlins unleash ./internal/domain/<pkg>` per package
(`make mutation`, the same loop as the `mutation-fast` CI job). Efficacy is 100.00% in every package.

## NOT COVERED by the cover profile, killed in fact (not equivalent)

| Package | File:line | Mutators | Why it is reported NOT COVERED |
| --- | --- | --- | --- |
| scorecard | `eligibility.go:55` (`f.Suspended && w != Window7d`) | CONDITIONALS_NEGATION | `case` expression of a tagless `switch` |
| scorecard | `eligibility.go:57` (`f.Streak < 0 \|\| f.Streak >= reinstateAfterDays`) | CONDITIONALS_BOUNDARY, CONDITIONALS_NEGATION on both operators (4) | same |
| scorecard | `eligibility.go:59` (`!f.Suspended && f.Streak != 0`) | CONDITIONALS_NEGATION | same |
| scorecard | `eligibility.go:61` (`(f.Streak == 0) != f.StreakDay.IsZero()`) | CONDITIONALS_NEGATION on both operators (2) | same |

Go's cover tool instruments the body of each `case` clause and never the `case` expression itself, so
gremlins sees no covered block on those lines (HARNESS.md "tagless switch" pitfall). The tests execute
and assert all four conditions (`TestFastTierRestoreRefusesACorruptState`,
`TestFastTierRestoreAcceptsEveryLegalStreak`, `TestFastTierStreakBoundErrorNamesTheLargestLegalStreak`):
each of the 8 mutations, applied by hand, fails `go test ./internal/domain/scorecard`.

Their cost is on the mutator-coverage figure only: scorecard measures 93.94%, so `.gremlins.yaml`
carries `mutant-coverage: 93`. Rewriting `FastTierState.validate` as an `if` chain (production,
behaviour-preserving) moves those lines into covered blocks and lets the gate rise to 99.

## Equivalent mutants

None.
