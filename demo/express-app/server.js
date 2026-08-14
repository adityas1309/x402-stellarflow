// Minimal Express app demonstrating @stellarflow/x402-middleware.
//
// ── HOW TO RUN (for the video demo) ─────────────────────────────
//
//   # One-time setup (before recording):
//   cd middleware/node && npm install && npm run build
//
//   # Demo flow:
//   cd demo/express-app
//   npm install                      # pulls in middleware via file: link
//   npx x402-init                    # interactive setup, writes .env
//   node server.js                   # starts on http://localhost:3000
//
//   # Verify from another terminal:
//   curl http://localhost:3000/api/catalog
//   curl -X POST http://localhost:3000/api/sentiment \
//     -H "Content-Type: application/json" \
//     -d '{"topic":"stellar"}'
//   # → HTTP 402 Payment Required with accepts[]
//
// ── What it shows ────────────────────────────────────────────────
//
// Three pieces of the library wired up in ~30 lines of Express:
//
//   1. x402({ price }) — paywall middleware on /api/sentiment
//   2. sep10Auth.routes() — SEP-10 wallet sign-in at /api/auth/*
//   3. dashboard.mount() — embedded operator dashboard at /x402/dashboard
//
// After making a paid call, open http://localhost:3000/x402/dashboard
// and sign in with Freighter to see the revenue + recent calls.

const express = require("express");
const crypto = require("node:crypto");
const { Keypair } = require("@stellar/stellar-sdk");
const { x402, sep10Auth, dashboard } = require("../../middleware/node/dist/index.js");

// ── Env loading ─────────────────────────────────────────────────
// In a real app you'd use dotenv; here we just read process.env.
// The `npx x402-init` command writes everything to .env; load it
// manually since this example stays framework-free.
try {
  require("node:fs")
    .readFileSync(".env", "utf-8")
    .split(/\r?\n/)
    .forEach((line) => {
      const m = line.match(/^([A-Z0-9_]+)=(.*)$/);
      if (m && !process.env[m[1]]) process.env[m[1]] = m[2];
    });
} catch {
  /* .env optional — fall through to ephemeral generation below */
}

// Ephemeral fallbacks if the user hasn't run `npx x402-init` yet.
// This keeps the demo running even without setup, though the agent
// wallet handoff / SEP-10 signing won't work without a real key.
if (!process.env.SEP10_SERVER_SIGNING_KEY) {
  const kp = Keypair.random();
  process.env.SEP10_SERVER_SIGNING_KEY = kp.secret();
  process.env.SEP10_SERVER_ADDRESS = kp.publicKey();
  console.warn("[demo] ⚠ SEP10_SERVER_SIGNING_KEY not set — generated ephemeral keypair");
}
if (!process.env.X402_JWT_SECRET) {
  process.env.X402_JWT_SECRET = crypto.randomBytes(32).toString("hex");
}
if (!process.env.X402_TREASURY_ADDRESS) {
  // Use the hackathon production treasury by default so the demo's
  // 402 challenge shows a real on-chain address.
  process.env.X402_TREASURY_ADDRESS =
    "GBLDFWELHTPY4SIW6BNHDPFAYLH3NR5N2HK5VTK5GPAUMK5OESE4SYR7";
}

const app = express();
app.use(express.json());

const POSITIVE_TERMS = new Set([
  "best", "benefit", "breakthrough", "clean", "fast", "good", "great", "improve",
  "innovation", "optimistic", "progress", "success", "useful", "win", "wins",
]);
const NEGATIVE_TERMS = new Set([
  "bad", "bug", "concern", "crash", "fail", "flaw", "loss", "problem", "risk",
  "slow", "spam", "unusable", "warning", "worse", "wrong",
]);
const STOP_WORDS = new Set([
  "about", "after", "again", "also", "and", "any", "are", "because", "been", "being",
  "can", "could", "for", "from", "has", "have", "how", "into", "its", "just", "more",
  "most", "new", "now", "one", "only", "our", "over", "that", "than", "their", "these",
  "this", "through", "too", "using", "via", "was", "were", "what", "when", "where",
  "which", "who", "will", "with", "would", "you", "your", "the", "x2f", "https",
]);

function termsFrom(text) {
  return text.toLowerCase().match(/[a-z][a-z0-9-]{2,}/g) ?? [];
}

