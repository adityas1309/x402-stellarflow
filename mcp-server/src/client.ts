// HTTP client for the stellarflow x402 backend, plus the helpers that turn an
// MCP tool invocation into a paid HTTP request.
//
// Two operating modes:
//
//  - DRY-RUN (default while iterating without spending real USDC):
//    The X-PAYMENT header is a v2 PaymentPayload envelope where the
//    `payload` field is a stub `{from, intent}` object instead of a real
//    signed Soroban transaction. The stellarflow backend accepts this as
//    long as X402_DRY_RUN=true on its end and uses the `from` field as
//    the payer address.
//
//  - LIVE: a full x402 round-trip:
//      1. Optimistically POST without X-PAYMENT, get back the 402 with
//         the operator's PaymentRequirements.
//      2. Pick the (scheme=exact, network=stellar:testnet) requirement.
//      3. Use @x402/stellar's ExactStellarScheme to sign a Soroban auth
//         entry against those requirements.
//      4. Wrap as a v2 PaymentPayload, base64-encode, set X-PAYMENT.
//      5. Re-POST and return the response.
//    Live mode requires STELLARFLOW_AGENT_SECRET (a real Stellar S-key) and
//    USDC funded on that wallet.

export interface StellarFlowConfig {
  baseURL: string;        // e.g. http://localhost:8086
  agentAddress: string;   // 56-char Stellar G-address used as the payer
  network: "stellar:testnet";
  clientID: string;       // forwarded as X-StellarFlow-Client header
  dryRun: boolean;        // if true, use stub payment payload
  agentSecret?: string;   // S-key for signing — required when dryRun=false
  rpcUrl?: string;        // Soroban RPC URL — required when dryRun=false on testnet
                          // (the @x402/stellar SDK needs it to simulate + assemble
                          // the Soroban auth entry transaction it signs)
}

interface PaymentRequirements {
  scheme: string;
  network: string;
  asset: string;
  amount: string;
  payTo: string;
  maxTimeoutSeconds: number;
  extra: Record<string, unknown>;
}

interface PaymentRequired {
  x402Version: number;
  error?: string;
  resource: { url: string; description?: string; mimeType?: string };
  accepts: PaymentRequirements[];
}

export interface PaidCallOptions {
  endpoint: string;          // e.g. "posting_heatmap"
  body: unknown;             // request JSON body
  reasoning?: string;        // optional reasoning forwarded to the live feed
  priceUSDC: number;         // for budget enforcement (caller knows the price)
}

export interface PaidCallResult {
  status: number;
  data: unknown;
  spentUSDC: number;
}

/**
 * Build a DRY-RUN x402 v2 PaymentPayload envelope. The `accepted` field
 * mirrors the requirements the server would publish (we don't fetch them
 * first — we know them). The `payload.from` field is what the dry-run
 * middleware uses as the payer address.
 */
function buildDryRunHeader(req: PaymentRequirements, cfg: StellarFlowConfig): string {
  const envelope = {
    x402Version: 2,
    accepted: req,
    payload: {
      from: cfg.agentAddress,
      intent: "dry-run",
    },
  };
  return Buffer.from(JSON.stringify(envelope)).toString("base64");
}

/**
 * Optimistically POST a paid endpoint without an X-PAYMENT header to read
 * back its current PaymentRequirements. Used by the LIVE flow before signing.
 */
