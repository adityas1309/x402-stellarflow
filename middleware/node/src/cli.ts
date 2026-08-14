#!/usr/bin/env node
/**
 * Interactive setup CLI for @stellarflow/x402-middleware.
 *
 * Walks the developer through:
 *   1. Generating or importing a Stellar treasury wallet
 *   2. Generating a SEP-10 server signing keypair
 *   3. Generating a JWT secret for wallet sessions
 *   4. Configuring the OZ facilitator API key
 *   5. Saving everything to .env (and adding to .gitignore)
 *   6. Printing a ready-to-paste code snippet
 *
 * Zero dependencies beyond what the library already uses.
 * Uses Node's built-in readline — no inquirer, no chalk, no ora.
 * (Keeps install size tiny for a library that gets installed by every consumer.)
 */

import * as readline from "node:readline/promises";
import { stdin as input, stdout as output } from "node:process";
import { Keypair } from "@stellar/stellar-sdk";
import { randomBytes } from "node:crypto";
import { existsSync, readFileSync, writeFileSync, appendFileSync } from "node:fs";
import { join } from "node:path";

// ─── Color helpers (no chalk dep) ──────────────────────────────

const c = {
  reset: "\x1b[0m",
  bold: "\x1b[1m",
  dim: "\x1b[2m",
  cyan: "\x1b[36m",
  green: "\x1b[32m",
  yellow: "\x1b[33m",
  red: "\x1b[31m",
  blue: "\x1b[34m",
};

const header = (text: string) => {
  console.log();
  console.log(c.cyan + "═".repeat(68) + c.reset);
  console.log(c.cyan + "  " + c.bold + text + c.reset);
  console.log(c.cyan + "═".repeat(68) + c.reset);
  console.log();
};

const step = (n: number, text: string) =>
  console.log(c.blue + `[${n}/5]` + c.reset + " " + c.bold + text + c.reset);

const info = (text: string) => console.log(c.dim + "  " + text + c.reset);
const ok = (text: string) => console.log(c.green + "  ✓" + c.reset + " " + text);
const warn = (text: string) => console.log(c.yellow + "  ⚠" + c.reset + " " + text);

// ─── Main ──────────────────────────────────────────────────────

