-- Migration 010: Stellar wallet auth (SEP-10) + x402 payments
--
-- Adds the four tables that enable StellarFlow to operate as agent-native
-- infrastructure: wallet sign-in for operators (SEP-10), per-endpoint pricing
-- in USDC, and an immutable log of every paid agent call (drives the live
-- activity feed and the operator dashboard).
--
-- The existing email/password auth tables (users, refresh_tokens) are NOT
-- touched. SEP-10 logins create a synthetic user record with an email of
-- the form `wallet+G...@stellar.local` and an unusable hashed_password.
-- This way the existing handlers and middleware keep working unchanged.

-- ─────────────────────────────────────────────────────────────────────────
-- 1. WALLETS — links a Stellar account to a StellarFlow user (1:1 for now).
-- ─────────────────────────────────────────────────────────────────────────
CREATE TABLE wallets (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    address         VARCHAR(56) NOT NULL UNIQUE,           -- Stellar G-addr (always 56 chars)
    network         VARCHAR(30) NOT NULL DEFAULT 'stellar:testnet',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login_at   TIMESTAMPTZ
);
CREATE INDEX idx_wallets_addr ON wallets(address);

-- ─────────────────────────────────────────────────────────────────────────
-- 2. SEP-10 CHALLENGES — short-lived auth challenges. One row per
--    /auth/challenge call. Marked used after a successful /auth/token.
--    Cleaned up periodically (or just left to expire).
-- ─────────────────────────────────────────────────────────────────────────
CREATE TABLE sep10_challenges (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    address         VARCHAR(56) NOT NULL,
    transaction_xdr TEXT NOT NULL,                          -- unsigned challenge tx XDR
    nonce           VARCHAR(128) NOT NULL,                  -- base64 random in manage_data op
    expires_at      TIMESTAMPTZ NOT NULL,                   -- typically NOW() + 5 min
    used_at         TIMESTAMPTZ,                            -- non-null once consumed
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_sep10_addr ON sep10_challenges(address);
CREATE INDEX idx_sep10_pending_expires ON sep10_challenges(expires_at) WHERE used_at IS NULL;

-- ─────────────────────────────────────────────────────────────────────────
-- 3. x402 PRICING — operator-configurable price per endpoint, per org.
--    Drives the public catalog and the verification middleware.
--    Precision matches Stellar's USDC native precision (7 decimals).
-- ─────────────────────────────────────────────────────────────────────────
CREATE TABLE x402_pricing (
    id              BIGSERIAL PRIMARY KEY,
    org_id          BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    endpoint        VARCHAR(100) NOT NULL,                  -- e.g. "posting_heatmap"
    price_usdc      NUMERIC(12,7) NOT NULL CHECK (price_usdc >= 0),
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(org_id, endpoint)
);
CREATE INDEX idx_x402_pricing_org_enabled ON x402_pricing(org_id) WHERE enabled = TRUE;

-- ─────────────────────────────────────────────────────────────────────────
-- 4. AGENT CALLS — append-only log of every x402-paid call. Drives the
--    live agent feed (SSE), revenue dashboard, and call-detail modal.
--    payer_address is the agent's wallet, NOT a FK to wallets — paying
--    agents don't need to register, the wallet IS the identity.
-- ─────────────────────────────────────────────────────────────────────────
CREATE TABLE agent_calls (
    id              BIGSERIAL PRIMARY KEY,
    org_id          BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    payer_address   VARCHAR(56) NOT NULL,                   -- agent's Stellar wallet
    endpoint        VARCHAR(100) NOT NULL,
    request_input   JSONB NOT NULL DEFAULT '{}',            -- what the agent asked for
    response_size   INTEGER,                                 -- bytes served (NULL = error)
    price_usdc      NUMERIC(12,7) NOT NULL,
    tx_hash         VARCHAR(64),                             -- Stellar tx hash, NULL in dry-run
    facilitator     VARCHAR(50) NOT NULL,                    -- 'openzeppelin' | 'dry-run'
    reasoning       TEXT,                                    -- agent reasoning if MCP forwarded it
    client_id       VARCHAR(100),                            -- e.g. 'claude-code', 'mcp:stellarflow'
    status          VARCHAR(20) NOT NULL DEFAULT 'paid',     -- paid|failed|refunded
    error_message   TEXT,
    duration_ms     INTEGER,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_agent_calls_org_time ON agent_calls(org_id, created_at DESC);
CREATE INDEX idx_agent_calls_payer ON agent_calls(payer_address);
CREATE INDEX idx_agent_calls_endpoint_time ON agent_calls(endpoint, created_at DESC);
