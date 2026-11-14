Feature: Threshold alert rules from the documentation
  ThresholdAlert (ADR 0002 A11): one NodeThresholdBreached per metric that
  moves from the healthy to the unhealthy side of its bound between two
  GRADED computations; the bound itself is healthy. The bounds are
  onTimeHandoverRate >= 0.90, promiseKeptRate >= 0.90, lateAckRate <= 0.05,
  rejectRate <= 0.10, expiryRate <= 0.05, feedFreshnessP95Minutes <= 240.

  Scenario Outline: The reject rate breaches only above 0.10
    Given node "node-1" has 90 purchase orders accepted early, 0 accepted late, 0 rejected and 0 expired
    When the scorecards are recomputed
    And node "node-1" has 0 purchase orders accepted early, 0 accepted late, <rejected> rejected and 0 expired
    And the scorecards are recomputed
    Then the metrics breached for "node-1:30d" were "<breached>"

    Examples:
      | rejected | breached   |
      | 10       |            |
      | 11       | rejectRate |

  Scenario Outline: The expiry rate breaches only above 0.05, and an expiry is also a late acknowledgement
    Given node "node-1" has 95 purchase orders accepted early, 0 accepted late, 0 rejected and 0 expired
    When the scorecards are recomputed
    And node "node-1" has 0 purchase orders accepted early, 0 accepted late, 0 rejected and <expired> expired
    And the scorecards are recomputed
    Then the metrics breached for "node-1:30d" were "<breached>"

    Examples:
      | expired | breached              |
      | 5       |                       |
      | 6       | lateAckRate,expiryRate |

  Scenario Outline: The promise-kept rate breaches only below 0.90
    Given node "node-1" has 90 kept promises and 0 promises broken by "LATE"
    When the scorecards are recomputed
    And node "node-1" has 0 kept promises and <broken> promises broken by "LATE"
    And the scorecards are recomputed
    Then the metrics breached for "node-1:30d" were "<breached>"

    Examples:
      | broken | breached        |
      | 10     |                 |
      | 11     | promiseKeptRate |

  Scenario Outline: Feed freshness breaches only above 240 minutes
    Given node "node-1" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    And node "node-1" applied feeds with the gaps "<gap>mx2"
    And the scorecards are recomputed
    Then the metrics breached for "node-1:30d" were "<breached>"

    Examples:
      | gap | breached                |
      | 240 |                         |
      | 241 | feedFreshnessP95Minutes |

  Scenario: Several metrics crossing in one recompute raise one alert each, in the order of the threshold table
    Given node "node-1" has 100 on-time handovers and 0 late handovers
    And node "node-1" has 100 kept promises and 0 promises broken by "LATE"
    When the scorecards are recomputed
    And node "node-1" has 0 on-time handovers and 25 late handovers
    And node "node-1" has 0 kept promises and 25 promises broken by "LATE"
    And the scorecards are recomputed
    Then the metrics breached for "node-1:30d" were "onTimeHandoverRate,promiseKeptRate"

  Scenario: An alert names the node and the window of the scorecard that crossed
    Given node "node-1" has 100 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    And node "node-1" has 0 on-time handovers and 25 late handovers
    And the scorecards are recomputed
    Then the "NodeThresholdBreached" event for "node-1:30d" is about the node "node-1" in the window "30d"
    And the "NodeThresholdBreached" event for "node-1:7d" is about the node "node-1" in the window "7d"

  Scenario: A metric that recovered and then falls again alerts again
    Given node "node-1" has 100 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    And node "node-1" has 0 on-time handovers and 25 late handovers
    And the scorecards are recomputed
    And node "node-1" has 200 on-time handovers and 0 late handovers
    And the scorecards are recomputed
    Then the metrics breached for "node-1:30d" were ""
    When node "node-1" has 0 on-time handovers and 40 late handovers
    And the scorecards are recomputed
    Then the metrics breached for "node-1:30d" were "onTimeHandoverRate"

  Scenario: A scorecard that was PROVISIONAL has no baseline, whatever its new grade
    Given node "node-1" has 49 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    And node "node-1" has 0 on-time handovers and 20 late handovers
    And the scorecards are recomputed
    Then the scorecard "node-1:30d" is GRADED with grade "D"
    And the metrics breached for "node-1:30d" were ""

  Scenario: Only the scorecard that was GRADED before can breach after a fall to PROVISIONAL and back
    Given node "node-1" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    And the clock advances by 9 days
    And the scorecards are recomputed
    And node "node-1" has 0 on-time handovers and 60 late handovers
    And the scorecards are recomputed
    Then the metrics breached for "node-1:30d" were "onTimeHandoverRate"
    And the metrics breached for "node-1:7d" were ""
