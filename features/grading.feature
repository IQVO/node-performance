Feature: Grading and publishing
  A scorecard with fewer than 50 observations is PROVISIONAL and never
  publishes. From 50 it is graded A, B, C or D by the Grading policy and
  publishes NodeScorecardUpdated; a graded scorecard that falls back below the
  minimum publishes the fall.

  Scenario: Below the observation minimum a scorecard stays PROVISIONAL and publishes nothing
    Given node "node-1" has 49 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    Then the scorecard "node-1:30d" is PROVISIONAL
    And the scorecard "node-1:7d" is PROVISIONAL
    And the scorecard "node-1:30d" counts 49 observations
    And no event was published

  Scenario: At the observation minimum a scorecard is graded and published
    Given node "node-1" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    Then the scorecard "node-1:30d" is GRADED with grade "A"
    And the scorecard "node-1:7d" is GRADED with grade "A"
    And the events published were:
      | node-1:30d | NodeScorecardUpdated |
      | node-1:7d  | NodeScorecardUpdated |

  Scenario Outline: The grade is the best one whose three bars are all met
    Given node "node-1" has <ontime> on-time handovers and <late> late handovers
    And node "node-1" has <kept> kept promises and <broken> promises broken by "LATE"
    When the scorecards are recomputed
    Then the scorecard "node-1:30d" is GRADED with grade "<grade>"

    Examples:
      | ontime | late | kept | broken | grade |
      | 100    | 0    | 100  | 0      | A     |
      | 98     | 2    | 97   | 3      | A     |
      | 97     | 3    | 100  | 0      | B     |
      | 98     | 2    | 96   | 4      | B     |
      | 95     | 5    | 94   | 6      | B     |
      | 94     | 6    | 100  | 0      | C     |
      | 95     | 5    | 93   | 7      | C     |
      | 90     | 10   | 90   | 10     | C     |
      | 89     | 11   | 100  | 0      | D     |
      | 100    | 0    | 89   | 11     | D     |

  Scenario Outline: The late-ack rate bar of each grade
    Given node "node-1" has 100 on-time handovers and 0 late handovers
    And node "node-1" has 100 kept promises and 0 promises broken by "LATE"
    And node "node-1" has <early> purchase orders accepted early, <late> accepted late, 0 rejected and 0 expired
    When the scorecards are recomputed
    Then the scorecard "node-1:30d" is GRADED with grade "<grade>"

    Examples:
      | early | late | grade |
      | 99    | 1    | A     |
      | 98    | 2    | B     |
      | 97    | 3    | B     |
      | 96    | 4    | C     |
      | 95    | 5    | C     |
      | 94    | 6    | D     |

  Scenario: A graded scorecard that loses observations falls back to PROVISIONAL and publishes the fall
    Given node "node-1" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    And the clock advances by 9 days
    And the scorecards are recomputed
    Then the events published were:
      | node-1:7d | NodeScorecardUpdated |
    And the scorecard "node-1:7d" is PROVISIONAL
    And the scorecard "node-1:7d" counts 0 observations
    And the scorecard "node-1:30d" is GRADED with grade "A"

  Scenario: Observations leave the 30-day window too
    Given node "node-1" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    And the clock advances by 32 days
    And the scorecards are recomputed
    Then the scorecard "node-1:30d" is PROVISIONAL
    And the scorecard "node-1:30d" counts 0 observations

  Scenario: Scorecards can be listed by their grade
    Given node "node-a" has 50 on-time handovers and 0 late handovers
    And node "node-b" applied 1 inventory feeds, one every 10 minutes
    When the scorecards are recomputed
    Then listing the grade "A" returns "node-a:30d,node-a:7d"
    And listing the grade "PROVISIONAL" returns "node-b:30d,node-b:7d"
    And listing the grade "D" returns ""
    And listing the grade "E" is refused as invalid
