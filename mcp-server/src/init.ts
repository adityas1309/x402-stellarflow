// init.ts — `x402-mcp init` command implementation.
//
// Provisions a fresh burner wallet for the agent:
//
//   DEFAULT (self-fund mode):
//     1. Generate a Stellar keypair locally.
//     2. Print the G-address and wait for the user to send >= 2 XLM.
//     3. Once funded: sign + submit a ChangeTrust(USDC) transaction.
//     4. Sign in via SEP-10, persist wallet + session.
//
//   --demo (operator-sponsored mode, for hackathon demos / evaluators):
//     1. Generate a Stellar keypair locally.
//     2. POST /api/agent/sponsor to the operator backend (operator pays 2 XLM).
//     3. Sign + submit ChangeTrust(USDC).
//     4. Sign in via SEP-10, persist wallet + session.
//
// The --demo mode is NOT recommended for production: the operator's treasury
// is a hot wallet and the sponsor endpoint is an attack surface. In production,
// agents bring their own XLM via self-fund (the default).

import { Keypair, TransactionBuilder, Networks, Operation, Asset, BASE_FEE, Horizon, Transaction } from "@stellar/stellar-sdk";
import * as bip39 from "bip39";
import { derivePath } from "ed25519-hd-key";

import { saveWallet, loadWallet, updateWalletSession, walletFilePath, type WalletFile } from "./wallet-store.js";

/**
 * SEP-0005 derivation path for the primary Stellar account.
 * https://github.com/stellar/stellar-protocol/blob/master/ecosystem/sep-0005.md
 *
 * `148` is the Stellar coin type in SLIP-44.
 * `0` is the first account index — multi-account users would use 1, 2, ...
 *
 * Freighter uses this same path when it imports a recovery phrase, so a
 * wallet generated here can be re-imported into Freighter by pasting the
 * 24-word mnemonic into "Add account → Import recovery phrase", and
 * Freighter will derive the exact same G-address.
 */
const STELLAR_DERIVATION_PATH = "m/44'/148'/0'";

/**
 * Generates a fresh BIP39 24-word mnemonic and derives the primary Stellar
 * account from it via SEP-0005. Returns the mnemonic, the derivation path
 * used, and a Keypair the caller can use for signing.
 *
 * 24 words (256 bits of entropy) is the stronger BIP39 variant. Freighter
 * supports both 12 and 24 word phrases, so this is safe for import.
 */
function generateMnemonicKeypair(): { mnemonic: string; path: string; kp: Keypair } {
  const mnemonic = bip39.generateMnemonic(256); // 24 words
  const seed = bip39.mnemonicToSeedSync(mnemonic);
  const { key } = derivePath(STELLAR_DERIVATION_PATH, seed.toString("hex"));
  const kp = Keypair.fromRawEd25519Seed(key);
  return { mnemonic, path: STELLAR_DERIVATION_PATH, kp };
}

interface InitConfig {
  baseURL: string;          // e.g. http://localhost:8086 — the operator's backend (sponsor + SEP-10 endpoints live here)
  frontendURL: string;      // e.g. http://localhost:5173 — where the operator dashboard frontend is served (the /wallet/:address page)
  network: "stellar:testnet";
  usdcIssuer: string;       // classic G-issuer for USDC trustline
  horizonURL: string;       // Horizon endpoint matching the network
  dryRun: boolean;          // when true, skip on-chain operations (sponsor/trustline)
                            // and just generate the keypair + sign SEP-10 + handoff
}

/**
 * Loads init config from environment variables, with sensible testnet defaults.
 *
 * STELLARFLOW_BASE_URL and STELLARFLOW_FRONTEND_URL are SEPARATE because in dev
 * the backend (Go) and the frontend (Vite) run on different ports. Only the
 * frontend serves the /wallet/:address handoff page.
 *
 * STELLARFLOW_DRY_RUN=true (default) means: generate the keypair locally and
 * sign in via SEP-10, but DO NOT touch the Stellar network. The wallet
 * exists only in ~/.config/x402-mcp/agent.json and never gets activated
 * on chain. Subsequent paid calls also use stub payments. This is the
 * default for local development so the user can clone the template and
 * test the full flow without spending any XLM or needing a treasury wallet.
 */
