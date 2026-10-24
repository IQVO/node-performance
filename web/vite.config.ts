import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { federation } from "@module-federation/vite";

// node-performance-mfe: the node-performance remote. Exposes ./App -- the
// warehouse-console shell lazy-loads it as `node_performance_mfe/App` and
// mounts it under its own `/node-performance/*` splat route. Also runnable
// standalone on :4016 for local development without the shell (see main.tsx).
//
// PINNED host/remote contract (warehouse-console vite.config.ts will carry the
// matching entry; change both together or not at all):
//   federation container  node_performance_mfe
//   exposed module        ./App (default export, no props)
//   production base       /mfes/node-performance/   (Nginx web gateway path)
//   dev/preview port      4016 (unique on the fleet's 5181+ ladder; every
//                          5181-5197/4010/4011 slot is taken)
//
// `base` is the deployment-time asset namespace. In the kind cluster this
// remote is served by its own nginx pod behind the Nginx web gateway at
// http://localhost/mfes/node-performance/, so every hashed chunk and the
// federation remoteEntry.js must resolve under that prefix -- otherwise a
// dynamically imported chunk would request /assets/... at the shell's origin
// root and collide with every other remote's assets.
//
// Kept as a plain object rather than the ({ command }) => ({...}) callback
// form on purpose: a callback export cannot be merged by Vite's mergeConfig
// ("Cannot merge config in form of callback"), which has killed whole test
// suites elsewhere in the fleet. Reading the command off process.argv keeps
// this a static object. (vitest.config.ts here is standalone and does not
// import this file at all.)
const IS_BUILD = process.argv.includes("build");
const PUBLIC_BASE = IS_BUILD ? "/mfes/node-performance/" : "/";

export default defineConfig({
  base: PUBLIC_BASE,
  plugins: [
    react(),
    federation({
      name: "node_performance_mfe",
      filename: "remoteEntry.js",
      // No federated type archive: the console imports this remote behind
      // `// @ts-expect-error` and never downloads its types, and the DTS
      // worker cannot compile through the solution-style tsconfig.json
      // (no `jsx` at the root), so it would only ever log a TYPE-001 error.
      dts: false,
      exposes: {
        "./App": "./src/App.tsx",
      },
      shared: {
        react: { singleton: true, requiredVersion: "^19.2.8" },
        "react-dom": { singleton: true, requiredVersion: "^19.2.8" },
        "react-router-dom": { singleton: true, requiredVersion: "^7.18.3" },
        "@warehouse/ui-kit": { singleton: true },
      },
    }),
  ],
  server: {
    port: 4016,
    strictPort: true,
    cors: true,
    origin: "http://localhost:4016",
  },
  preview: {
    port: 4016,
    strictPort: true,
    cors: true,
  },
  build: {
    target: "esnext",
    modulePreload: false,
  },
});
