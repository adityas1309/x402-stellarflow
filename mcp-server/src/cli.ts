#!/usr/bin/env node
// cli.ts — entry point for the x402-mcp binary.
//
// Sub-commands:
//
//   x402-mcp           run the MCP server (default, for backward compat
//                          with existing MCP host configs that just spawn
//                          the binary with no args)
//   x402-mcp run       same as above, explicit
//   x402-mcp init      provision a fresh burner wallet. Default: self-fund
//                          (you send 2 XLM to activate). With --demo: the
//                          operator's treasury sponsors the activation.
//   x402-mcp help      print usage
//
// Each sub-command lives in its own module and is dynamically imported so
// the MCP server's startup time stays fast (init pulls in @stellar/stellar-sdk
// which is ~1MB of code we don't want to load on every `run`).

async function main(): Promise<void> {
  const cmd = process.argv[2] ?? "run";

  switch (cmd) {
    case "run":
    case undefined:
    case "": {
      const { runServer } = await import("./run.js");
      await runServer();
      return;
    }

    case "init": {
      const { runInit } = await import("./init.js");
      const args = process.argv.slice(3);
      const force = args.includes("--force");
      const demo = args.includes("--demo");
      await runInit({ force, demo });
      return;
    }

    case "help":
    case "--help":
    case "-h": {
      printUsage();
      return;
    }

    default:
      console.error(`unknown command: ${cmd}`);
      printUsage();
      process.exit(2);
  }
}

function printUsage(): void {
  console.log(`Usage: x402-mcp [command]

Commands:
  run                     Start the MCP server over stdio (default).
  init [--force] [--demo] Provision a fresh burner wallet for paying x402 calls.
                          Refuses to overwrite an existing wallet at
                          ~/.config/x402-mcp/agent.json unless --force is given.

                          Default: self-fund mode. The CLI generates a wallet and
                          waits for YOU to send >= 2 XLM to activate it. You keep
                          full control of funding — no operator treasury involved.

                          --demo: the operator's backend sponsors the wallet with
                          2 XLM from their treasury. Convenient for hackathon demos
                          and evaluators, but NOT recommended for production (the
                          operator bears the cost and the treasury is a hot wallet).

  help                    Show this message.

Environment variables (for run):
  STELLARFLOW_BASE_URL          Backend URL (default http://localhost:8086)
  STELLARFLOW_DRY_RUN           "true" to use stub payments (default true)
  STELLARFLOW_NETWORK           "stellar:testnet"
  STELLARFLOW_RPC_URL           Soroban RPC URL — required when DRY_RUN=false
                             (default https://soroban-testnet.stellar.org for testnet,
                             https://soroban-testnet.stellar.org for testnet)
  BUDGET_USDC_PER_CALL       Per-call cap in USDC (default 0.20)
  BUDGET_USDC_PER_SESSION    Per-process cap in USDC (default 1.00)
  BUDGET_USDC_PER_DAY        Per-day cap in USDC (default 5.00)

  When ~/.config/x402-mcp/agent.json exists (after \`init\`), the wallet
  is loaded from there. Otherwise the env vars STELLARFLOW_AGENT_ADDRESS /
  STELLARFLOW_AGENT_SECRET are used.

Environment variables (for init):
  STELLARFLOW_BASE_URL          Backend URL where the operator backend lives
                             (default http://localhost:8086)
  STELLARFLOW_FRONTEND_URL      Frontend URL where the dashboard /wallet/:address
                             page is served. The handoff link printed at the
                             end of init points here.
                             (default http://localhost:5173)
  STELLARFLOW_NETWORK           "stellar:testnet"
  STELLARFLOW_USDC_ISSUER       Override the USDC classic issuer (rare)
  STELLARFLOW_HORIZON_URL       Override the Horizon endpoint
`);
}

main().catch((err) => {
  console.error("[x402-mcp] fatal:", err);
  process.exit(1);
});
