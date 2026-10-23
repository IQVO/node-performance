Feature: Metric definitions
  Each metric is a numerator over a denominator with a better direction. A
  metric with an empty denominator reports its best value.

  Scenario: A node with nothing measured yet reports the best values
    Given node "node-1" applied 1 inventory feeds, one every 10 minutes
    When the scorecards are recomputed
    Then the metric "onTimeHandoverRate" of the scorecard "node-1:30d" is 1
    And the metric "lateAckRate" of the scorecard "node-1:30d" is 0
    And the metric "promiseKeptRate" of the scorecard "node-1:30d" is 1
    And the metric "feedFreshnessP95Minutes" of the scorecard "node-1:30d" is 0

  Scenario: On-time handover rate is on-time handovers over handovers
    Given node "node-1" has 3 on-time handovers and 1 late handovers
    When the scorecards are recomputed
    Then the metric "onTimeHandoverRate" of the scorecard "node-1:30d" is 0.75

  Scenario: A handover exactly on requiredShipBy is on time
    Given purchase order "po-1" of node "node-1" was issued with an ack window of 600 minutes and a required ship-by 1800 minutes after the issue
    When the package of purchase order "po-1" of node "node-1" is handed over 1800 minutes after the issue
    And the scorecards are recomputed
    Then the metric "onTimeHandoverRate" of the scorecard "node-1:30d" is 1

  Scenario: Acknowledgement rates share one denominator
    Given node "node-1" has 5 purchase orders accepted early, 3 accepted late, 1 rejected and 1 expired
    When the scorecards are recomputed
    Then the metric "lateAckRate" of the scorecard "node-1:30d" is 0.4
    And the metric "rejectRate" of the scorecard "node-1:30d" is 0.1
    And the metric "expiryRate" of the scorecard "node-1:30d" is 0.1
    And the scorecard "node-1:30d" counts 10 observations

  Scenario Outline: An acknowledgement in the last 10% of the ack window is late
    Given purchase order "po-1" of node "node-1" was issued with an ack window of 600 minutes and a required ship-by 1800 minutes after the issue
    When purchase order "po-1" of node "node-1" is <verb> <minutes> minutes after the issue
    And the scorecards are recomputed
    Then the metric "lateAckRate" of the scorecard "node-1:30d" is <late>

    Examples:
      | verb     | minutes | late |
      | accepted | 0       | 0    |
      | accepted | 539     | 0    |
      | accepted | 540     | 1    |
      | accepted | 600     | 1    |
      | rejected | 539     | 0    |
      | rejected | 540     | 1    |

  Scenario: An expired purchase order is always late
    Given purchase order "po-1" of node "node-1" was issued with an ack window of 600 minutes and a required ship-by 1800 minutes after the issue
    When purchase order "po-1" of node "node-1" expires
    And the scorecards are recomputed
    Then the metric "lateAckRate" of the scorecard "node-1:30d" is 1
    And the metric "expiryRate" of the scorecard "node-1:30d" is 1

  Scenario: Feed freshness is the p95 of the gaps between feeds
    Given node "node-1" applied 10 inventory feeds, one every 10 minutes
    When the clock advances by 300 minutes
    And node "node-1" applied 1 inventory feeds, one every 10 minutes
    And the scorecards are recomputed
    Then the metric "feedFreshnessP95Minutes" of the scorecard "node-1:30d" is 210

  Scenario: Regular feeds are as fresh as their cadence
    Given node "node-1" applied 21 inventory feeds, one every 10 minutes
    When the scorecards are recomputed
    Then the metric "feedFreshnessP95Minutes" of the scorecard "node-1:30d" is 10