async function main() {
  const cmd = process.argv[2] ?? "init";

  if (cmd === "help" || cmd === "--help" || cmd === "-h") {
    printUsage();
    return;
  }

  if (cmd !== "init") {
    console.error(`Unknown command: ${cmd}`);
    printUsage();
    process.exit(2);
  }

  const rl = readline.createInterface({ input, output });
  const ask = async (q: string, def?: string): Promise<string> => {
    const prompt = def ? `${q} ${c.dim}(${def})${c.reset} ` : `${q} `;
    const ans = (await rl.question(prompt)).trim();
    return ans || def || "";
  };
  const askYN = async (q: string, defYes = true): Promise<boolean> => {
    const def = defYes ? "Y/n" : "y/N";
    const ans = (await rl.question(`${q} ${c.dim}[${def}]${c.reset} `)).trim().toLowerCase();
    if (!ans) return defYes;
    return ans.startsWith("y");
  };

  try {
    header("x402-middleware — interactive setup");
    console.log("This CLI will configure your Stellar-native API paywall in under 2 minutes.");
    console.log("It sets up TWO things:");
    console.log(`  ${c.cyan}•${c.reset} ${c.bold}x402 paywall${c.reset} — agents pay USDC per call`);
    console.log(`  ${c.cyan}•${c.reset} ${c.bold}SEP-10 wallet login${c.reset} — operators sign in with their Stellar wallet`);
    console.log();

    // ── Step 1: Treasury wallet ──────────────────────────────
    step(1, "Treasury wallet (where you receive USDC payments)");
    const hasTreasury = await askYN("Do you already have a Stellar treasury wallet?", false);

    let treasuryAddress = "";
    let treasurySecret = "";
    if (hasTreasury) {
      treasuryAddress = await ask("  Treasury G-address:");
      if (!treasuryAddress.startsWith("G") || treasuryAddress.length !== 56) {
        warn("That doesn't look like a valid Stellar G-address. Continuing anyway.");
      }
      const saveSecret = await askYN("  Save the S-key to .env for settlement?", true);
      if (saveSecret) {
        treasurySecret = await ask("  Treasury S-key:");
      }
      ok(`Using treasury: ${shortAddr(treasuryAddress)}`);
    } else {
      const kp = Keypair.random();
      treasuryAddress = kp.publicKey();
      treasurySecret = kp.secret();
      ok(`Generated treasury wallet: ${shortAddr(treasuryAddress)}`);
      info("Full address and S-key will be saved to .env");
      console.log();
      warn("IMPORTANT: fund this wallet with ~5 XLM + USDC reserve before going live.");
      warn(`  1. Buy XLM on a CEX (Kraken, Coinbase, Binance) or use an existing wallet`);
      warn(`  2. Send to: ${c.bold}${treasuryAddress}${c.reset}`);
      warn(`  3. Also send some USDC (Circle) via Stellar to the same address`);
    }
    console.log();

    // ── Step 2: SEP-10 server signing keypair ────────────────
    step(2, "SEP-10 server signing keypair (for wallet sign-in)");
    info("This is a SEPARATE keypair used only to sign SEP-10 challenge");
    info("transactions. It never holds funds. Generated automatically.");
    const sep10KP = Keypair.random();
    ok(`Generated SEP-10 signer: ${shortAddr(sep10KP.publicKey())}`);
    console.log();

    // ── Step 3: JWT secret ────────────────────────────────────
    step(3, "JWT secret (for wallet session tokens)");
    const jwtSecret = randomBytes(32).toString("hex");
    ok(`Generated 256-bit JWT secret`);
    console.log();

    // ── Step 4: OZ facilitator API key ────────────────────────
    step(4, "OpenZeppelin Built-on-Stellar facilitator");
    info("The facilitator verifies and settles x402 payments on-chain.");
    info("It's free and takes one curl command to set up.");
    console.log();
    const getFacKey = await askYN("  Fetch a facilitator API key now?", true);

    let facilitatorKey = "";
    if (getFacKey) {
      info("Calling https://channels.openzeppelin.com/gen ...");
      try {
        // OZ's /gen endpoint responds to GET (returns 201 with a fresh
        // apiKey). POST used to work but started returning 401 as of
        // 2026-04. Using GET is the stable path.
        const resp = await fetch("https://channels.openzeppelin.com/gen", { method: "GET" });
        if (resp.ok) {
          const data = (await resp.json()) as { apiKey?: string };
          facilitatorKey = data.apiKey ?? "";
          if (facilitatorKey) {
            ok(`Got facilitator API key: ${facilitatorKey.slice(0, 8)}...${facilitatorKey.slice(-4)}`);
          } else {
            warn("Response didn't contain apiKey. Get one manually.");
          }
        } else {
          warn(`Facilitator gen returned ${resp.status}. Get a key manually.`);
        }
      } catch (e) {
        warn(`Failed to auto-fetch: ${(e as Error).message}`);
      }
    }

    if (!facilitatorKey) {
      info("You can get one manually with:");
      info(`  ${c.cyan}curl https://channels.openzeppelin.com/gen${c.reset}`);
      const manualKey = await ask("  Paste the API key here (or ENTER to skip):");
      // Defensive validation: if the user types 'n', 'y', 'no', 'skip', etc.
      // thinking they can answer yes/no here, treat it as empty. A real OZ
      // facilitator key looks like a UUID (8-4-4-4-12 hex chars) or at
      // minimum is longer than 8 characters.
      const looksValid =
        manualKey.length >= 20 ||
        /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(manualKey);
      if (manualKey && !looksValid) {
        warn(`That doesn't look like a facilitator key (got "${manualKey}"). Skipping.`);
        warn("If you meant to paste a real key, re-run `npx x402-init` and paste a UUID.");
      } else if (manualKey) {
        facilitatorKey = manualKey;
      }
    }

    if (!facilitatorKey) {
      warn("No facilitator key — you'll need to run in dry-run mode until you add one");
    }
    console.log();

    // ── Step 5: Write .env ───────────────────────────────────
    step(5, "Save configuration");
    const envPath = await ask("  Path to .env file:", ".env");
    const fullEnvPath = join(process.cwd(), envPath);

    const envContents = buildEnvContents({
      treasuryAddress,
      treasurySecret,
      sep10SignerSecret: sep10KP.secret(),
      sep10SignerAddress: sep10KP.publicKey(),
      jwtSecret,
      facilitatorKey,
      network: "stellar:testnet",
      dryRun: facilitatorKey ? "false" : "true",
    });

    if (existsSync(fullEnvPath)) {
      const overwrite = await askYN(`  .env already exists at ${fullEnvPath}. Append to it?`, true);
      if (overwrite) {
        appendFileSync(fullEnvPath, "\n\n" + envContents);
        ok(`Appended to ${envPath}`);
      } else {
        const altPath = fullEnvPath + ".x402";
        writeFileSync(altPath, envContents);
        ok(`Saved to ${envPath}.x402 (copy the values into your .env manually)`);
      }
    } else {
      writeFileSync(fullEnvPath, envContents);
      ok(`Saved to ${envPath}`);
    }

    // Add to .gitignore
    const gitignorePath = join(process.cwd(), ".gitignore");
    if (existsSync(gitignorePath)) {
      const gi = readFileSync(gitignorePath, "utf-8");
      if (!gi.includes(".env")) {
        appendFileSync(gitignorePath, "\n.env\n");
        ok("Added .env to .gitignore");
      } else {
        info(".gitignore already excludes .env");
      }
    }

    rl.close();

    // ── Next steps ────────────────────────────────────────────
    printNextSteps(treasuryAddress, sep10KP.publicKey(), !!facilitatorKey);
  } catch (err) {
    rl.close();
    console.error(c.red + "Error: " + (err as Error).message + c.reset);
    process.exit(1);
  }
}

