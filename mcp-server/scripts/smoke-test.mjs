#!/usr/bin/env node
// smoke-test.mjs — speak MCP JSON-RPC to the x402-mcp server over stdio
// and verify list/call work end-to-end. Useful as a CI check and as a way
// to validate the MCP layer without needing Claude Code installed.
//
// Usage:
//   node scripts/smoke-test.mjs                  # dry-run, no money spent
//   LIVE=true node scripts/smoke-test.mjs        # live mode, real USDC settlement
//
// Env vars:
//   STELLARFLOW_BASE_URL  (default http://localhost:8086)
//   LIVE               (default false)
//
// In LIVE mode the script makes a single sentiment call (~$0.05 USDC) to
// validate the full x402 flow against the OZ facilitator with minimum spend.
// The wallet loaded from ~/.config/x402-mcp/agent.json must have at least
// $0.15 USDC and an established USDC trustline.

import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const SERVER_PATH = path.resolve(__dirname, "..", "dist", "cli.js");

const LIVE_MODE = (process.env.LIVE ?? "").toLowerCase() === "true";

const child = spawn("node", [SERVER_PATH], {
  stdio: ["pipe", "pipe", "inherit"],
  env: {
    ...process.env,
    STELLARFLOW_BASE_URL: process.env.STELLARFLOW_BASE_URL ?? "http://localhost:8086",
    STELLARFLOW_CLIENT_ID: "mcp-smoke-test",
    BUDGET_USDC_PER_SESSION: "1.00",
    STELLARFLOW_DRY_RUN: LIVE_MODE ? "false" : "true",
  },
});

const rl = createInterface({ input: child.stdout });
const responses = new Map();
let nextID = 1;

rl.on("line", (line) => {
  if (!line.trim()) return;
  let msg;
  try {
    msg = JSON.parse(line);
  } catch {
    return;
  }
  if (typeof msg.id !== "undefined") {
    const resolver = responses.get(msg.id);
    if (resolver) {
      responses.delete(msg.id);
      resolver(msg);
    }
  }
});

function send(method, params) {
  return new Promise((resolve, reject) => {
    const id = nextID++;
    responses.set(id, (msg) => {
      if (msg.error) reject(new Error(JSON.stringify(msg.error)));
      else resolve(msg.result);
    });
    child.stdin.write(JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n");
  });
}

function notify(method, params) {
  child.stdin.write(JSON.stringify({ jsonrpc: "2.0", method, params }) + "\n");
}

async function main() {
  // 1. Initialize
  console.log("\n[1] initialize");
  const init = await send("initialize", {
    protocolVersion: "2024-11-05",
    capabilities: {},
    clientInfo: { name: "x402-mcp-smoke-test", version: "0.0.1" },
  });
  console.log("    server:", init.serverInfo?.name, init.serverInfo?.version);
  console.log("    capabilities:", JSON.stringify(init.capabilities));

  notify("notifications/initialized");

  // 2. List tools
  console.log("\n[2] tools/list");
  const list = await send("tools/list", {});
  console.log(`    ${list.tools.length} tools registered:`);
  for (const t of list.tools) {
    console.log(`      - ${t.name}`);
  }

  // 3. Call stellarflow_sentiment
  const topic = LIVE_MODE ? "stellar blockchain" : "tesla";
  console.log(
    `\n[3] tools/call stellarflow_sentiment { topic: ${JSON.stringify(topic)} }${
      LIVE_MODE ? "  ← LIVE MODE: real USDC ($0.05) will be spent" : ""
    }`,
  );
  const result = await send("tools/call", {
    name: "stellarflow_sentiment",
    arguments: {
      topic,
      __reasoning: LIVE_MODE
        ? "live agent payment — real x402 settlement via OZ facilitator (0.05 USDC)"
        : "agent payment check using a local dry-run envelope",
    },
  });
  console.log("    isError:", result.isError ?? false);
  console.log("    content:");
  for (const c of result.content) {
    console.log(c.text.split("\n").map((l) => "      " + l).join("\n"));
  }

  console.log("\n✓ Agent payment completed");
  child.stdin.end();
  process.exit(0);
}

main().catch((err) => {
  console.error("\n✗ Agent payment failed:", err);
  child.kill();
  process.exit(1);
});
