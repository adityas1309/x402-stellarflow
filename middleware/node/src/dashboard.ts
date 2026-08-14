/**
 * Embedded SEP-10 operator dashboard for @stellarflow/x402-middleware.
 *
 * Mounts three routes on the user's Express app:
 *   GET  /x402/dashboard   — a single-page HTML/JS dashboard (Freighter login + stats)
 *   GET  /x402/stats       — JSON stats, gated by SEP-10 JWT
 *   (SEP-10 /api/auth/* routes must be mounted separately via sep10Auth.routes())
 *
 * Usage:
 *
 *   import express from 'express';
 *   import { x402, sep10Auth, dashboard } from '@stellarflow/x402-middleware';
 *
 *   const app = express();
 *   app.use(express.json());
 *   app.use(sep10Auth.routes());
 *   dashboard.mount(app, { allowedWallets: [process.env.X402_TREASURY_ADDRESS!] });
 *
 *   app.post('/api/sentiment', x402({ price: 0.05 }), myHandler);
 *
 *   app.listen(3000);
 *   // → open http://localhost:3000/x402/dashboard
 *   // → click "Sign in with Freighter"
 *   // → see live revenue, unique payers, recent calls
 */

import type { Express, Request, Response } from "express";
import { sep10Auth } from "./sep10.js";
import { defaultLogger } from "./logger.js";

export interface DashboardOptions {
  /** Only these wallets can load stats. Defaults to anyone with a valid SEP-10 session. */
  allowedWallets?: string[];
  /** Override the default mount path (default "/x402/dashboard"). */
  dashboardPath?: string;
  /** Override the default stats path (default "/x402/stats"). */
  statsPath?: string;
}

function mount(app: Express, opts: DashboardOptions = {}): void {
  const dashboardPath = opts.dashboardPath ?? "/x402/dashboard";
  const statsPath = opts.statsPath ?? "/x402/stats";

  // Stats endpoint — protected by SEP-10 JWT.
  app.get(
    statsPath,
    sep10Auth.protect({ allowedWallets: opts.allowedWallets }),
    (_req: Request, res: Response) => {
      res.json(defaultLogger.stats());
    },
  );

  // HTML dashboard — public URL, auth happens client-side via Freighter.
  app.get(dashboardPath, (_req: Request, res: Response) => {
    res.setHeader("Content-Type", "text/html; charset=utf-8");
    res.send(dashboardHTML(statsPath));
  });
}

export const dashboard = { mount };

// ─── Embedded HTML ─────────────────────────────────────────────