// ─── Helpers ───────────────────────────────────────────────────

function shortAddr(addr: string): string {
  if (addr.length < 12) return addr;
  return `${addr.slice(0, 6)}…${addr.slice(-6)}`;
}

function buildEnvContents(cfg: {
  treasuryAddress: string;
  treasurySecret: string;
  sep10SignerSecret: string;
  sep10SignerAddress: string;
  jwtSecret: string;
  facilitatorKey: string;
  network: string;
  dryRun: string;
}): string {
  return `# x402-middleware configuration (generated by x402-init)
# ─────────────────────────────────────────────────────────

# Treasury wallet — receives USDC payments from agents
X402_TREASURY_ADDRESS=${cfg.treasuryAddress}
${cfg.treasurySecret ? `X402_TREASURY_SECRET=${cfg.treasurySecret}` : "# X402_TREASURY_SECRET=  (not saved)"}

# SEP-10 server signing keypair — signs wallet login challenges
SEP10_SERVER_SIGNING_KEY=${cfg.sep10SignerSecret}
SEP10_SERVER_ADDRESS=${cfg.sep10SignerAddress}
SEP10_HOME_DOMAIN=localhost

# JWT secret — signs wallet session tokens (256-bit)
X402_JWT_SECRET=${cfg.jwtSecret}

# OpenZeppelin Built-on-Stellar facilitator
X402_FACILITATOR_URL=https://channels.openzeppelin.com/x402/testnet
${cfg.facilitatorKey ? `X402_FACILITATOR_KEY=${cfg.facilitatorKey}` : "# X402_FACILITATOR_KEY=  (get one: curl https://channels.openzeppelin.com/gen)"}

# Network configuration
X402_NETWORK=${cfg.network}
X402_DRY_RUN=${cfg.dryRun}
`;
}

