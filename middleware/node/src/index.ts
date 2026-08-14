/**
 * @stellarflow/x402-middleware — Express middleware for x402 USDC paywalls on Stellar.
 *
 * Usage:
 *
 *   import express from 'express';
 *   import { x402 } from '@stellarflow/x402-middleware';
 *
 *   const app = express();
 *   app.use(express.json());
 *
 *   app.post('/api/sentiment',
 *     x402({
 *       price: 0.05,
 *       treasury: 'GBLDFWELHTPY4SIW6BNHDPFAYLH3NR5N2HK5VTK5GPAUMK5OESE4SYR7',
 *       facilitatorUrl: 'https://channels.openzeppelin.com/x402/testnet',
 *       facilitatorKey: 'your-api-key',
 *     }),
 *     (req, res) => {
 *       // req.x402.payer is the Stellar G-address that paid
 *       res.json({ sentiment: 'positive', score: 0.82 });
 *     }
 *   );
 *
 * That's it. The middleware handles:
 *   1. Returning 402 with payment requirements when no X-PAYMENT header
 *   2. Decoding and validating the X-PAYMENT header
 *   3. Calling the OZ facilitator to verify the payment
 *   4. Settling and confirming the payment on-chain
 *   5. Calling the protected handler only after settlement succeeds
 *   6. Setting req.x402.payer so your handler knows who paid
 */

import type { Request, Response, NextFunction } from "express";
import { defaultLogger } from "./logger.js";

// ─── Public types ──────────────────────────────────────────────

export interface X402Options {
  /** Price in USDC for this endpoint (e.g. 0.05) */
  price: number;
  /** Stellar G-address of the treasury that receives payments. Defaults to env X402_TREASURY_ADDRESS. */
  treasury?: string;
  /** OZ facilitator URL (default: env X402_FACILITATOR_URL or testnet endpoint) */
  facilitatorUrl?: string;
  /** OZ facilitator API key (default: env X402_FACILITATOR_KEY). Get one: curl -X POST https://channels.openzeppelin.com/gen */
  facilitatorKey?: string;
  /** Stellar network (default: env X402_NETWORK or "stellar:testnet") */
  network?: string;
  /** USDC Soroban contract address (default: testnet SAC) */
  usdcContract?: string;
  /** USDC classic issuer (default: Stellar testnet USDC issuer) */
  usdcIssuer?: string;
  /** Description shown in the 402 response (default: endpoint path) */
  description?: string;
  /** Dry-run mode — skip facilitator calls, accept any payment header (default: env X402_DRY_RUN or false) */
  dryRun?: boolean;
}

export interface X402Context {
  /** Stellar G-address of the agent that paid */
  payer: string;
  /** USDC amount paid */
  amount: number;
  /** Facilitator used ("openzeppelin" or "dry-run") */
  facilitator: string;
}

// Extend Express Request
declare global {
  namespace Express {
    interface Request {
      x402?: X402Context;
    }
  }
}

// ─── Constants ─────────────────────────────────────────────────

const DEFAULT_FACILITATOR_URL = "https://channels.openzeppelin.com/x402/testnet";
const DEFAULT_NETWORK = "stellar:testnet";
const DEFAULT_USDC_CONTRACT = "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA";
const DEFAULT_USDC_ISSUER = "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5";
const X402_VERSION = 2;

// ─── Middleware factory ────────────────────────────────────────