export function loadInitConfig(): InitConfig {
  const configuredNetwork = process.env.STELLARFLOW_NETWORK;
  if (configuredNetwork && configuredNetwork !== "stellar:testnet") {
    throw new Error("Only Stellar testnet is supported. Set STELLARFLOW_NETWORK=stellar:testnet.");
  }
  const network = "stellar:testnet" as const;
  const dryRun = (process.env.STELLARFLOW_DRY_RUN ?? "true").toLowerCase() === "true";
  return {
    baseURL: (process.env.STELLARFLOW_BASE_URL ?? "http://localhost:8086").replace(/\/$/, ""),
    frontendURL: (process.env.STELLARFLOW_FRONTEND_URL ?? "http://localhost:5173").replace(/\/$/, ""),
    network,
    usdcIssuer:
      process.env.STELLARFLOW_USDC_ISSUER ??
      "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5",
    horizonURL:
      process.env.STELLARFLOW_HORIZON_URL ??
      (network === "stellar:testnet"
        ? "https://horizon-testnet.stellar.org"
        : "https://horizon.stellar.org"),
    dryRun,
  };
}

/**
 * Maps the env-style network identifier to the SDK's Networks passphrase.
 */
function networkPassphrase(network: string): string {
  return Networks.TESTNET;
}

/**
 * Calls the operator's /api/agent/sponsor endpoint to fund the new account
 * with 2 XLM. Idempotent: if the account already exists, returns the
 * already_existed flag without spending more XLM.
 */
async function sponsorAgent(
  cfg: InitConfig,
  address: string,
): Promise<{ tx_hash: string; already_existed: boolean }> {
  const url = `${cfg.baseURL}/api/agent/sponsor`;
  const resp = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ address }),
  });
  const text = await resp.text();
  if (!resp.ok) {
    throw new Error(`sponsor request failed (${resp.status}): ${text}`);
  }
  let data: { tx_hash?: string; already_existed?: boolean; error?: string };
  try {
    data = JSON.parse(text);
  } catch {
    throw new Error(`sponsor returned non-JSON: ${text}`);
  }
  if (data.error) {
    throw new Error(`sponsor error: ${data.error}`);
  }
  return {
    tx_hash: data.tx_hash ?? "",
    already_existed: Boolean(data.already_existed),
  };
}

/**
 * Builds and submits a ChangeTrust(USDC) transaction signed by the freshly
 * created agent account. The agent pays the small XLM fee out of its 2 XLM
 * starting balance (the trustline reserve is 0.5 XLM, plus a tiny tx fee).
 */
async function establishUSDCTrustline(
  cfg: InitConfig,
  agentKP: Keypair,
): Promise<string> {
  const server = new Horizon.Server(cfg.horizonURL);
  const account = await server.loadAccount(agentKP.publicKey());

  const usdcAsset = new Asset("USDC", cfg.usdcIssuer);

  const tx = new TransactionBuilder(account, {
    fee: BASE_FEE,
    networkPassphrase: networkPassphrase(cfg.network),
  })
    .addOperation(
      Operation.changeTrust({
        asset: usdcAsset,
      }),
    )
    .setTimeout(120)
    .build();

  tx.sign(agentKP);

  const result = await server.submitTransaction(tx);
  return result.hash;
}

/**
 * Performs a full SEP-10 sign-in dance against the operator backend using
 * the freshly generated burner S-key. This is the headless equivalent of
 * the Freighter-based sign-in the operator does in the browser:
 *
 *   1. GET /api/auth/challenge?account=G... → unsigned challenge XDR
 *   2. Sign the XDR with the burner S-key (programmatically, no UI)
 *   3. POST /api/auth/token { id, transaction } → Paseto access + refresh
 *      tokens plus the user, org and wallet payload
 *
 * The returned `Sep10Session` is the same shape the frontend's authStore
 * expects from a successful login. We base64-encode it and put it into
 * the dashboard URL as `?session=...` so the frontend can adopt it on
 * page load without the user typing or pasting anything.
 */
interface Sep10ChallengeResponse {
  id: string;
  transaction: string;
  network_passphrase: string;
  home_domain: string;
  expires_at: string;
}