function printNextSteps(treasury: string, sep10Signer: string, hasFacKey: boolean): void {
  header("Setup complete. Next steps.");

  console.log("  " + c.bold + "1. Add the paywall to your app:" + c.reset);
  console.log();
  console.log(c.dim + "     // Express" + c.reset);
  console.log(`     ${c.blue}import${c.reset} express ${c.blue}from${c.reset} ${c.green}"express"${c.reset};`);
  console.log(`     ${c.blue}import${c.reset} { x402, sep10Auth } ${c.blue}from${c.reset} ${c.green}"@stellarflow/x402-middleware"${c.reset};`);
  console.log();
  console.log(`     ${c.blue}const${c.reset} app = ${c.cyan}express${c.reset}();`);
  console.log(`     app.${c.cyan}use${c.reset}(express.${c.cyan}json${c.reset}());`);
  console.log();
  console.log(c.dim + "     // SEP-10 auto-registers /api/auth/challenge + /api/auth/token" + c.reset);
  console.log(`     app.${c.cyan}use${c.reset}(${c.cyan}sep10Auth${c.reset}.${c.cyan}routes${c.reset}());`);
  console.log();
  console.log(c.dim + "     // Paid endpoint — agents pay USDC per call" + c.reset);
  console.log(`     app.${c.cyan}post${c.reset}(${c.green}"/api/sentiment"${c.reset},`);
  console.log(`       ${c.cyan}x402${c.reset}({ price: ${c.yellow}0.05${c.reset} }),`);
  console.log(`       (req, res) => res.${c.cyan}json${c.reset}({ score: ${c.yellow}0.82${c.reset} }));`);
  console.log();
  console.log(c.dim + "     // Operator dashboard — SEP-10 wallet sign-in" + c.reset);
  console.log(`     app.${c.cyan}get${c.reset}(${c.green}"/operator/stats"${c.reset},`);
  console.log(`       ${c.cyan}sep10Auth${c.reset}.${c.cyan}protect${c.reset}({ allowedWallets: [${c.green}"${treasury.slice(0, 8)}..."${c.reset}] }),`);
  console.log(`       (req, res) => res.${c.cyan}json${c.reset}({ wallet: req.wallet }));`);
  console.log();

  console.log("  " + c.bold + "2. Start your server:" + c.reset);
  console.log(`     ${c.cyan}node${c.reset} index.js`);
  console.log();

  console.log("  " + c.bold + "3. Test it:" + c.reset);
  console.log(`     ${c.cyan}curl${c.reset} -X POST http://localhost:3000/api/sentiment`);
  console.log(`     ${c.dim}→ 402 Payment Required (expected, no payment header)${c.reset}`);
  console.log();

  if (!hasFacKey) {
    warn("You're in dry-run mode. Get a facilitator key to go live:");
    warn(`  curl https://channels.openzeppelin.com/gen`);
    warn("Then set X402_FACILITATOR_KEY in .env and X402_DRY_RUN=false");
    console.log();
  }

  console.log("  " + c.dim + "Treasury G-address:  " + treasury + c.reset);
  console.log("  " + c.dim + "SEP-10 signer:       " + sep10Signer + c.reset);
  console.log();
  console.log(c.green + "  Happy building. May your USDC flow freely." + c.reset);
  console.log();
}

function printUsage(): void {
  console.log(`Usage: x402-init [command]

Commands:
  init       Interactive setup (default). Generates wallets, JWT secret,
             fetches a facilitator API key, and writes .env.
  help       Show this message.

Run from your project root (where you want .env to live).
`);
}

main().catch((err) => {
  console.error("[x402-init] fatal:", err);
  process.exit(1);
});
