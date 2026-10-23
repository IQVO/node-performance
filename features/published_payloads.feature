Feature: What the published events carry
  Recompute publishes NodeScorecardUpdated with the state and grade it
  computed and NodeThresholdBreached with the metric, its new value and the
  threshold it crossed (.claude/rules/integration-events.md). A recompute
  that changes nothing publishes nothing (ADR 0002, A13).

  Scenario: NodeScorecardUpdated carries the recomputed grade
    Given node "node-1" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    Then a "NodeScorecardUpdated" event was published for "node-1:30d" carrying the grade "A"

  Scenario: A PROVISIONAL scorecard publishes its fall as PROVISIONAL
    Given node "node-1" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    And the clock advances by 9 days
    And the scorecards are recomputed
    Then a "NodeScorecardUpdated" event was published for "node-1:7d" carrying the grade "PROVISIONAL"

  Scenario: NodeThresholdBreached names the metric, its value and the threshold
    Given node "node-1" has 100 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    And node "node-1" has 0 on-time handovers and 25 late handovers
    And the scorecards are recomputed
    Then a "NodeThresholdBreached" event was published for "node-1:30d" naming the metric "onTimeHandoverRate" with value 0.8 against the threshold 0.9

  Scenario: A recompute that changes nothing publishes nothing
    Given node "node-1" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    And the scorecards are recomputed
    Then no event was published
