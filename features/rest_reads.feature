Feature: Reading and listing scorecards
  The two read operations of apis/openapi.yaml: GET /scorecards/{nodeId}
  answers both windows of an observed node, the 7-day one first, and a node
  nothing was observed about is not found; GET /scorecards lists one grade at
  a time, one page at a time, and refuses a grade, window, page size or
  cursor that is not an accepted value (validation-failed).

  Scenario: A node's scorecards are read 7d first, then 30d
    Given node "node-1" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    Then reading the node "node-1" returns the scorecards "node-1:7d,node-1:30d"

  Scenario: A nodeId that cannot be a node id is refused
    Then reading the node "node:1" is refused as invalid

  Scenario: The window filter restricts the list to one window
    Given node "node-a" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    Then listing the grade "A" returns "node-a:30d,node-a:7d"
    And listing the grade "A" in window "7d" returns "node-a:7d"
    And listing the grade "A" in window "30d" returns "node-a:30d"

  Scenario: A window that is neither 7d nor 30d is refused
    Given node "node-a" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    Then listing the grade "A" in window "14d" is refused as invalid

  Scenario: The list is read one page at a time by cursor
    Given node "node-a" has 50 on-time handovers and 0 late handovers
    And node "node-b" has 50 kept promises and 0 promises broken by "LATE"
    When the scorecards are recomputed
    Then listing the grade "A" with a page of 3 returns "node-a:30d,node-a:7d,node-b:30d" and the next cursor "node-b:30d"
    And listing the grade "A" after "node-b:30d" with a page of 3 returns "node-b:7d" and no next cursor

  Scenario: A page size above the maximum of 500 is refused
    Given node "node-a" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    Then listing the grade "A" with a page of 501 is refused as invalid

  Scenario: A cursor longer than the longest scorecardId is refused
    Given node "node-a" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    Then listing after a cursor of 81 characters is refused as invalid
