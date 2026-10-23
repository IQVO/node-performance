Feature: Recording observations
  Every consumed fact that measures a node becomes an observation on both of its
  scorecards, the 7-day and the 30-day one. The first observation of a node
  creates them, PROVISIONAL. An observation is counted once, whatever the
  delivery count of the event that carried it.

  Scenario: The first observation of a node creates both scorecards
    Given node "node-1" applied 1 inventory feeds, one every 10 minutes
    Then the scorecard "node-1:7d" is PROVISIONAL
    And the scorecard "node-1:30d" is PROVISIONAL
    And the scorecard "node-1:7d" holds 1 observations
    And the scorecard "node-1:30d" has not been computed yet
    And the scorecard "node-2:7d" does not exist

  Scenario: An observation counts on both windows
    Given node "node-1" applied 3 inventory feeds, one every 10 minutes
    Then the scorecard "node-1:7d" holds 3 observations
    And the scorecard "node-1:30d" holds 3 observations
    And the scorecard "node-1:30d" has version 3

  Scenario: A redelivered event is counted once
    When the promise kept event "evt-1" for node "node-1" is delivered 3 times
    Then the outcome is "duplicate"
    And the scorecard "node-1:30d" holds 1 observations
    And the scorecard "node-1:30d" has version 1

  Scenario: An event with no effect only reaches the inbox
    When an event without effect "evt-9" is delivered
    Then the outcome is "ignored"
    And the scorecard "node-9:7d" does not exist

  Scenario: Issuing a purchase order records facts, not an observation
    Given purchase order "po-1" of node "node-1" was issued with an ack window of 600 minutes and a required ship-by 1800 minutes after the issue
    Then the outcome is "applied"
    And the scorecard "node-1:7d" does not exist

  Scenario: An acceptance of a purchase order the service never saw issued is transient
    When the unknown purchase order "po-ghost" of node "node-1" is accepted
    Then the command fails with "purchase order not known yet"
    And the scorecard "node-1:7d" does not exist

  Scenario: A handover of a purchase order the service knows nothing about is transient
    When the package of purchase order "po-ghost" of node "node-1" is handed over 10 minutes after the issue
    Then the command fails with "purchase order not known yet"
    And the scorecard "node-1:7d" does not exist

  Scenario: A shipment confirmation alone lets a handover be judged
    Given the vendor confirms the shipment of purchase order "po-1" of node "node-1" with a required ship-by in 100 minutes
    When the package of purchase order "po-1" of node "node-1" is handed over 50 minutes after the issue
    Then the outcome is "applied"
    And the scorecard "node-1:7d" holds 1 observations

  Scenario Outline: A broken promise counts against the node only for a node-attributable cause
    Given node "node-1" has 1 kept promises and 1 promises broken by "<cause>"
    When the scorecards are recomputed
    Then the scorecard "node-1:30d" counts <observations> observations
    And the metric "promiseKeptRate" of the scorecard "node-1:30d" is <rate>

    Examples:
      | cause         | observations | rate |
      | LATE          | 2            | 0.5  |
      | LATE_HANDOVER | 2            | 0.5  |
      | VENDOR_REJECT | 2            | 0.5  |
      | ACK_EXPIRED   | 2            | 0.5  |
      | LOST          | 1            | 1    |
      | RETURNED      | 1            | 1    |
      | SOMETHING_NEW | 1            | 1    |
