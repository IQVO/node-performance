import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/node-performance-api",
    },
    {
      type: "category",
      label: "Scorecards",
      link: {
        type: "doc",
        id: "api-reference/rest/scorecards",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-scorecard",
          label: "Get both scorecards (7-day and 30-day) of one node (GetScorecard)",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/list-scorecards-by-grade",
          label: "List the scorecards whose current grade matches (ListScorecardsByGrade)",
          className: "api-method get",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
