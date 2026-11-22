Feature: Fast-tier eligibility rules from the documentation
  FastTierEligibility (ADR 0013): the 7-day on-time handover rate under 0.90
  suspends a node from SAME_DAY and NEXT_DAY; at or above 0.95 on three
  distinct UTC days of recompute reinstates it. Only the GRADED 7d scorecard
  moves the flag.

  Scenario Outline: A rate of exactly 0.90 keeps a node eligible and anything below suspends it
    Given node "node-1" has <ontime> on-time handovers and <late> late handovers
    When the scorecards are recomputed
    Then node "node-1" is <eligibility> for the fast tiers

    Examples:
      | ontime | late | eligibility |
      | 90     | 10   | eligible    |
      | 89     | 11   | ineligible  |

  Scenario: A day at exactly 0.95 counts toward reinstatement
    Given node "node-1" has 50 on-time handovers and 10 late handovers
    When the scorecards are recomputed
    And node "node-1" has 140 on-time handovers and 0 late handovers
    And the scorecards are recomputed
    Then node "node-1" is ineligible for the fast tiers
    And the reinstatement streak of node "node-1" is 1

  Scenario: Reinstatement counts UTC calendar days, not 24-hour periods
    Given node "node-1" has 50 on-time handovers and 10 late handovers
    When the scorecards are recomputed
    And node "node-1" has 400 on-time handovers and 0 late handovers
    And the scorecards are recomputed
    Then the reinstatement streak of node "node-1" is 1
    When the clock advances by 719 minutes
    And node "node-1" has 10 on-time handovers and 0 late handovers
    And the scorecards are recomputed
    Then the reinstatement streak of node "node-1" is 1
    When the clock advances by 2 minutes
    And node "node-1" has 10 on-time handovers and 0 late handovers
    And the scorecards are recomputed
    Then the reinstatement streak of node "node-1" is 2

  Scenario: A PROVISIONAL recompute leaves a suspended node and its streak as they were
    Given node "node-1" has 50 on-time handovers and 10 late handovers
    When the scorecards are recomputed
    And node "node-1" has 400 on-time handovers and 0 late handovers
    And the scorecards are recomputed
    And the clock advances by 9 days
    And the scorecards are recomputed
    Then the scorecard "node-1:7d" is PROVISIONAL
    And node "node-1" is ineligible for the fast tiers
    And the reinstatement streak of node "node-1" is 1
    And the events published were:
      | node-1:7d | NodeScorecardUpdated |

  Scenario: The 30-day rate never suspends a node, only the 7-day rate does
    Given node "node-1" has 0 on-time handovers and 50 late handovers
    When the clock advances by 9 days
    And node "node-1" has 100 on-time handovers and 0 late handovers
    And the scorecards are recomputed
    Then the scorecard "node-1:30d" is GRADED with grade "D"
    And the scorecard "node-1:7d" is GRADED with grade "A"
    And node "node-1" is eligible for the fast tiers
    And the events published were:
      | node-1:30d | NodeScorecardUpdated |
      | node-1:7d  | NodeScorecardUpdated |
