// run.ts — MCP server runtime (the long-running stdio server).
//
// Entry point: exported `runServer()` is called by cli.ts when the user
// invokes `x402-mcp` (no args, default) or `x402-mcp run`.
//
// Wallet loading priority:
//   1. Env vars STELLARFLOW_AGENT_ADDRESS / STELLARFLOW_AGENT_SECRET (manual config,
//      backwards compat with anyone who configured the MCP host before init
//      existed).
//   2. ~/.config/x402-mcp/agent.json (auto-loaded, written by `init`).
//   3. Hardcoded placeholder (dry-run only — backend will replace it).
//
// All other env vars are documented in the MCP host config and in cli.ts
// help text.

import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";

import { Budget, loadBudgetFromEnv } from "./budget.js";
import type { StellarFlowConfig } from "./client.js";
// EXAMPLE: StellarFlow sentiment analysis. Replace this import (and the file
// it points to) with your own MCP tools when forking the template. The
// tools registered here are NOT part of the wire.
import { dispatch, listTools } from "./example_stellarflow_tools.js";
import { loadWallet } from "./wallet-store.js";

const DEFAULT_AGENT_ADDRESS = "GMCPDEMOAGENTXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX";

async function loadConfig(): Promise<StellarFlowConfig> {
  const dryRun = (process.env.STELLARFLOW_DRY_RUN ?? "true").toLowerCase() === "true";

  // Wallet resolution: env vars override file, file overrides placeholder.
  let agentAddress = process.env.STELLARFLOW_AGENT_ADDRESS;
  let agentSecret: string | undefined = process.env.STELLARFLOW_AGENT_SECRET;
  let walletSource: "env" | "file" | "placeholder" = "env";

  if (!agentAddress || !agentSecret) {
    const fileWallet = await loadWallet();
    if (fileWallet) {
      agentAddress = fileWallet.address;
      agentSecret = fileWallet.secret;
      walletSource = "file";
    }
  }

  if (!agentAddress) {
    agentAddress = DEFAULT_AGENT_ADDRESS;
    walletSource = "placeholder";
  }

  // Stash for the startup log line.
  (loadConfig as unknown as { walletSource?: string }).walletSource = walletSource;

  // Soroban RPC URL — required by @x402/stellar to simulate the Soroban
  // auth entry transaction before signing in live mode.
  const rpcUrl =
    process.env.STELLARFLOW_RPC_URL ??
    (process.env.STELLARFLOW_NETWORK === "stellar:testnet"
      ? "https://soroban-testnet.stellar.org"
      : "https://soroban-testnet.stellar.org");

  return {
    baseURL: (process.env.STELLARFLOW_BASE_URL ?? "http://localhost:8086").replace(/\/$/, ""),
    agentAddress,
    network: "stellar:testnet",
    clientID: process.env.STELLARFLOW_CLIENT_ID ?? "claude-code",
    dryRun,
    agentSecret,
    rpcUrl,
  };
}

export async function runServer(): Promise<void> {
  const cfg = await loadConfig();
  const budget = new Budget(loadBudgetFromEnv());
  const walletSource =
    (loadConfig as unknown as { walletSource?: string }).walletSource ?? "unknown";

  // Sanity-check the agent address. Stellar G-addresses are exactly 56 chars.
  if (cfg.agentAddress.length !== 56 || !cfg.agentAddress.startsWith("G")) {
    console.error(
      `[x402-mcp] WARN: agent address is not a valid 56-char Stellar G-address (got length ${cfg.agentAddress.length}). The backend will replace it with a placeholder.`,
    );
  }

  const server = new Server(
    {
      name: "x402-mcp",
      version: "0.1.0",
    },
    {
      capabilities: {
        tools: {},
      },
    },
  );

  // ListTools — return all 5 paid endpoints.
  server.setRequestHandler(ListToolsRequestSchema, async () => ({
    tools: listTools(),
  }));

  // CallTool — dispatch to the paid call helper.
  server.setRequestHandler(CallToolRequestSchema, async (request) => {
    const { name, arguments: rawArgs } = request.params;
    const args = (rawArgs ?? {}) as Record<string, unknown>;

    // The MCP protocol doesn't have a standard "reasoning" field on tool
    // calls, so we look for it on a few common keys the LLM might pass
    // through. The stellarflow backend forwards whatever we send to the live
    // operator feed.
    const reasoning =
      typeof args.__reasoning === "string"
        ? (args.__reasoning as string)
        : typeof args._reasoning === "string"
        ? (args._reasoning as string)
        : undefined;

    if (reasoning) {
      delete args.__reasoning;
      delete args._reasoning;
    }

    return dispatch({ cfg, budget }, name, args, reasoning);
  });

  const transport = new StdioServerTransport();
  await server.connect(transport);

  // Log to stderr so MCP hosts show it in the logs panel without corrupting
  // the stdio JSON-RPC stream.
  console.error(
    `[x402-mcp] connected · backend=${cfg.baseURL} · network=${cfg.network} · dry_run=${cfg.dryRun} · wallet=${cfg.agentAddress.slice(0, 4)}…${cfg.agentAddress.slice(-4)} (${walletSource}) · budget per_call=$${budget.cfg.perCall} per_session=$${budget.cfg.perSession} per_day=$${budget.cfg.perDay}`,
  );
}
