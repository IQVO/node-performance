Feature: Fast-tier eligibility from the 7-day window
  The 7d scorecard gates the SAME_DAY and NEXT_DAY tiers (ADR 0013). A GRADED
  7d on-time handover rate under 0.90 suspends the node; at or above 0.95 on
  three distinct UTC days of recompute reinstates it. The flip is published as
  NodeTierEligibilityChanged, once per tier. A PROVISIONAL scorecard never
  changes eligibility.

  Scenario: A collapse of on-time handover suspends both fast tiers
    Given node "node-1" has 50 on-time handovers and 10 late handovers
    When the scorecards are recomputed
    Then node "node-1" is ineligible for the fast tiers
    And the events published were:
      | node-1:30d | NodeScorecardUpdated       |
      | node-1:7d  | NodeScorecardUpdated       |
      | node-1:7d  | NodeTierEligibilityChanged |
      | node-1:7d  | NodeTierEligibilityChanged |
    And a tier event was published for "node-1:7d" for the tier "SAME_DAY" with eligible false on value 0.8333333333 against the threshold 0.9
    And a tier event was published for "node-1:7d" for the tier "NEXT_DAY" with eligible false on value 0.8333333333 against the threshold 0.9

  Scenario: A healthy node is eligible from the start and publishes no tier event
    Given node "node-1" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    Then node "node-1" is eligible for the fast tiers
    And the events published were:
      | node-1:30d | NodeScorecardUpdated |
      | node-1:7d  | NodeScorecardUpdated |

  Scenario: A PROVISIONAL scorecard never suspends a node
    Given node "node-1" has 0 on-time handovers and 10 late handovers
    When the scorecards are recomputed
    Then node "node-1" is eligible for the fast tiers
    And no event was published

  Scenario: A rate between the two bars neither reinstates nor counts a day
    Given node "node-1" has 50 on-time handovers and 10 late handovers
    When the scorecards are recomputed
    And node "node-1" has 65 on-time handovers and 0 late handovers
    And the scorecards are recomputed
    Then node "node-1" is ineligible for the fast tiers
    And the reinstatement streak of node "node-1" is 0
    And the events published were:
      | node-1:30d | NodeScorecardUpdated |
      | node-1:7d  | NodeScorecardUpdated |

  Scenario: Reinstatement takes three distinct days at or above 0.95
    Given node "node-1" has 50 on-time handovers and 10 late handovers
    When the scorecards are recomputed
    And node "node-1" has 400 on-time handovers and 0 late handovers
    And the scorecards are recomputed
    Then node "node-1" is ineligible for the fast tiers
    And the reinstatement streak of node "node-1" is 1
    When the scorecards are recomputed
    Then the reinstatement streak of node "node-1" is 1
    When the clock advances by 1 days
    And node "node-1" has 10 on-time handovers and 0 late handovers
    And the scorecards are recomputed
    Then node "node-1" is ineligible for the fast tiers
    And the reinstatement streak of node "node-1" is 2
    When the clock advances by 1 days
    And node "node-1" has 10 on-time handovers and 0 late handovers
    And the scorecards are recomputed
    Then node "node-1" is eligible for the fast tiers
    And the reinstatement streak of node "node-1" is 0
    And the events published were:
      | node-1:30d | NodeScorecardUpdated       |
      | node-1:7d  | NodeScorecardUpdated       |
      | node-1:7d  | NodeTierEligibilityChanged |
      | node-1:7d  | NodeTierEligibilityChanged |
    And a tier event was published for "node-1:7d" for the tier "SAME_DAY" with eligible true on value 0.9791666667 against the threshold 0.95
    And a tier event was published for "node-1:7d" for the tier "NEXT_DAY" with eligible true on value 0.9791666667 against the threshold 0.95

  Scenario: A recompute below 0.95 resets the streak
    Given node "node-1" has 50 on-time handovers and 10 late handovers
    When the scorecards are recomputed
    And node "node-1" has 400 on-time handovers and 0 late handovers
    And the scorecards are recomputed
    Then the reinstatement streak of node "node-1" is 1
    When the clock advances by 1 days
    And node "node-1" has 0 on-time handovers and 150 late handovers
    And the scorecards are recomputed
    Then node "node-1" is ineligible for the fast tiers
    And the reinstatement streak of node "node-1" is 0
