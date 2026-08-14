/**
 * SEP-10 Wallet Authentication for Express — the operator-side companion
 * to the x402 paywall. While x402 lets agents pay you per call, SEP-10
 * lets YOU (the operator) sign in to your dashboard with your Stellar
 * wallet instead of rolling your own auth system.
 *
 * The spec: https://github.com/stellar/stellar-protocol/blob/master/ecosystem/sep-0010.md
 *
 * Usage:
 *
 *   import { sep10Auth } from '@stellarflow/x402-middleware';
 *
 *   // Auto-registers /api/auth/challenge (GET) and /api/auth/token (POST)
 *   app.use(sep10Auth.routes());
 *
 *   // Protect operator endpoints — only wallets in allowedWallets can access
 *   app.get('/operator/stats',
 *     sep10Auth.protect({ allowedWallets: ['GBLDFWEL...'] }),
 *     (req, res) => {
 *       // req.wallet is set to the G-address of the signed-in operator
 *       res.json({ wallet: req.wallet });
 *     }
 *   );
 *
 * Configuration is read from environment variables set by `x402-init`:
 *   SEP10_SERVER_SIGNING_KEY  — server signing keypair S-key
 *   SEP10_SERVER_ADDRESS      — server signing keypair G-address
 *   SEP10_HOME_DOMAIN         — the "home" string included in challenges
 *   X402_JWT_SECRET           — HMAC secret for session JWTs
 *   X402_NETWORK              — "stellar:testnet"
 */

import type { Request, Response, NextFunction, Router } from "express";
import { Router as ExpressRouter } from "express";
import {
  Account,
  Keypair,
  Networks,
  Operation,
  Transaction,
  TransactionBuilder,
  BASE_FEE,
} from "@stellar/stellar-sdk";
import jwt from "jsonwebtoken";
import { randomBytes } from "node:crypto";

// ─── Types ─────────────────────────────────────────────────────

declare global {
  namespace Express {
    interface Request {
      wallet?: string; // set by sep10Auth.protect() to the signed-in wallet G-address
    }
  }
}

export interface Sep10Options {
  /** Server signing keypair S-key (default: env SEP10_SERVER_SIGNING_KEY) */
  serverSigningKey?: string;
  /** Home domain string included in challenges (default: env SEP10_HOME_DOMAIN or "localhost") */
  homeDomain?: string;
  /** JWT secret for session tokens (default: env X402_JWT_SECRET) */
  jwtSecret?: string;
  /** Network: "stellar:testnet" (default: env X402_NETWORK or testnet) */
  network?: string;
  /** How long challenges are valid, in seconds (default: 300) */
  challengeTTLSeconds?: number;
  /** How long session JWTs are valid, in seconds (default: 86400 = 24h) */
  sessionTTLSeconds?: number;
}

export interface ProtectOptions {
  /** If set, only these G-addresses are allowed. Otherwise any valid SEP-10 session is accepted. */
  allowedWallets?: string[];
}

// ─── In-memory challenge store ─────────────────────────────────
// Simple Map-based store with TTL. For production with multiple
// backend instances, replace with Redis or a shared store.

interface PendingChallenge {
  xdr: string;
  account: string;
  expiresAt: number;
}

const challenges = new Map<string, PendingChallenge>();

// GC expired challenges every minute
setInterval(() => {
  const now = Date.now();
  for (const [id, ch] of challenges.entries()) {
    if (ch.expiresAt < now) challenges.delete(id);
  }
}, 60 * 1000).unref();

// ─── Config loader ─────────────────────────────────────────────

function loadConfig(opts: Sep10Options): Required<Sep10Options> {
  const cfg = {
    serverSigningKey: opts.serverSigningKey ?? process.env.SEP10_SERVER_SIGNING_KEY ?? "",
    homeDomain: opts.homeDomain ?? process.env.SEP10_HOME_DOMAIN ?? "localhost",
    jwtSecret: opts.jwtSecret ?? process.env.X402_JWT_SECRET ?? "",
    network: opts.network ?? process.env.X402_NETWORK ?? "stellar:testnet",
    challengeTTLSeconds: opts.challengeTTLSeconds ?? 300,
    sessionTTLSeconds: opts.sessionTTLSeconds ?? 86400,
  };
  if (!cfg.serverSigningKey) {
    throw new Error(
      "sep10Auth: SEP10_SERVER_SIGNING_KEY not set. Run `npx x402-init` to generate one."
    );
  }
  if (!cfg.jwtSecret) {
    throw new Error(
      "sep10Auth: X402_JWT_SECRET not set. Run `npx x402-init` to generate one."
    );
  }
  return cfg;
}

function getNetworkPassphrase(network: string): string {
	if (network !== "stellar:testnet") {
		throw new Error(`Unsupported Stellar network: ${network}. Use stellar:testnet.`);
	}
	return Networks.TESTNET;
}

// ─── Challenge construction ────────────────────────────────────

