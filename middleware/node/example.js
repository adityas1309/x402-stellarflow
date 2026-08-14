// Full demo of @stellarflow/x402-middleware — paywall + SEP-10 auth + dashboard.
//
// Run with:
//   cd middleware/node
//   npm install express
//   node example.js
//
// Then open:
//   http://localhost:3000/api/catalog         — public catalog
//   http://localhost:3000/x402/dashboard      — operator dashboard (SEP-10 login)

const express = require("express");
const { Keypair } = require("@stellar/stellar-sdk");
const crypto = require("node:crypto");
const { x402, sep10Auth, dashboard } = require("./dist/index.js");

// Ephemeral dev config — in a real app, run `npx x402-init` to write these
// to .env once. Here we generate fresh values on every start so the example
// is self-contained.
if (!process.env.SEP10_SERVER_SIGNING_KEY) {
  const kp = Keypair.random();
  process.env.SEP10_SERVER_SIGNING_KEY = kp.secret();
  process.env.SEP10_SERVER_ADDRESS = kp.publicKey();
}
process.env.SEP10_HOME_DOMAIN ??= "localhost";
process.env.X402_JWT_SECRET ??= crypto.randomBytes(32).toString("hex");
process.env.X402_TREASURY_ADDRESS ??= "GBLDFWELHTPY4SIW6BNHDPFAYLH3NR5N2HK5VTK5GPAUMK5OESE4SYR7";
process.env.X402_DRY_RUN ??= "true";

const app = express();
app.use(express.json());

// SEP-10 /api/auth/challenge + /api/auth/token
app.use(sep10Auth.routes());

// Embedded dashboard at /x402/dashboard (stats at /x402/stats).
// For the example we skip the allowlist so any valid SEP-10 sign-in works.
// In production pass { allowedWallets: ['GXXX...'] } to restrict access.
dashboard.mount(app);

// Public catalog endpoint — not part of the library, just for the example.
app.get("/api/catalog", (_req, res) => {
  res.json({
    catalog: [
      {
        endpoint: "sentiment",
        path: "/api/sentiment",
        method: "POST",
        price_usdc: 0.05,
        description: "Sentiment analysis on any topic.",
      },
    ],
  });
});

// Paid endpoint.
app.post(
  "/api/sentiment",
  x402({ price: 0.05, description: "sentiment" }),
  (req, res) => {
    const topic = req.body?.topic ?? "unknown";
    res.json({
      topic,
      sentiment: "positive",
      score: 0.82,
      paidBy: req.x402?.payer,
    });
  },
);

const PORT = 3000;
app.listen(PORT, () => {
  console.log(`\n✓ Example running on http://localhost:${PORT}`);
  console.log(`  Catalog:   http://localhost:${PORT}/api/catalog`);
  console.log(`  Dashboard: http://localhost:${PORT}/x402/dashboard`);
  console.log(`\nTest the paywall:`);
  console.log(`  curl -X POST http://localhost:${PORT}/api/sentiment -H 'Content-Type: application/json' -d '{"topic":"stellar"}'`);
  console.log(`  → 402 Payment Required\n`);
});