async function fetchRequirements(
  cfg: StellarFlowConfig,
  endpoint: string,
): Promise<PaymentRequirements> {
  const resp = await fetch(`${cfg.baseURL}/api/x402/${endpoint}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: "{}",
  });
  if (resp.status !== 402) {
    throw new Error(
      `expected 402 from ${endpoint} probe, got ${resp.status}: ${await resp.text()}`,
    );
  }
  const required: PaymentRequired = await resp.json();
  const match = required.accepts.find(
    (r) => r.scheme === "exact" && r.network === cfg.network,
  );
  if (!match) {
    throw new Error(
      `no matching requirement: server offers ${required.accepts.map((a) => `${a.scheme}/${a.network}`).join(", ")}, client wants exact/${cfg.network}`,
    );
  }
  return match;
}

/**
 * Build a LIVE signed PaymentPayload envelope using @x402/stellar.
 *
 * This is intentionally lazy-imported so the SDK (and its @stellar/stellar-sdk
 * dependency) is only loaded when we actually need to sign — keeping startup
 * fast in dry-run mode where it's never used.
 */
async function buildLiveHeader(req: PaymentRequirements, cfg: StellarFlowConfig): Promise<string> {
  if (!cfg.agentSecret) {
    throw new Error("STELLARFLOW_AGENT_SECRET is required when STELLARFLOW_DRY_RUN=false");
  }
  if (!cfg.rpcUrl && cfg.network === "stellar:testnet") {
    throw new Error(
      "STELLARFLOW_RPC_URL is required when STELLARFLOW_DRY_RUN=false on stellar:testnet. " +
        "Set it to a Soroban RPC provider like https://soroban-testnet.stellar.org or " +
        "https://soroban-rpc.creit.tech (free).",
    );
  }
  // Lazy import — pulls in @stellar/stellar-sdk under the hood.
  const { ExactStellarScheme, createEd25519Signer } = await import("@x402/stellar");
  const signer = createEd25519Signer(cfg.agentSecret, cfg.network as Parameters<typeof createEd25519Signer>[1]);
  // Pass the RPC URL via rpcConfig so the scheme can simulate the Soroban
  // auth entry against a live RPC node before signing. Without this the SDK
  // throws "Stellar testnet requires a non-empty rpcUrl" at sign time.
  const scheme = new ExactStellarScheme(signer, cfg.rpcUrl ? { url: cfg.rpcUrl } : undefined);
  const partial = await scheme.createPaymentPayload(2, req as Parameters<typeof scheme.createPaymentPayload>[1]);
  // partial = { x402Version: 2, payload: { transaction: "base64..." } }
  // Wrap with the `accepted` field so the backend can validate.
  const envelope = {
    x402Version: 2,
    accepted: req,
    payload: partial.payload,
  };
  return Buffer.from(JSON.stringify(envelope)).toString("base64");
}


/**
 * Make a paid call to a stellarflow x402 endpoint. Always probes the server
 * for the canonical PaymentRequirements first (one extra HTTP call), then
 * builds the X-PAYMENT header (stub in dry-run, signed in live mode), then
 * re-POSTs with the header. This avoids any client-side knowledge of the
 * operator's treasury address or USDC contract.
 */
async function paidCallOnce(
  cfg: StellarFlowConfig,
  opts: PaidCallOptions,
): Promise<PaidCallResult> {
  // 1. Probe — get the server's current PaymentRequirements for this endpoint.
  const requirement = await fetchRequirements(cfg, opts.endpoint);

  // 2. Build the X-PAYMENT header (stub in dry-run, real signature in live).
  const paymentHeader = cfg.dryRun
    ? buildDryRunHeader(requirement, cfg)
    : await buildLiveHeader(requirement, cfg);

  const url = `${cfg.baseURL}/api/x402/${opts.endpoint}`;
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    "X-Payment": paymentHeader,
    "X-StellarFlow-Client": cfg.clientID,
  };
  if (opts.reasoning) {
    // HTTP headers are restricted to ByteString (ASCII 0-255). Real LLM
    // reasoning text often contains Unicode (em dash, smart quotes, etc).
    // URL-encode the value before sending; the backend URL-decodes it.
    headers["X-StellarFlow-Reason"] = encodeURIComponent(opts.reasoning);
  }

  // 3. Re-POST with the payment header.
  const resp = await fetch(url, {
    method: "POST",
    headers,
    body: JSON.stringify(opts.body),
  });

  const text = await resp.text();
  let data: unknown;
  try {
    data = JSON.parse(text);
  } catch {
    data = { raw: text };
  }

  if (resp.status === 402) {
    throw new Error(
      `payment required by ${opts.endpoint}: ${
        (data as { error?: string })?.error ?? "unknown reason"
      }`,
    );
  }
  if (resp.status >= 400) {
    throw new Error(
      `${opts.endpoint} returned ${resp.status}: ${
        (data as { error?: string })?.error ?? text
      }`,
    );
  }

  return {
    status: resp.status,
    data,
    spentUSDC: opts.priceUSDC,
  };
}

// Soroban RPC simulation and facilitator authorization can briefly race the
// latest ledger. Rebuild the quote and sign a fresh payload instead of
// replaying an authorization that may already be stale or rejected.
export async function paidCall(
  cfg: StellarFlowConfig,
  opts: PaidCallOptions,
): Promise<PaidCallResult> {
  let lastError: unknown;
  for (let attempt = 1; attempt <= 4; attempt += 1) {
    try {
      console.error(`[stellarflow.x402] payment_attempt=${attempt} endpoint=${opts.endpoint} mode=${cfg.dryRun ? "dry-run" : "live"}`);
      return await paidCallOnce(cfg, opts);
    } catch (error) {
      lastError = error;
      const message = error instanceof Error ? error.message : String(error);
      const retryable = /simulation_failed|auth_(already_)?expired|already_used|settlement failed/i.test(message);
      console.error(`[stellarflow.x402] payment_error=${JSON.stringify({ attempt, endpoint: opts.endpoint, retryable, message: message.slice(0, 500) })}`);
      if (!retryable || attempt === 4) throw error;
      console.error(`[stellarflow.x402] retrying_fresh_authorization=true next_attempt=${attempt + 1}`);
      await new Promise((resolve) => setTimeout(resolve, 1500 * attempt));
    }
  }
  throw lastError instanceof Error ? lastError : new Error(String(lastError));
}

/**
 * Fetch the public catalog from the backend, used at server startup so we
 * can validate the prices we have configured locally still match what the
 * operator is publishing.
 */
export async function fetchCatalog(cfg: StellarFlowConfig): Promise<unknown> {
  const resp = await fetch(`${cfg.baseURL}/api/catalog`);
  if (!resp.ok) {
    throw new Error(`catalog fetch failed: ${resp.status}`);
  }
  return resp.json();
}