function buildChallengeTransaction(
  serverKP: Keypair,
  clientAccount: string,
  homeDomain: string,
  networkPassphrase: string,
): string {
  // SEP-10 challenge: a 0-sequence transaction with a manage_data op
  // containing a random nonce. Signed by the server keypair, verified
  // by the client signing it too.
  const nonce = randomBytes(48).toString("base64");

  // Synthetic source account with sequence -1 (SEP-10 convention:
  // the server creates a dummy account for the challenge tx).
  const serverAccount = new Account(serverKP.publicKey(), "-1");

  const tx = new TransactionBuilder(serverAccount, {
    fee: BASE_FEE,
    networkPassphrase,
  })
    .addOperation(
      Operation.manageData({
        name: `${homeDomain} auth`,
        value: nonce,
        source: clientAccount,
      }),
    )
    .addOperation(
      Operation.manageData({
        name: "web_auth_domain",
        value: homeDomain,
        source: serverKP.publicKey(),
      }),
    )
    .setTimeout(300)
    .build();

  tx.sign(serverKP);
  return tx.toXDR();
}

function verifyChallenge(
  signedXDR: string,
  expectedAccount: string,
  serverSigningKey: string,
  networkPassphrase: string,
): boolean {
  try {
    const tx = new Transaction(signedXDR, networkPassphrase);
    // Must have at least 2 signatures (server + client)
    if (tx.signatures.length < 2) return false;

    // Verify the client signed it (their signature must be present).
    const clientKP = Keypair.fromPublicKey(expectedAccount);
    const signedByClient = tx.signatures.some((sig) => {
      try {
        return clientKP.verify(tx.hash(), sig.signature());
      } catch {
        return false;
      }
    });
    if (!signedByClient) return false;

    // Verify the server signed it too.
    const serverKP = Keypair.fromSecret(serverSigningKey);
    const signedByServer = tx.signatures.some((sig) => {
      try {
        return serverKP.verify(tx.hash(), sig.signature());
      } catch {
        return false;
      }
    });
    return signedByServer;
  } catch {
    return false;
  }
}

// ─── Public API ────────────────────────────────────────────────

function routes(opts: Sep10Options = {}): Router {
  const cfg = loadConfig(opts);
  const serverKP = Keypair.fromSecret(cfg.serverSigningKey);
  const networkPassphrase = getNetworkPassphrase(cfg.network);

  const router = ExpressRouter();

  // GET /api/auth/challenge?account=G...
  router.get("/api/auth/challenge", (req: Request, res: Response) => {
    const account = typeof req.query.account === "string" ? req.query.account : "";
    if (!account || account.length !== 56 || !account.startsWith("G")) {
      res.status(400).json({ error: "invalid 'account' query parameter" });
      return;
    }

    const xdr = buildChallengeTransaction(serverKP, account, cfg.homeDomain, networkPassphrase);
    const id = randomBytes(16).toString("hex");
    challenges.set(id, {
      xdr,
      account,
      expiresAt: Date.now() + cfg.challengeTTLSeconds * 1000,
    });

    res.json({
      id,
      transaction: xdr,
      network_passphrase: networkPassphrase,
      home_domain: cfg.homeDomain,
      expires_at: new Date(Date.now() + cfg.challengeTTLSeconds * 1000).toISOString(),
    });
  });

  // POST /api/auth/token { id, transaction }
  router.post("/api/auth/token", (req: Request, res: Response) => {
    const { id, transaction } = (req.body ?? {}) as {
      id?: string;
      transaction?: string;
    };
    if (!id || !transaction) {
      res.status(400).json({ error: "missing id or transaction" });
      return;
    }

    const pending = challenges.get(id);
    if (!pending) {
      res.status(401).json({ error: "challenge not found or expired" });
      return;
    }
    if (pending.expiresAt < Date.now()) {
      challenges.delete(id);
      res.status(401).json({ error: "challenge expired" });
      return;
    }

    const valid = verifyChallenge(
      transaction,
      pending.account,
      cfg.serverSigningKey,
      networkPassphrase,
    );
    if (!valid) {
      res.status(401).json({ error: "signature verification failed" });
      return;
    }

    // Single-use
    challenges.delete(id);

    // Issue a JWT session token
    const token = jwt.sign(
      {
        wallet: pending.account,
        iat: Math.floor(Date.now() / 1000),
      },
      cfg.jwtSecret,
      { expiresIn: cfg.sessionTTLSeconds },
    );

    res.json({
      token,
      wallet: pending.account,
      expires_in: cfg.sessionTTLSeconds,
    });
  });

  return router;
}

function protect(protectOpts: ProtectOptions = {}, opts: Sep10Options = {}) {
  const cfg = loadConfig(opts);

  return (req: Request, res: Response, next: NextFunction): void => {
    const authHeader = req.headers.authorization ?? "";
    const token = authHeader.startsWith("Bearer ") ? authHeader.slice(7) : "";
    if (!token) {
      res.status(401).json({ error: "missing Authorization: Bearer <token>" });
      return;
    }

    try {
      const payload = jwt.verify(token, cfg.jwtSecret) as { wallet?: string };
      const wallet = payload.wallet;
      if (!wallet) {
        res.status(401).json({ error: "invalid token: no wallet claim" });
        return;
      }

      if (protectOpts.allowedWallets && protectOpts.allowedWallets.length > 0) {
        if (!protectOpts.allowedWallets.includes(wallet)) {
          res.status(403).json({ error: "wallet not in allowlist" });
          return;
        }
      }

      req.wallet = wallet;
      next();
    } catch (err) {
      res.status(401).json({ error: `invalid token: ${(err as Error).message}` });
    }
  };
}

// Namespace export so users can do `sep10Auth.routes()` and `sep10Auth.protect()`
export const sep10Auth = {
  routes,
  protect,
};
