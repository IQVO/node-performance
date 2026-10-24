import { Navigate, NavLink, Outlet, Route, Routes } from "react-router-dom";
import { ScorecardListScreen } from "./screens/ScorecardListScreen";
import { NodeLookupScreen, NodeRoute } from "./screens/NodeScreen";

const SUB_NAV = [
  { to: "scorecards", label: "Scorecards" },
  { to: "nodes", label: "Node lookup" },
];

const linkStyle = ({ isActive }: { isActive: boolean }) => ({
  display: "inline-flex",
  padding: "6px 12px",
  borderRadius: "var(--wh-radius-pill)",
  fontSize: "var(--wh-font-size-sm)",
  fontWeight: isActive ? 600 : 500,
  color: isActive ? "var(--wh-color-text)" : "var(--wh-color-text-muted)",
  background: isActive ? "var(--wh-color-accent-muted)" : "transparent",
  textDecoration: "none",
});

/** The sub-nav, rendered by a layout route with the path "/".
 *
 *  The console mounts this component inside its own `<Route
 *  path="/node-performance/*">`. A relative `<NavLink>` rendered straight in
 *  that splat route resolves against the FULL current URL (react-router 7
 *  uses the last path-contributing match's pathname, splat included), so from
 *  /node-performance/nodes the link `scorecards` would point at
 *  /node-performance/nodes/scorecards. A pathless layout route does not help
 *  (react-router drops pathless routes from relative resolution); a layout
 *  route WITH path "/" does: its own match is the mount point, under any
 *  prefix and standalone at `/`. (Same fix as the other fleet remotes;
 *  App.test.tsx mounts under the host's splat route to prove it.) */
function PerformanceLayout() {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-5)" }}>
      <nav aria-label="Node performance sections" style={{ display: "flex", gap: "var(--wh-space-2)" }}>
        {SUB_NAV.map((item) => (
          <NavLink key={item.to} to={item.to} style={linkStyle}>
            {item.label}
          </NavLink>
        ))}
      </nav>
      <Outlet />
    </div>
  );
}

/** Exposed as node_performance_mfe/App via Module Federation. Takes NO props:
 *  the console mounts it under `/node-performance/*` and provides the
 *  BrowserRouter, the design tokens and `window.__WAREHOUSE_CONFIG__`. Routes
 *  here are RELATIVE, so the component works identically under that prefix (in
 *  the shell) or at / (standalone dev, see main.tsx). It calls ONLY this
 *  context's own API (/api/node-performance), and it is read-only: it renders
 *  the two scorecard reads and changes nothing.
 *
 *   - (index): redirects to scorecards.
 *   - scorecards: the scorecards of one grade, with the window filter and paging.
 *   - nodes: a node id lookup; nodes/:nodeId: both windows of the node. */
export default function App() {
  return (
    <Routes>
      <Route path="/" element={<PerformanceLayout />}>
        <Route index element={<Navigate to="scorecards" replace />} />
        <Route path="scorecards" element={<ScorecardListScreen />} />
        <Route path="nodes" element={<NodeLookupScreen />} />
        <Route path="nodes/:nodeId" element={<NodeRoute />} />
      </Route>
    </Routes>
  );
}
