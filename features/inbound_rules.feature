Feature: Inbound metric rules from the documentation
  The informational inbound metrics (ADR 0009): a receipt is joined to its
  shipment by shipmentId, the first shipped instant wins, cartons are pooled
  across receipts, only RECEIVED cartons are accurate, the timeliness is a
  nearest-rank p95 and a receipt counts while it is after now minus the window.

  Background:
    Given node "node-1" has 50 on-time handovers and 0 late handovers
    And the scorecards are recomputed

  Scenario: A shipment announced twice keeps its first shipped instant
    Given shipment "shp-10" of node "node-1" shipped 300 minutes ago
    When shipment "shp-10" of node "node-1" shipped 100 minutes ago
    Then the outcome is "duplicate"
    When the receipt of shipment "shp-10" at node "node-1" is reported with cartons "RECEIVED" received 60 minutes ago
    Then the scorecard "node-1:30d" shows inbound timeliness p95 of 240 minutes

  Scenario: Accuracy pools the cartons of all receipts instead of averaging their rates
    Given shipment "shp-11" of node "node-1" shipped 300 minutes ago
    And shipment "shp-12" of node "node-1" shipped 300 minutes ago
    When the receipt of shipment "shp-11" at node "node-1" is reported with cartons "RECEIVED" received 100 minutes ago
    And the receipt of shipment "shp-12" at node "node-1" is reported with cartons "RECEIVED,DAMAGED,MISSING" received 100 minutes ago
    Then the scorecard "node-1:30d" shows inbound accuracy of 0.5

  Scenario Outline: Only a RECEIVED carton is accurate, any other status is not
    Given shipment "shp-13" of node "node-1" shipped 300 minutes ago
    When the receipt of shipment "shp-13" at node "node-1" is reported with cartons "<cartons>" received 100 minutes ago
    Then the scorecard "node-1:30d" shows inbound accuracy of <accuracy>

    Examples:
      | cartons          | accuracy |
      | RECEIVED,CRUSHED | 0.5      |
      | CRUSHED          | 0        |
      | RECEIVED         | 1        |

  Scenario: A receipt with no carton still gives a timeliness sample and no accuracy
    Given shipment "shp-14" of node "node-1" shipped 150 minutes ago
    When the receipt of shipment "shp-14" at node "node-1" is reported with cartons "" received 60 minutes ago
    Then the scorecard "node-1:30d" shows inbound timeliness p95 of 90 minutes
    And the scorecard "node-1:30d" shows no inbound accuracy

  Scenario Outline: The inbound timeliness is the nearest-rank 95th percentile of the receipts
    Given node "node-1" received <fast> shipments that took 60 minutes each
    And node "node-1" received <slow> shipments that took 600 minutes each
    Then the scorecard "node-1:30d" shows inbound timeliness p95 of <p95> minutes

    Examples:
      | fast | slow | p95 |
      | 19   | 1    | 60  |
      | 18   | 2    | 600 |

  Scenario: The list read carries the same inbound metrics as the node read
    Given shipment "shp-15" of node "node-1" shipped 300 minutes ago
    When the receipt of shipment "shp-15" at node "node-1" is reported with cartons "RECEIVED,RECEIVED,RECEIVED,DAMAGED" received 60 minutes ago
    Then listing the grade "A" shows inbound accuracy of 0.75 on "node-1:30d"
    And listing the grade "A" shows inbound timeliness p95 of 240 minutes on "node-1:7d"

  Scenario: A receipt exactly 7 days old has left the 7d window but not the 30d one
    Given shipment "shp-16" of node "node-1" shipped 20000 minutes ago
    When the receipt of shipment "shp-16" at node "node-1" is reported with cartons "RECEIVED" received 10080 minutes ago
    Then the scorecard "node-1:7d" shows no inbound metrics
    And the scorecard "node-1:30d" shows inbound timeliness p95 of 9920 minutes

  Scenario: A receipt one minute younger than 7 days still counts in the 7d window
    Given shipment "shp-17" of node "node-1" shipped 20000 minutes ago
    When the receipt of shipment "shp-17" at node "node-1" is reported with cartons "RECEIVED" received 10079 minutes ago
    Then the scorecard "node-1:7d" shows inbound timeliness p95 of 9921 minutes

  Scenario: A receipt exactly 30 days old counts in no window
    Given shipment "shp-18" of node "node-1" shipped 50000 minutes ago
    When the receipt of shipment "shp-18" at node "node-1" is reported with cartons "RECEIVED" received 43200 minutes ago
    Then the scorecard "node-1:30d" shows no inbound metrics
    And the scorecard "node-1:7d" shows no inbound metrics
