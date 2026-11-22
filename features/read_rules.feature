Feature: Read rules from the documentation
  The two reads of apis/openapi.yaml: GET /scorecards/{nodeId} (not-found
  until the first observation, validation-failed for an id that cannot be a
  node id) and GET /scorecards?grade= (grade A, B, C, D or PROVISIONAL, limit 1
  to 500, cursor up to 80 characters, nextCursor only when more rows exist).

  Scenario: A node nothing was observed about is not found
    Then reading the node "node-9" is not found

  Scenario Outline: A node id of 64 characters is a valid id, 65 is not
    Then reading a node whose id has <length> characters is <answer>

    Examples:
      | length | answer             |
      | 64     | not found          |
      | 65     | refused as invalid |

  Scenario Outline: An id that is not 1 to 64 characters from A-Z a-z 0-9 . _ - starting with a letter or digit is refused
    Then reading the node "<id>" is refused as invalid

    Examples:
      | id      |
      | -node   |
      | .node   |
      | _node   |
      | node 1  |
      | node/1  |
      | node:7d |

  Scenario: A cursor of exactly 80 characters is accepted
    Given node "node-a" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    Then listing after a cursor of 80 characters returns "node-a:30d,node-a:7d"

  Scenario: A page that exactly exhausts the list advertises no next cursor
    Given node "node-a" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    Then listing the grade "A" with a page of 2 returns "node-a:30d,node-a:7d" and no next cursor

  Scenario: A page of 500, the maximum, is accepted
    Given node "node-a" has 50 on-time handovers and 0 late handovers
    When the scorecards are recomputed
    Then listing the grade "A" with a page of 500 returns "node-a:30d,node-a:7d" and no next cursor

  Scenario Outline: A grade that is not A, B, C, D or PROVISIONAL is refused
    Then listing the grade "<grade>" is refused as invalid

    Examples:
      | grade       |
      | a           |
      | E           |
      | GRADED      |
      | provisional |

  Scenario: A scorecard is listed under the grade it holds, B and C included
    Given node "node-b" has 95 on-time handovers and 5 late handovers
    And node "node-c" has 90 on-time handovers and 10 late handovers
    When the scorecards are recomputed
    Then listing the grade "B" returns "node-b:30d,node-b:7d"
    And listing the grade "C" returns "node-c:30d,node-c:7d"