interface Sep10TokenResponse {
  access_token: string;
  refresh_token: string;
  user: Record<string, unknown>;
  org: Record<string, unknown>;
  wallet: { address: string; network: string };
}

async function signInSEP10(
  cfg: InitConfig,
  agentKP: Keypair,
): Promise<Sep10TokenResponse> {
  // 1. Get the unsigned challenge.
  const challengeURL = `${cfg.baseURL}/api/auth/challenge?account=${encodeURIComponent(agentKP.publicKey())}`;
  const challengeResp = await fetch(challengeURL);
  if (!challengeResp.ok) {
    const text = await challengeResp.text();
    throw new Error(`SEP-10 challenge request failed (${challengeResp.status}): ${text}`);
  }
  const challenge = (await challengeResp.json()) as Sep10ChallengeResponse;

  // 2. Sign the XDR locally with the burner S-key.
  // The challenge tx uses the network passphrase the BACKEND set, which is
  // authoritative — we honor it instead of guessing from cfg.network so the
  // signature matches what the backend will verify.
  const tx = new Transaction(challenge.transaction, challenge.network_passphrase);
  tx.sign(agentKP);
  const signedXDR = tx.toXDR();

  // 3. Exchange the signed challenge for a Paseto session.
  const tokenURL = `${cfg.baseURL}/api/auth/token`;
  const tokenResp = await fetch(tokenURL, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id: challenge.id, transaction: signedXDR }),
  });
  if (!tokenResp.ok) {
    const text = await tokenResp.text();
    throw new Error(`SEP-10 token exchange failed (${tokenResp.status}): ${text}`);
  }
  const session = (await tokenResp.json()) as Sep10TokenResponse;
  return session;
}

/**
 * Encodes the SEP-10 session into a URL-safe base64 string suitable for
 * embedding as a query parameter. The frontend's handoff hook decodes it
 * with the inverse `decodeURIComponent + atob` dance.
 */
function encodeSession(session: Sep10TokenResponse): string {
  const json = JSON.stringify(session);
  // Node's Buffer base64 is the standard variant; the frontend uses atob
  // which accepts standard base64 (= padding allowed).
  return Buffer.from(json, "utf-8").toString("base64");
}

/**
 * Polls Horizon until the given account is visible (i.e. the sponsor's
 * CreateAccount tx has been confirmed and propagated). Bounded retry to
 * avoid hanging forever if Horizon is misbehaving.
 */
async function waitForAccount(
  cfg: InitConfig,
  address: string,
  maxAttempts = 10,
  delayMs = 1500,
): Promise<void> {
  const server = new Horizon.Server(cfg.horizonURL);
  for (let i = 0; i < maxAttempts; i++) {
    try {
      await server.loadAccount(address);
      return;
    } catch (err) {
      // 404 from Horizon means "not yet propagated". Anything else is a
      // genuine error and we surface it.
      const isNotFound =
        (err as { response?: { status?: number } })?.response?.status === 404 ||
        (err as Error).message?.includes("Not Found");
      if (!isNotFound) {
        throw err;
      }
      await new Promise((r) => setTimeout(r, delayMs));
    }
  }
  throw new Error(
    `account ${address} did not propagate to Horizon within ${(maxAttempts * delayMs) / 1000}s`,
  );
}

const HEADER = "════════════════════════════════════════════════════════════════";

function logHeader(text: string): void {
  console.log(HEADER);
  console.log(`  ${text}`);
  console.log(HEADER);
  console.log();
}

/**
 * Run the full init flow. Returns the WalletFile that was persisted, so
 * the caller (cli.ts) can either return success or chain into Fase 2.5
 * (SEP-10 handoff) which adds a session to the saved wallet.
 *
 * Refuses to run if a wallet file already exists at the target path,
 * unless `force` is true. This prevents accidentally overwriting (and
 * losing access to) an already-funded burner wallet.
 *
 * `demo`: if true, use the operator-sponsored flow (POST /api/agent/sponsor).
 * Default (false): self-fund — generate the keypair, print the address,
 * and wait for the user to fund it with >= 2 XLM before proceeding.
 */
