Feature: Metric, grade and recompute rules from the documentation
  How the metrics are computed from the observations of a window (ADR 0002
  A5, A9, A10, A13-A15) and what a recompute does with a scorecard that has
  nothing new, little data, or data that slid out of its window.

  Scenario: A PROVISIONAL scorecard still shows what was observed
    Given node "node-1" has 3 on-time handovers and 1 late handovers
    When the scorecards are recomputed
    Then the scorecard "node-1:30d" is PROVISIONAL
    And the metric "onTimeHandoverRate" of the scorecard "node-1:30d" is 0.75
    And the scorecard "node-1:30d" counts 4 observations
    And no event was published

  Scenario: Feed freshness orders the feeds by their own instant, not by arrival
    Given node "node-1" applied inventory feeds at the minutes "0,30,10,20"
    When the scorecards are recomputed
    Then the metric "feedFreshnessP95Minutes" of the scorecard "node-1:30d" is 10

  Scenario Outline: Feed freshness is the nearest-rank 95th percentile of the gaps between feeds
    Given node "node-1" applied feeds with the gaps "<gaps>"
    When the scorecards are recomputed
    Then the metric "feedFreshnessP95Minutes" of the scorecard "node-1:30d" is <p95>

    Examples:
      | gaps           | p95 |
      | 10mx19,300mx1  | 10  |
      | 10mx18,300mx2  | 300 |
      | 10mx9,60mx1    | 60  |
      | 45mx1          | 45  |

  Scenario: The time since the last feed is not a sample
    Given node "node-1" applied 10 inventory feeds, one every 10 minutes
    When the clock advances by 3 hours
    And node "node-1" has 1 kept promises and 0 promises broken by "LATE"
    And the scorecards are recomputed
    Then the metric "feedFreshnessP95Minutes" of the scorecard "node-1:30d" is 10
    And the scorecard "node-1:30d" counts 11 observations

  Scenario: Grades compare the unrounded rate, so 97 of 99 handovers on time is not an A
    Given node "node-1" has 97 on-time handovers and 2 late handovers
    And node "node-1" has 100 kept promises and 0 promises broken by "LATE"
    When the scorecards are recomputed
    Then the scorecard "node-1:30d" is GRADED with grade "B"

  Scenario: The 7-day scorecard forgets late handovers that the 30-day scorecard remembers
    Given node "node-1" has 45 on-time handovers and 5 late handovers
    When the clock advances by 9 days
    And node "node-1" has 50 on-time handovers and 0 late handovers
    And the scorecards are recomputed
    Then listing the grade "A" returns "node-1:7d"
    And listing the grade "B" returns "node-1:30d"

  Scenario: A regrade that crosses no threshold publishes the update and no alert
    Given node "node-1" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    And node "node-1" has 0 on-time handovers and 4 late handovers
    And the scorecards are recomputed
    Then a "NodeScorecardUpdated" event was published for "node-1:30d" carrying the grade "C"
    And the metrics breached for "node-1:30d" were ""

  Scenario: The 50th observation moves a PROVISIONAL scorecard to GRADED and publishes it
    Given node "node-1" has 49 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    Then no event was published
    When node "node-1" has 1 on-time handovers and 0 late handovers
    And the scorecards are recomputed
    Then the scorecard "node-1:30d" is GRADED with grade "A"
    And the events published were:
      | node-1:30d | NodeScorecardUpdated |
      | node-1:7d  | NodeScorecardUpdated |

  Scenario: A scorecard with nothing new is not recomputed again
    Given node "node-1" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    Then the scorecard "node-1:30d" has version 51
    When the scorecards are recomputed
    Then the scorecard "node-1:30d" has version 51
    And no event was published

  Scenario: A recompute touches only the nodes that have news
    Given node "node-a" has 50 on-time handovers and 0 late handovers
    And the scorecards are recomputed
    And node "node-b" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    Then the events published were:
      | node-b:30d | NodeScorecardUpdated |
      | node-b:7d  | NodeScorecardUpdated |

  Scenario Outline: An observation leaves its window at the instant the window no longer reaches it
    Given node "node-1" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    And the clock advances by <minutes> minutes
    And the scorecards are recomputed
    Then the scorecard "node-1:<window>" is <state>
    And the scorecard "node-1:<window>" counts <count> observations

    Examples: handovers are stamped 1700 minutes after the start, the windows are 10080 and 43200 minutes
      | window | minutes | state       | count |
      | 7d     | 11779   | GRADED      | 50    |
      | 7d     | 11780   | PROVISIONAL | 0     |
      | 30d    | 44899   | GRADED      | 50    |
      | 30d    | 44900   | PROVISIONAL | 0     |