function dashboardHTML(statsPath: string): string {
  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>x402 Operator Dashboard</title>
<style>
  :root {
    color-scheme: dark;
    --bg: #0a0e1a;
    --panel: #11172a;
    --border: #1f2744;
    --text: #e6e9f4;
    --dim: #8b92a8;
    --accent: #00d4ff;
    --good: #22d3a4;
    --warn: #ffb547;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    background: var(--bg);
    color: var(--text);
    min-height: 100vh;
  }
  header {
    padding: 20px 32px;
    border-bottom: 1px solid var(--border);
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  h1 { margin: 0; font-size: 18px; font-weight: 600; letter-spacing: 0.3px; }
  h1 span { color: var(--accent); }
  .wallet { font-family: ui-monospace, monospace; font-size: 12px; color: var(--dim); }
  main { padding: 24px 32px; max-width: 1100px; }
  .row { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 16px; margin-bottom: 24px; }
  .card {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 18px 22px;
  }
  .label { color: var(--dim); font-size: 12px; text-transform: uppercase; letter-spacing: 0.6px; margin-bottom: 8px; }
  .value { font-size: 28px; font-weight: 700; }
  .value.accent { color: var(--accent); }
  .value.good { color: var(--good); }
  table { width: 100%; border-collapse: collapse; font-size: 13px; }
  th, td { text-align: left; padding: 10px 12px; border-bottom: 1px solid var(--border); }
  th { color: var(--dim); font-weight: 500; text-transform: uppercase; font-size: 11px; letter-spacing: 0.5px; }
  td.mono { font-family: ui-monospace, monospace; color: var(--dim); }
  td.amount { color: var(--good); font-weight: 600; }
  .section-title { font-size: 14px; font-weight: 600; margin: 24px 0 12px; color: var(--dim); text-transform: uppercase; letter-spacing: 0.6px; }
  button {
    background: var(--accent);
    color: var(--bg);
    border: none;
    padding: 10px 20px;
    border-radius: 8px;
    font-weight: 600;
    font-size: 14px;
    cursor: pointer;
  }
  button:hover { opacity: 0.9; }
  button.secondary { background: transparent; color: var(--text); border: 1px solid var(--border); }
  .center {
    display: flex; flex-direction: column; align-items: center; justify-content: center;
    min-height: 60vh; text-align: center; gap: 14px;
  }
  .center h2 { margin: 0; font-size: 22px; font-weight: 600; }
  .center p { color: var(--dim); max-width: 400px; margin: 0; line-height: 1.5; }
  .error { color: #ff6b8a; font-size: 13px; margin-top: 8px; }
  .small { font-size: 11px; color: var(--dim); font-family: ui-monospace, monospace; }
</style>
</head>
<body>
<header>
  <h1>x402 <span>operator dashboard</span></h1>
  <div id="walletLabel" class="wallet"></div>
</header>

<main id="root">
  <div class="center">
    <h2>Sign in with your Stellar wallet</h2>
    <p>This dashboard uses <strong>SEP-10 Web Authentication</strong>. Your wallet signs a challenge transaction — no email, no password, no cookies. Only wallets listed in the operator allowlist can view stats.</p>
    <button onclick="signInFreighter()">🚀 Sign in with Freighter</button>
    <button class="secondary" onclick="signInManual()">Paste an S-key (dev only)</button>
    <div id="err" class="error"></div>
    <div class="small">Powered by @stellarflow/x402-middleware</div>
  </div>
</main>

<script type="module">
  const STATS_PATH = ${JSON.stringify(statsPath)};
  const API_CHALLENGE = "/api/auth/challenge";
  const API_TOKEN = "/api/auth/token";

  let jwt = sessionStorage.getItem("x402-jwt");
  let walletAddr = sessionStorage.getItem("x402-wallet");

  if (jwt && walletAddr) {
    loadDashboard();
  }

  window.signInFreighter = async () => {
    setError("");
    try {
      // Try Freighter API dynamic import
      const freighter = await import("https://esm.sh/@stellar/freighter-api@6").catch(() => null);
      if (!freighter || !freighter.isConnected) {
        throw new Error("Freighter not detected. Install https://freighter.app");
      }
      const allowed = await freighter.requestAccess();
      const addr = allowed?.address ?? await freighter.getAddress();
      if (!addr || addr.error) throw new Error(addr?.error ?? "no address returned");

      // Fetch SEP-10 challenge
      const chRes = await fetch(API_CHALLENGE + "?account=" + encodeURIComponent(typeof addr === "string" ? addr : addr.address));
      if (!chRes.ok) throw new Error("challenge failed: " + chRes.status);
      const challenge = await chRes.json();

      // Sign the challenge XDR with Freighter
      const signed = await freighter.signTransaction(challenge.transaction, {
        networkPassphrase: challenge.network_passphrase,
      });
      const signedXDR = signed?.signedTxXdr ?? signed?.signedXDR ?? signed;
      if (!signedXDR || typeof signedXDR !== "string") {
        throw new Error("Freighter did not return a signed XDR");
      }

      const tokRes = await fetch(API_TOKEN, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ id: challenge.id, transaction: signedXDR }),
      });
      if (!tokRes.ok) throw new Error("token exchange failed: " + await tokRes.text());
      const tok = await tokRes.json();

      sessionStorage.setItem("x402-jwt", tok.token);
      sessionStorage.setItem("x402-wallet", tok.wallet);
      jwt = tok.token;
      walletAddr = tok.wallet;
      loadDashboard();
    } catch (e) {
      setError(e.message || String(e));
    }
  };

  window.signInManual = async () => {
    const addr = prompt("Paste your Stellar G-address (public key):");
    if (!addr) return;
    setError("Manual sign-in requires the S-key on the server side. Use Freighter instead. This button is a placeholder.");
  };

  function setError(msg) {
    document.getElementById("err").textContent = msg;
  }

  async function loadDashboard() {
    try {
      const res = await fetch(STATS_PATH, {
        headers: { Authorization: "Bearer " + jwt },
      });
      if (res.status === 401) {
        sessionStorage.clear();
        location.reload();
        return;
      }
      if (!res.ok) throw new Error("stats failed: " + res.status);
      const stats = await res.json();
      renderDashboard(stats);
    } catch (e) {
      setError(e.message);
    }
  }

  function renderDashboard(stats) {
    const walletShort = walletAddr.slice(0, 6) + "…" + walletAddr.slice(-6);
    document.getElementById("walletLabel").innerHTML =
      'signed in as <strong>' + walletShort + '</strong> · <a href="#" onclick="signOut();return false" style="color:var(--dim)">sign out</a>';

    const recentRows = stats.recent.map((c) => {
      const t = new Date(c.timestamp).toLocaleTimeString();
      const payerShort = c.payer.slice(0, 4) + "…" + c.payer.slice(-4);
      return \`<tr>
        <td class="mono">\${t}</td>
        <td>\${c.endpoint}</td>
        <td class="mono">\${payerShort}</td>
        <td class="amount">$\${c.amount.toFixed(2)}</td>
        <td class="mono">\${c.facilitator}</td>
        <td class="mono">\${c.durationMs}ms</td>
      </tr>\`;
    }).join("");

    const byEpRows = stats.byEndpoint.map((e) => \`
      <tr>
        <td>\${e.endpoint}</td>
        <td>\${e.calls}</td>
        <td class="amount">$\${e.revenue.toFixed(2)}</td>
      </tr>
    \`).join("");

    document.getElementById("root").innerHTML = \`
      <div class="row">
        <div class="card">
          <div class="label">Total revenue</div>
          <div class="value good">$\${stats.totalRevenue.toFixed(2)}</div>
        </div>
        <div class="card">
          <div class="label">Total calls</div>
          <div class="value accent">\${stats.totalCalls}</div>
        </div>
        <div class="card">
          <div class="label">Unique payers</div>
          <div class="value accent">\${stats.uniquePayers}</div>
        </div>
      </div>

      <div class="section-title">Revenue by endpoint</div>
      <div class="card" style="padding:0">
        <table>
          <thead><tr><th>Endpoint</th><th>Calls</th><th>Revenue</th></tr></thead>
          <tbody>\${byEpRows || '<tr><td colspan="3" style="color:var(--dim);padding:16px">No paid calls yet.</td></tr>'}</tbody>
        </table>
      </div>

      <div class="section-title">Recent calls (last 50)</div>
      <div class="card" style="padding:0">
        <table>
          <thead><tr><th>Time</th><th>Endpoint</th><th>Payer</th><th>Amount</th><th>Facilitator</th><th>Duration</th></tr></thead>
          <tbody>\${recentRows || '<tr><td colspan="6" style="color:var(--dim);padding:16px">No paid calls yet.</td></tr>'}</tbody>
        </table>
      </div>
    \`;
  }

  window.signOut = () => {
    sessionStorage.clear();
    location.reload();
  };

  // Auto-refresh every 5s when logged in.
  setInterval(() => {
    if (jwt) loadDashboard();
  }, 5000);
</script>
</body>
</html>`;
}