export async function runInit(opts: { force?: boolean; demo?: boolean } = {}): Promise<WalletFile> {
  const cfg = loadInitConfig();
  const isDemo = opts.demo === true;

  // Safety: refuse to overwrite an existing wallet unless --force.
  const existing = await loadWallet();
  if (existing && !opts.force) {
    logHeader("x402-mcp — wallet already exists");
    console.log(`A burner wallet is already saved at:`);
    console.log(`   ${walletFilePath()}`);
    console.log();
    console.log(`Address:  ${existing.address}`);
    console.log(`Network:  ${existing.network}`);
    console.log(`Created:  ${existing.created_at}`);
    console.log();
    console.log(`To regenerate (this will burn the existing wallet's S-key forever`);
    console.log(`and any USDC funds left on it become unrecoverable), re-run with:`);
    console.log();
    console.log(`   x402-mcp init --force`);
    console.log();
    throw new Error("wallet already exists, refusing to overwrite without --force");
  }

  const modeLabel = cfg.dryRun
    ? "DRY-RUN, no on-chain operations"
    : isDemo
    ? "demo mode — operator sponsors the wallet"
    : "self-fund mode — you provide the XLM";

  logHeader(`x402-mcp — initialise burner wallet (${modeLabel})`);

  // Step 1 — generate keypair locally from a fresh BIP39 mnemonic.
  // Using SEP-0005 derivation (path m/44'/148'/0') so the wallet can be
  // re-imported into Freighter via "Import recovery phrase" — Freighter
  // derives the exact same G-address from the same 24-word phrase.
  const { mnemonic, path: derivationPath, kp: agentKP } = generateMnemonicKeypair();
  const address = agentKP.publicKey();
  console.log(`🔑 Generated burner wallet (BIP39 + SEP-0005)`);
  console.log(`   Public:  ${address}`);
  console.log();

  if (cfg.dryRun) {
    console.log(`⏭  DRY-RUN: skipping account activation + trustline (no XLM spent)`);
    console.log(`   The wallet exists only locally and will be used to sign stub`);
    console.log(`   payment payloads against the dry-run backend.`);
    console.log();
    console.log(`   To go live on testnet, set a funded wallet and a USDC trustline:`);
    console.log(`     export STELLARFLOW_DRY_RUN=false`);
    console.log(`     x402-mcp init --force`);
    console.log();
  } else if (isDemo) {
    // --demo: operator-sponsored flow (POST /api/agent/sponsor).
    // The operator's treasury pays ~2 XLM to activate the new account.
    // Convenient for hackathon demos but NOT production — the treasury
    // is a hot wallet and the endpoint is an attack surface.
    console.log(`⏳ Sponsoring with operator treasury at ${cfg.baseURL} ...`);
    console.log(`   (--demo mode: the operator pays ~2 XLM to activate your wallet)`);
    const sponsorResult = await sponsorAgent(cfg, address);
    if (sponsorResult.already_existed) {
      console.log(`   ✓ Account already exists on-chain (no XLM spent)`);
    } else {
      console.log(`   ✓ Sponsored. tx: ${sponsorResult.tx_hash}`);
    }
    console.log();

    // Wait for Horizon to propagate the new account.
    if (!sponsorResult.already_existed) {
      console.log(`⏳ Waiting for Horizon to propagate the new account ...`);
      await waitForAccount(cfg, address);
      console.log(`   ✓ Account visible`);
      console.log();
    }

    // Establish USDC trustline.
    console.log(`⏳ Establishing USDC trustline ...`);
    try {
      const trustHash = await establishUSDCTrustline(cfg, agentKP);
      console.log(`   ✓ Trustline established. tx: ${trustHash}`);
    } catch (err) {
      const msg = (err as Error).message;
      if (msg.includes("op_already_exists") || msg.includes("trust_line_full")) {
        console.log(`   ⚠ Trustline already exists, continuing`);
      } else {
        throw new Error(`failed to establish trustline: ${msg}`);
      }
    }
    console.log();
  } else {
    // DEFAULT: self-fund flow.
    // The user sends >= 2 XLM to the generated address from any source
    // (CEX withdrawal, Freighter, Lobstr, another wallet, etc).
    // We poll Horizon until the account appears, then proceed.
    console.log(`💰 Self-fund: send at least 2 XLM to activate this wallet.`);
    console.log();
    console.log(`   Address:  ${address}`);
    console.log(`   Network:  Stellar TESTNET`);
    console.log();
    console.log(`   You can send from any Stellar wallet: Freighter, Lobstr, a CEX,`);
    console.log(`   or another CLI. The minimum for account activation is ~2 XLM.`);
    if (cfg.network === "stellar:testnet") {
      console.log();
      console.log(`   Testnet faucet: https://friendbot.stellar.org?addr=${address}`);
    }
    console.log();
    console.log(`⏳ Waiting for account to appear on Horizon ...`);
    console.log(`   (polling every 3 seconds, will wait up to 10 minutes)`);
    console.log();

    // Poll with longer intervals and more attempts for self-fund —
    // the user needs time to open their wallet app and send XLM.
    await waitForAccount(cfg, address, /* maxAttempts */ 200, /* delayMs */ 3000);
    console.log(`   ✓ Account funded and visible on Horizon`);
    console.log();

    // Establish USDC trustline.
    console.log(`⏳ Establishing USDC trustline ...`);
    try {
      const trustHash = await establishUSDCTrustline(cfg, agentKP);
      console.log(`   ✓ Trustline established. tx: ${trustHash}`);
    } catch (err) {
      const msg = (err as Error).message;
      if (msg.includes("op_already_exists") || msg.includes("trust_line_full")) {
        console.log(`   ⚠ Trustline already exists, continuing`);
      } else {
        throw new Error(`failed to establish trustline: ${msg}`);
      }
    }
    console.log();
  }

  // Step 4 — persist wallet locally.
  const wallet: WalletFile = {
    version: 2,
    address,
    secret: agentKP.secret(),
    mnemonic,
    derivation_path: derivationPath,
    network: cfg.network,
    created_at: new Date().toISOString(),
  };
  const path = await saveWallet(wallet);
  console.log(`🔐 Saved to ${path} (file mode 0600)`);
  console.log();

  // Step 5 — SEP-10 sign-in to obtain a Paseto session for the agent
  // dashboard. Headless: signs the challenge with the burner S-key
  // we just generated, no Freighter / no UI / no human input.
  console.log(`⏳ Signing in via SEP-10 ...`);
  let session: Sep10TokenResponse | null = null;
  try {
    session = await signInSEP10(cfg, agentKP);
    await updateWalletSession({
      access_token: session.access_token,
      refresh_token: session.refresh_token,
    });
    console.log(`   ✓ Signed in. Paseto token persisted to wallet file`);
  } catch (err) {
    // Don't fail the whole init if SEP-10 sign-in fails — the wallet is
    // still usable for paying x402 calls. The user just won't get a
    // dashboard handoff URL. Surface the error clearly so they know.
    console.log(`   ⚠ SEP-10 sign-in failed: ${(err as Error).message}`);
    console.log(`   ⚠ The wallet is still usable for paid calls, but you won't`);
    console.log(`     have an automatic dashboard session. You can sign in later`);
    console.log(`     by running x402-mcp refresh-session.`);
  }
  console.log();

  logHeader("Next steps");

  let stepCounter = 1;
  if (!cfg.dryRun) {
    console.log(`${stepCounter}. Fund this wallet with USDC to pay for API calls.`);
    console.log(`   Send any USDC amount on Stellar testnet to:`);
    console.log(`     ${address}`);
    console.log();
    if (isDemo) {
      console.log("   The operator covered your account activation (--demo mode).");
      console.log("   You provide the USDC for actual API calls.");
    } else {
      console.log("   You funded the account activation yourself (self-fund mode).");
      console.log("   Now add USDC for API calls — send from any wallet or CEX.");
    }
    console.log();
    stepCounter++;
  } else {
    console.log("(DRY-RUN mode — no real money or wallet funding required)");
    console.log();
  }

  console.log(`${stepCounter}. Add the MCP server to your host config (Claude Code, Cursor, ...):`);
  console.log();
  console.log("   {");
  console.log('     "mcpServers": {');
  console.log('       "stellarflow": {');
  console.log('         "command": "npx",');
  console.log('         "args": ["x402-mcp", "run"],');
  console.log('         "env": {');
  console.log(`           "STELLARFLOW_DRY_RUN": "${cfg.dryRun ? "true" : "false"}"`);
  console.log("         }");
  console.log("       }");
  console.log("     }");
  console.log("   }");
  console.log();
  stepCounter++;

  console.log(`${stepCounter}. Restart your MCP host. The wallet at ~/.config/x402-mcp/agent.json`);
  console.log("   will be loaded automatically.");
  console.log();
  stepCounter++;

  if (session) {
    const sessionParam = encodeSession(session);
    const dashboardURL = `${cfg.frontendURL}/wallet/${address}?session=${encodeURIComponent(sessionParam)}`;
    console.log(`${stepCounter}. Open your usage dashboard (one-time login link):`);
    console.log();
    console.log(`   ${dashboardURL}`);
    console.log();
    console.log("   This link contains your session token. The dashboard adopts");
    console.log("   it on load and redirects to the clean URL.");
    console.log();
    console.log("   (If your frontend is on a different port, set STELLARFLOW_FRONTEND_URL");
    console.log(`    when running init — e.g. STELLARFLOW_FRONTEND_URL=http://localhost:5176)`);
  } else {
    console.log(`${stepCounter}. Sign in to your usage dashboard manually (SEP-10 sign-in failed during init):`);
    console.log();
    console.log(`   ${cfg.frontendURL}/wallet/${address}`);
  }
  console.log();

  // ── Wallet credentials block ──────────────────────────────
  // Prints the S-key + 24-word recovery phrase so the user can import
  // into Freighter. This is deliberately verbose — Freighter is the
  // canonical way to check balance, fund, and manage the wallet
  // outside the MCP flow.
  printFreighterImportInstructions(address, agentKP.secret(), mnemonic);

  return wallet;
}