async function analyzeTopic(topic) {
  const query = String(topic || "").trim().slice(0, 120) || "technology";
  const url = `https://hn.algolia.com/api/v1/search?tags=story&hitsPerPage=20&query=${encodeURIComponent(query)}`;
  const response = await fetch(url, { headers: { accept: "application/json" } });
  if (!response.ok) throw new Error(`public source returned HTTP ${response.status}`);

  const body = await response.json();
  const stories = (body.hits ?? []).filter((story) => story.title || story.story_text);
  const frequencies = new Map();
  const queryTerms = new Set(termsFrom(query));
  let totalSignal = 0;

  for (const story of stories) {
    const words = termsFrom(`${story.title ?? ""} ${story.story_text ?? ""}`);
    let storySignal = 0;
    for (const word of words) {
      if (POSITIVE_TERMS.has(word)) storySignal += 1;
      if (NEGATIVE_TERMS.has(word)) storySignal -= 1;
      if (!STOP_WORDS.has(word) && !queryTerms.has(word)) {
        frequencies.set(word, (frequencies.get(word) ?? 0) + 1);
      }
    }
    totalSignal += Math.max(-3, Math.min(3, storySignal));
  }

  const score = stories.length
    ? Math.max(-1, Math.min(1, totalSignal / (stories.length * 3)))
    : 0;
  const sentiment = score > 0.12 ? "positive" : score < -0.12 ? "negative" : "neutral";
  const keywords = [...frequencies.entries()]
    .sort((a, b) => b[1] - a[1])
    .slice(0, 5)
    .map(([word]) => word);

  return {
    topic: query,
    sentiment,
    score: Number(score.toFixed(2)),
    summary: stories.length
      ? `Analyzed ${stories.length} recent public Hacker News stories mentioning "${query}".`
      : `No recent public Hacker News stories matched "${query}".`,
    sources: stories.length,
    keywords: keywords.length ? keywords : [query.toLowerCase()],
    engine: "hacker-news-lexicon-v1",
    source: "Hacker News Algolia API",
  };
}

// ── (1) SEP-10 wallet auth ──────────────────────────────────────
// Auto-registers GET /api/auth/challenge + POST /api/auth/token.
app.use(sep10Auth.routes());

// ── (2) Embedded operator dashboard ─────────────────────────────
// Serves /x402/dashboard (HTML + Freighter login) and /x402/stats
// (JWT-protected JSON endpoint).
dashboard.mount(app);

// ── (3) Public catalog — not part of the library, just for agents ─
app.get("/api/catalog", (_req, res) => {
  res.json({
    catalog: [
      {
        endpoint: "sentiment",
        method: "POST",
        path: "/api/x402/sentiment",
        price_usdc: 0.05,
      description: "Live public-signal analysis on any topic",
        network: "stellar:testnet",
        pay_to: process.env.X402_TREASURY_ADDRESS,
        asset_code: "USDC",
      },
    ],
  });
});

// ── (4) Paid endpoint — one middleware line ─────────────────────
app.post(
  ["/api/sentiment", "/api/x402/sentiment"],
  async (req, res, next) => {
    try {
      res.locals.topicAnalysis = await analyzeTopic(req.body?.topic);
      next();
    } catch (error) {
      res.status(502).json({
        error: "The public analysis source is temporarily unavailable.",
        details: error.message,
      });
    }
  },
  x402({ price: 0.05, description: "sentiment" }),
  (req, res) => {
    const analysis = res.locals.topicAnalysis;
    // Your handler runs AFTER payment is verified. The x402 middleware
    // has already settled the payment with the OZ facilitator by this
    // point (or accepted a dry-run stub). You do whatever business
    // logic you want here — call OpenAI, query a database, whatever.
    res.json({
      ...analysis,
      paidBy: req.x402?.payer,
      amountUsdc: req.x402?.amount,
    });
  },
);

// ── Start ────────────────────────────────────────────────────────
const PORT = Number(process.env.PORT ?? 3000);
function start() {
app.listen(PORT, () => {
  console.log();
  console.log("  ╔════════════════════════════════════════════════════════════╗");
  console.log("  ║  x402 Express demo                                          ║");
  console.log("  ╚════════════════════════════════════════════════════════════╝");
  console.log();
  console.log(`  Server:     http://localhost:${PORT}`);
  console.log(`  Catalog:    http://localhost:${PORT}/api/catalog`);
  console.log(`  Paywall:    POST http://localhost:${PORT}/api/sentiment`);
  console.log(`  Dashboard:  http://localhost:${PORT}/x402/dashboard`);
  console.log();
  console.log("  Try it:");
  console.log(`    curl -X POST http://localhost:${PORT}/api/sentiment \\`);
  console.log(`      -H "Content-Type: application/json" \\`);
  console.log(`      -d '{"topic":"stellar"}'`);
  console.log("    → HTTP 402 Payment Required");
  console.log();
});
}

if (require.main === module) start();

module.exports = app;