export function x402(opts: X402Options) {
  const {
    price,
    treasury = process.env.X402_TREASURY_ADDRESS ?? "",
    facilitatorUrl = process.env.X402_FACILITATOR_URL ?? DEFAULT_FACILITATOR_URL,
    facilitatorKey = process.env.X402_FACILITATOR_KEY ?? "",
    network = process.env.X402_NETWORK ?? DEFAULT_NETWORK,
    usdcContract = process.env.X402_USDC_CONTRACT ?? DEFAULT_USDC_CONTRACT,
    usdcIssuer = process.env.X402_USDC_ISSUER ?? DEFAULT_USDC_ISSUER,
    description,
    dryRun = (process.env.X402_DRY_RUN ?? "").toLowerCase() === "true",
  } = opts;

  if (!treasury) {
    throw new Error(
      "x402: no treasury address. Set X402_TREASURY_ADDRESS env var or pass treasury option. Run `npx x402-init` to generate one."
    );
  }

  // Pre-build the payment requirements (they don't change per request).
  const amountStroops = Math.round(price * 10_000_000).toString();

  return async (req: Request, res: Response, next: NextFunction): Promise<void> => {
    const requestId = `x402-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
    trace(requestId, "request", { method: req.method, path: req.path, hasPayment: Boolean(req.headers["x-payment"]) });
    const requirement = {
      scheme: "exact" as const,
      network,
      asset: usdcContract,
      amount: amountStroops,
      payTo: treasury,
      maxTimeoutSeconds: 30,
      extra: {
        code: "USDC",
        issuer: usdcIssuer,
        areFeesSponsored: true,
      },
    };

    const paymentRequired = {
      x402Version: X402_VERSION,
      error: "",
      resource: {
        url: `${req.protocol}://${req.get("host")}${req.originalUrl}`,
        description: description ?? req.path,
        mimeType: "application/json",
      },
      accepts: [requirement],
    };

    // 1. No X-PAYMENT header → 402
    const paymentHeader = req.headers["x-payment"] as string | undefined;
    if (!paymentHeader) {
      paymentRequired.error = "X-PAYMENT header missing";
      trace(requestId, "challenge", { status: 402, reason: paymentRequired.error });
      res.status(402).json(paymentRequired);
      return;
    }

    // 2. Decode the payload
    let payload: PaymentPayload;
    try {
      const raw = Buffer.from(paymentHeader, "base64");
      payload = JSON.parse(raw.toString("utf-8"));
    } catch {
      paymentRequired.error = "invalid X-PAYMENT header: base64/JSON decode failed";
      trace(requestId, "reject", { status: 402, reason: paymentRequired.error });
      res.status(402).json(paymentRequired);
      return;
    }

    // 3. Validate requirements match
    const acc = payload.accepted;
    if (
      acc?.scheme !== requirement.scheme ||
      acc?.network !== requirement.network ||
      acc?.payTo !== requirement.payTo ||
      acc?.amount !== requirement.amount
    ) {
      paymentRequired.error = "payment requirements mismatch";
      trace(requestId, "reject", { status: 402, reason: paymentRequired.error });
      res.status(402).json(paymentRequired);
      return;
    }

    // 4. Verify with facilitator (or accept in dry-run)
    let payer = "";
    let facilitatorName = "dry-run";

    if (dryRun) {
      // In dry-run: extract payer from the payload's "from" field.
      try {
        const inner = JSON.parse(
          typeof payload.payload === "string"
            ? payload.payload
            : JSON.stringify(payload.payload)
        );
        payer = inner.from ?? "GDRYRUNXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX";
      } catch {
        payer = "GDRYRUNXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX";
      }
    } else {
      if (!facilitatorKey) {
        paymentRequired.error = "facilitator not configured (set facilitatorKey)";
        res.status(402).json(paymentRequired);
        return;
      }

      // POST /verify to the facilitator
      try {
        trace(requestId, "verify_start", { facilitator: facilitatorUrl, amount: requirement.amount, payTo: requirement.payTo });
        const verifyResp = await facilitatorPost(facilitatorUrl, facilitatorKey, "/verify", {
          x402Version: X402_VERSION,
          paymentPayload: payload,
          paymentRequirements: requirement,
        });

        if (!verifyResp.isValid) {
          paymentRequired.error = `payment rejected: ${verifyResp.invalidReason ?? "unknown"}`;
          trace(requestId, "verify_reject", { reason: paymentRequired.error });
          res.status(402).json(paymentRequired);
          return;
        }
        payer = String(verifyResp.payer ?? "");
        facilitatorName = "openzeppelin";
        trace(requestId, "verify_ok", { payer: shortAddress(payer) });
      } catch (err) {
        paymentRequired.error = `facilitator verify error: ${(err as Error).message}`;
        trace(requestId, "verify_error", { reason: paymentRequired.error });
        res.status(402).json(paymentRequired);
        return;
      }
    }

    const startedAt = Date.now();
    const normalizedPayer = normalizeStellarAddress(payer);

    // 5. Set req.x402 for the downstream handler.
    req.x402 = {
      payer: normalizedPayer,
      amount: price,
      facilitator: facilitatorName,
    };

    // 6. Settle before releasing the paid response. A 200 from /verify only
    // proves the authorization is valid; /settle must also confirm that the
    // USDC transfer was submitted on-chain.
    if (!dryRun && facilitatorKey) {
      try {
        trace(requestId, "settle_start", { payer: shortAddress(normalizedPayer), amount: requirement.amount, payTo: requirement.payTo });
        const settlement = await facilitatorPost(facilitatorUrl, facilitatorKey, "/settle", {
          x402Version: X402_VERSION,
          paymentPayload: payload,
          paymentRequirements: requirement,
        });
        if (settlement.success !== true) {
          paymentRequired.error = `payment settlement failed: ${String(settlement.errorReason ?? settlement.errorMessage ?? "unknown")}`;
          trace(requestId, "settle_reject", { reason: paymentRequired.error });
          res.status(402).json(paymentRequired);
          return;
        }
        trace(requestId, "settle_ok", { transaction: settlement.transaction ?? settlement.txHash ?? null });
      } catch (err) {
        paymentRequired.error = `facilitator settle error: ${(err as Error).message}`;
        trace(requestId, "settle_error", { reason: paymentRequired.error });
        res.status(402).json(paymentRequired);
        return;
      }
    }

    // 7. Call the protected handler only after settlement is confirmed.
    res.on("finish", () => {
      trace(requestId, "response", { status: res.statusCode, payer: shortAddress(normalizedPayer), durationMs: Date.now() - startedAt });
      if (res.statusCode < 400) {
        defaultLogger.log({
          timestamp: startedAt,
          endpoint: description ?? req.path,
          payer: normalizedPayer,
          amount: price,
          facilitator: facilitatorName,
          durationMs: Date.now() - startedAt,
        });
      }
    });
    next();

  };
}

