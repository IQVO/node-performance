Feature: Intake rules from the documentation
  What the service does with each consumed fact (ADR 0002 A2-A8, A16, A20 and
  ADR 0003): which facts are outcomes, which only complete a purchase order,
  how a redelivery, an unusable envelope or an unusual timestamp is treated.

  Scenario: An expiry is counted even though the issue of its purchase order was never seen
    When the purchase order "po-77" of node "node-1" expires without its issue having been seen
    Then the outcome is "applied"
    And the scorecard "node-1:30d" holds 1 observations
    And the scorecard "node-1:7d" holds 1 observations

  Scenario: A shipment confirmation alone cannot judge an acknowledgement
    Given the vendor confirms the shipment of purchase order "po-78" of node "node-1" with a required ship-by in 100 minutes
    When the unknown purchase order "po-78" of node "node-1" is accepted
    Then the command fails with "purchase order not known yet"
    And the scorecard "node-1:7d" does not exist

  Scenario: The issue of a purchase order arriving after its shipment confirmation completes the facts
    Given the vendor confirms the shipment of purchase order "po-79" of node "node-1" with a required ship-by in 100 minutes
    And purchase order "po-79" of node "node-1" was issued with an ack window of 600 minutes and a required ship-by 1800 minutes after the issue
    When purchase order "po-79" of node "node-1" is accepted 100 minutes after the issue
    Then the outcome is "applied"
    And the scorecard "node-1:30d" holds 1 observations

  Scenario: A required ship-by already known is never overwritten by a later confirmation
    Given purchase order "po-80" of node "node-1" was issued with an ack window of 600 minutes and a required ship-by 1800 minutes after the issue
    And the vendor confirms the shipment of purchase order "po-80" of node "node-1" with a required ship-by in 100 minutes
    When the package of purchase order "po-80" of node "node-1" is handed over 500 minutes after the issue
    And the scorecards are recomputed
    Then the metric "onTimeHandoverRate" of the scorecard "node-1:30d" is 1

  Scenario Outline: A handover is on time up to its required ship-by and late right after it
    Given purchase order "po-81" of node "node-1" was issued with an ack window of 600 minutes and a required ship-by 1800 minutes after the issue
    When the package of purchase order "po-81" of node "node-1" is handed over <minutes> minutes after the issue
    And the scorecards are recomputed
    Then the metric "onTimeHandoverRate" of the scorecard "node-1:30d" is <rate>

    Examples:
      | minutes | rate |
      | 1799    | 1    |
      | 1801    | 0    |

  Scenario Outline: The late part of an acknowledgement is a tenth of that order's own ack window
    Given purchase order "po-82" of node "node-1" was issued with an ack window of <window> minutes and a required ship-by 3000 minutes after the issue
    When purchase order "po-82" of node "node-1" is accepted <minutes> minutes after the issue
    And the scorecards are recomputed
    Then the metric "lateAckRate" of the scorecard "node-1:30d" is <late>

    Examples:
      | window | minutes | late |
      | 100    | 89      | 0    |
      | 100    | 90      | 1    |
      | 1000   | 899     | 0    |
      | 1000   | 900     | 1    |

  Scenario Outline: A redelivered event is counted once, whatever kind of fact it carries
    When the <kind> event "evt-dup" for node "node-1" is delivered 2 times
    Then the outcome is "duplicate"
    And the scorecard "node-1:30d" holds 1 observations
    And the scorecard "node-1:7d" holds 1 observations

    Examples:
      | kind                        |
      | package handover            |
      | purchase order acceptance   |
      | purchase order expiry       |
      | inventory feed              |
      | promise broken by LATE      |

  Scenario Outline: A promise broken for a cause that is not the node's is acknowledged once and never counted
    When the <kind> event "evt-lost" for node "node-1" is delivered 2 times
    Then the outcome is "duplicate"
    And the scorecard "node-1:7d" does not exist

    Examples:
      | kind                    |
      | promise broken by LOST     |
      | promise broken by RETURNED |

  Scenario: An event with no effect is a duplicate the second time it arrives
    When an event without effect "evt-profile" is delivered
    And an event without effect "evt-profile" is delivered
    Then the outcome is "duplicate"

  Scenario: An event without a CloudEvents id can never be applied
    When a promise kept event "" is delivered for node "node-1"
    Then the delivery is refused as an invalid event
    And the scorecard "node-1:7d" does not exist

  Scenario Outline: A CloudEvents id is accepted up to 200 characters
    When a promise kept event whose id has <length> characters is delivered for node "node-1"
    Then the delivery is <answer>

    Examples:
      | length | answer                     |
      | 200    | applied                    |
      | 201    | refused as an invalid event |

  Scenario Outline: A node id is accepted up to 64 characters
    When a promise kept for a node whose id has <length> characters is delivered
    Then the delivery is <answer>

    Examples:
      | length | answer                     |
      | 64     | applied                    |
      | 65     | refused as an invalid event |

  Scenario: A node id with a colon would corrupt the scorecardId and is refused
    When a promise kept event "evt-colon" is delivered for node "node:1"
    Then the delivery is refused as an invalid event

  Scenario: A kept promise without a delivery instant can never be applied
    When a promise kept event "evt-nodate" without a delivery instant is delivered for node "node-1"
    Then the delivery is refused as an invalid event
    And the scorecard "node-1:7d" does not exist

  Scenario Outline: A broken promise without a delivery instant is dated by its envelope
    Given a promise broken by "LATE" for node "node-1" without a delivery instant is stamped <days> days ago
    When the scorecards are recomputed
    Then the scorecard "node-1:30d" counts <in30d> observations
    And the scorecard "node-1:7d" counts <in7d> observations

    Examples:
      | days | in30d | in7d |
      | 3    | 1     | 1    |
      | 10   | 1     | 0    |
      | 40   | 0     | 0    |

  Scenario: A fact stamped in the future still counts, clocks skew
    Given node "node-1" applied inventory feeds at the minutes "2880"
    When the scorecards are recomputed
    Then the scorecard "node-1:30d" counts 1 observations
    And the scorecard "node-1:7d" counts 1 observations

  Scenario: Promises broken by LOST do not count toward the 50 outcomes of a grade
    Given node "node-1" has 49 on-time handovers and 0 late handovers
    And node "node-1" has 0 kept promises and 5 promises broken by "LOST"
    When the scorecards are recomputed
    Then the scorecard "node-1:30d" is PROVISIONAL
    And the scorecard "node-1:30d" counts 49 observations
    And no event was published
