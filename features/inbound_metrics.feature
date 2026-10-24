Feature: Inbound timeliness and accuracy
  Two informational metrics per node window (ADR 0009): the p95 of the minutes
  between a shipment's shipped instant and its receipt, and the share of
  reported cartons that arrived RECEIVED. A receipt is joined to its shipment
  by shipmentId, whichever of the two events arrives first, and counts once.
  They are shown on the scorecard and never graded.

  Background:
    Given node "node-1" has 50 on-time handovers and 0 late handovers
    And the scorecards are recomputed

  Scenario: A node without inbound data shows no inbound metrics
    Then the scorecard "node-1:30d" shows no inbound metrics

  Scenario: A receipt joined to its shipment gives both metrics
    Given shipment "shp-1" of node "node-1" shipped 300 minutes ago
    When the receipt of shipment "shp-1" at node "node-1" is reported with cartons "RECEIVED,RECEIVED,RECEIVED,DAMAGED" received 60 minutes ago
    Then the outcome is "applied"
    And the scorecard "node-1:30d" shows inbound timeliness p95 of 240 minutes
    And the scorecard "node-1:30d" shows inbound accuracy of 0.75
    And the scorecard "node-1:7d" shows inbound timeliness p95 of 240 minutes

  Scenario: A receipt that arrives before its shipment is parked and joins later
    When the receipt of shipment "shp-2" at node "node-1" is reported with cartons "RECEIVED,MISSING" received 30 minutes ago
    Then the outcome is "applied"
    And the scorecard "node-1:30d" shows no inbound metrics
    When shipment "shp-2" of node "node-1" shipped 150 minutes ago
    Then the scorecard "node-1:30d" shows inbound timeliness p95 of 120 minutes
    And the scorecard "node-1:30d" shows inbound accuracy of 0.5

  Scenario: A redelivered receipt event is counted once
    Given shipment "shp-3" of node "node-1" shipped 100 minutes ago
    When the receipt event "evt-r3" of shipment "shp-3" at node "node-1" is delivered 2 times with cartons "RECEIVED,DAMAGED" received 10 minutes ago
    Then the outcome is "duplicate"
    And the scorecard "node-1:30d" shows inbound accuracy of 0.5

  Scenario: A second receipt of the same shipment does not count
    Given shipment "shp-4" of node "node-1" shipped 100 minutes ago
    And the receipt of shipment "shp-4" at node "node-1" is reported with cartons "RECEIVED,RECEIVED" received 20 minutes ago
    When the receipt of shipment "shp-4" at node "node-1" is reported with cartons "DAMAGED,DAMAGED" received 10 minutes ago
    Then the outcome is "duplicate"
    And the scorecard "node-1:30d" shows inbound accuracy of 1

  Scenario: A receipt stamped before its shipment is clamped to zero minutes
    Given shipment "shp-5" of node "node-1" shipped 10 minutes ago
    When the receipt of shipment "shp-5" at node "node-1" is reported with cartons "RECEIVED" received 20 minutes ago
    Then the scorecard "node-1:30d" shows inbound timeliness p95 of 0 minutes

  Scenario: A receipt older than 7 days counts in the 30d window only
    Given shipment "shp-6" of node "node-1" shipped 14400 minutes ago
    When the receipt of shipment "shp-6" at node "node-1" is reported with cartons "RECEIVED" received 12000 minutes ago
    Then the scorecard "node-1:30d" shows inbound timeliness p95 of 2400 minutes
    And the scorecard "node-1:7d" shows no inbound metrics

  Scenario: Inbound facts never change the grade, the observations or what is published
    Given shipment "shp-7" of node "node-1" shipped 100 minutes ago
    When the receipt of shipment "shp-7" at node "node-1" is reported with cartons "DAMAGED,MISSING" received 10 minutes ago
    And the scorecards are recomputed
    Then the scorecard "node-1:30d" is GRADED with grade "A"
    And the scorecard "node-1:30d" counts 50 observations
    And no event was published