function shortAddress(address: string): string {
  return address && address.length > 12 ? `${address.slice(0, 6)}...${address.slice(-6)}` : address;
}

function trace(requestId: string, event: string, details: Record<string, unknown>): void {
  console.log(JSON.stringify({ scope: "stellarflow.x402", requestId, event, timestamp: new Date().toISOString(), ...details }));
}

// ─── Facilitator HTTP client ───────────────────────────────────

interface PaymentPayload {
  x402Version: number;
  accepted: {
    scheme: string;
    network: string;
    asset: string;
    amount: string;
    payTo: string;
  };
  payload: unknown;
  resource?: unknown;
}

async function facilitatorPost(
  baseUrl: string,
  apiKey: string,
  path: string,
  body: unknown,
): Promise<Record<string, unknown>> {
  const resp = await fetch(`${baseUrl.replace(/\/$/, "")}${path}`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json",
      Authorization: `Bearer ${apiKey}`,
    },
    body: JSON.stringify(body),
  });

  const text = await resp.text();
  if (!resp.ok) {
    throw new Error(`facilitator ${path} returned ${resp.status}: ${text}`);
  }
  return JSON.parse(text);
}

function normalizeStellarAddress(addr: string): string {
  if (addr.length === 56 && addr.startsWith("G")) {
    return addr;
  }
  return "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAADRYRUNANONYMOUSXXXXX";
}

// ─── Re-export for convenience ─────────────────────────────────

export { sep10Auth } from "./sep10.js";
export type { Sep10Options, ProtectOptions } from "./sep10.js";
export { dashboard } from "./dashboard.js";
export type { DashboardOptions } from "./dashboard.js";
export { CallLogger, defaultLogger } from "./logger.js";
export type { PaidCallLog, StatsSummary } from "./logger.js";
export default x402;