/**
 * Prints a clearly-fenced block with the wallet's import credentials
 * and step-by-step instructions for adding the account to Freighter.
 * Both methods (recovery phrase and secret key) derive the same
 * G-address because we generated via SEP-0005.
 */
function printFreighterImportInstructions(
  address: string,
  secret: string,
  mnemonic: string,
): void {
  logHeader("Wallet credentials — import into Freighter for manual management");

  console.log("  Use either method below — both give you the SAME account.");
  console.log();
  console.log(`  ${"Public (G-address):".padEnd(22)}${address}`);
  console.log();
  console.log(`  ${"Secret key (S-key):".padEnd(22)}${secret}`);
  console.log();
  console.log("  Recovery phrase (24 words, BIP39 / SEP-0005):");
  console.log();
  const words = mnemonic.split(" ");
  // Print in a 4-column grid: col1 | col2 | col3 | col4
  const rows = 6;
  for (let r = 0; r < rows; r++) {
    const parts: string[] = [];
    for (let c = 0; c < 4; c++) {
      const idx = c * rows + r;
      if (idx < words.length) {
        const n = (idx + 1).toString().padStart(2, " ");
        parts.push(`${n}. ${words[idx].padEnd(10)}`);
      }
    }
    console.log("     " + parts.join("  "));
  }
  console.log();
  console.log("  How to import into Freighter (https://freighter.app):");
  console.log();
  console.log("    Option A — Recovery phrase (recommended):");
  console.log("      1. Open Freighter → top-right menu → 'Add account'");
  console.log("      2. Click 'Import recovery phrase'");
  console.log("      3. Paste or type the 24 words above");
  console.log("      4. Freighter will derive the SAME G-address automatically.");
  console.log();
  console.log("    Option B — Secret key (faster):");
  console.log("      1. Open Freighter → top-right menu → 'Add account'");
  console.log("      2. Click 'Import secret key'");
  console.log("      3. Paste the S-key above.");
  console.log();
  console.log("  After import, Freighter is your GUI for checking balance,");
  console.log("  sending/receiving USDC, and re-logging into the operator");
  console.log("  dashboard when your session expires.");
  console.log();
  console.log("  Full wallet file (read-only, 0600): ~/.config/x402-mcp/agent.json");
  console.log();
}
