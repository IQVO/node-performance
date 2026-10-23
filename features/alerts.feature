Feature: Threshold alerts
  NodeThresholdBreached is raised, analytics only, when a metric moves to the
  unhealthy side of its threshold between two GRADED recomputes. Without a
  GRADED baseline there is nothing to cross.

  Scenario: A metric crossing its threshold downward raises an alert
    Given node "node-1" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    And node "node-1" has 0 on-time handovers and 6 late handovers
    And the scorecards are recomputed
    Then the events published were:
      | node-1:30d | NodeScorecardUpdated  |
      | node-1:30d | NodeThresholdBreached |
      | node-1:7d  | NodeScorecardUpdated  |
      | node-1:7d  | NodeThresholdBreached |

  Scenario: A metric that is still healthy raises no alert
    Given node "node-1" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    And node "node-1" has 0 on-time handovers and 5 late handovers
    And the scorecards are recomputed
    Then the events published were:
      | node-1:30d | NodeScorecardUpdated |
      | node-1:7d  | NodeScorecardUpdated |

  Scenario: A metric that stays unhealthy does not alert again
    Given node "node-1" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    And node "node-1" has 0 on-time handovers and 6 late handovers
    And the scorecards are recomputed
    And node "node-1" has 0 on-time handovers and 3 late handovers
    And the scorecards are recomputed
    Then the events published were:
      | node-1:30d | NodeScorecardUpdated |
      | node-1:7d  | NodeScorecardUpdated |

  Scenario: The first graded recompute has no baseline, so it raises no alert
    Given node "node-1" has 0 on-time handovers and 50 late handovers
    When the scorecards are recomputed
    Then the scorecard "node-1:30d" is GRADED with grade "D"
    And the events published were:
      | node-1:30d | NodeScorecardUpdated |
      | node-1:7d  | NodeScorecardUpdated |

  Scenario: A metric where lower is better alerts when it rises above its threshold
    Given node "node-1" has 100 purchase orders accepted early, 0 accepted late, 0 rejected and 0 expired
    When the scorecards are recomputed
    And node "node-1" has 0 purchase orders accepted early, 6 accepted late, 0 rejected and 0 expired
    And the scorecards are recomputed
    Then the events published were:
      | node-1:30d | NodeScorecardUpdated  |
      | node-1:30d | NodeThresholdBreached |
      | node-1:7d  | NodeScorecardUpdated  |
      | node-1:7d  | NodeThresholdBreached |
