Feature: Published payload rules from the documentation
  NodeScorecardUpdated carries the metrics of the recompute exactly as
  computed and the recompute instant (ADR 0002 A12, A13); an update is
  published whenever the state, the grade or any metric changed, observation
  count included.

  Scenario Outline: NodeScorecardUpdated carries the observation count, the rate and the recompute instant
    Given node "node-1" has <ontime> on-time handovers and <late> late handovers
    When the clock advances by 3 hours
    And the scorecards are recomputed
    Then the "NodeScorecardUpdated" event for "node-1:30d" carries <observations> observations and an on-time handover rate of <rate> computed at the current clock
    And the "NodeScorecardUpdated" event for "node-1:7d" carries <observations> observations and an on-time handover rate of <rate> computed at the current clock

    Examples:
      | ontime | late | observations | rate         |
      | 50     | 0    | 50           | 1            |
      | 50     | 10   | 60           | 0.8333333333 |
