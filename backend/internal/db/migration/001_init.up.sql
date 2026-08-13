-- Initial schema for the stellarflow wire + StellarFlow example.
--
-- The wire-only tables are:
--   - organizations / users / refresh_tokens     (auth + multi-tenant)
--   - operation_costs / usage_log                 (per-call accounting)
--   - credit_transactions                          (audit log)
--
-- The example tables (delete these when forking the template for your
-- own example):
--   - competitors / competitor_metrics / metrics_history
--
-- The Stellar wallet + x402 payment tables are added in migration
-- 010_stellar_x402.

-- Organizations (multi-tenant / white-label).
CREATE TABLE organizations (
    id          BIGSERIAL PRIMARY KEY,
    name        VARCHAR(200) NOT NULL,
    slug        VARCHAR(100) NOT NULL UNIQUE,
    logo_url    VARCHAR(500),
    plan        VARCHAR(30) NOT NULL DEFAULT 'starter',
    credits     INTEGER NOT NULL DEFAULT 5,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

-- Users belong to an organization. Roles: owner | member.
-- is_super_admin is unused in the hackathon flow (no admin panel) but kept
-- for compatibility with the existing token payload + middleware.
CREATE TABLE users (
    id              BIGSERIAL PRIMARY KEY,
    org_id          BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email           VARCHAR(200) NOT NULL UNIQUE,
    hashed_password VARCHAR(200) NOT NULL,
    full_name       VARCHAR(200) NOT NULL,
    role            VARCHAR(30) NOT NULL DEFAULT 'member',
    is_super_admin  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX idx_users_org ON users(org_id);

-- Tracked competitor accounts ("Tracked Brands" in the hackathon UI).
CREATE TABLE competitors (
    id              BIGSERIAL PRIMARY KEY,
    org_id          BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    platform        VARCHAR(20) NOT NULL CHECK (platform IN ('instagram','tiktok')),
    username        VARCHAR(100) NOT NULL,
    display_name    VARCHAR(200),
    profile_pic_url VARCHAR(500),
    is_own_account  BOOLEAN NOT NULL DEFAULT FALSE,
    scrape_status   VARCHAR(30) NOT NULL DEFAULT 'pending',
    scrape_error    TEXT,
    last_scraped_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(org_id, platform, username)
);
CREATE INDEX idx_competitors_org ON competitors(org_id);
CREATE INDEX idx_competitors_scrape ON competitors(scrape_status) WHERE scrape_status IN ('pending','failed');

-- Scraped metrics snapshot (one row per competitor, upserted by the mock scraper).
CREATE TABLE competitor_metrics (
    id                  BIGSERIAL PRIMARY KEY,
    competitor_id       BIGINT NOT NULL UNIQUE REFERENCES competitors(id) ON DELETE CASCADE,
    followers_count     INTEGER DEFAULT 0,
    following_count     INTEGER DEFAULT 0,
    posts_count         INTEGER DEFAULT 0,
    total_likes         BIGINT DEFAULT 0,
    engagement_rate     BIGINT DEFAULT 0, -- basis points (250 = 2.50%)
    is_verified         BOOLEAN DEFAULT FALSE,
    posts_per_week      NUMERIC(5,2) DEFAULT 0,
    bio                 TEXT,
    website             VARCHAR(500),
    raw_profile_data    JSONB NOT NULL DEFAULT '{}',
    raw_posts_data      JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ DEFAULT NOW(),
    updated_at          TIMESTAMPTZ DEFAULT NOW()
);

-- Historical metrics for trend charts.
CREATE TABLE metrics_history (
    id              BIGSERIAL PRIMARY KEY,
    competitor_id   BIGINT NOT NULL REFERENCES competitors(id) ON DELETE CASCADE,
    followers_count INTEGER NOT NULL,
    engagement_rate BIGINT NOT NULL,
    posts_count     INTEGER NOT NULL,
    scraped_at      TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX idx_metrics_history_comp ON metrics_history(competitor_id, scraped_at DESC);

-- Credit transaction log (audit trail).
CREATE TABLE credit_transactions (
    id          BIGSERIAL PRIMARY KEY,
    org_id      BIGINT NOT NULL REFERENCES organizations(id),
    user_id     BIGINT NOT NULL REFERENCES users(id),
    report_id   BIGINT, -- legacy column, no FK (reports table is gone)
    amount      INTEGER NOT NULL,
    balance     INTEGER NOT NULL,
    description TEXT,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX idx_credit_tx_org ON credit_transactions(org_id, created_at DESC);

-- Refresh tokens for the PASETO auth flow (issued by SEP-10).
CREATE TABLE refresh_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  VARCHAR(200) NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

-- Operation cost configuration (the mock-scraper still consumes credits
-- when it runs, just for parity with the production flow).
CREATE TABLE operation_costs (
    id            BIGSERIAL PRIMARY KEY,
    operation     VARCHAR(50) NOT NULL UNIQUE,
    credit_cost   INTEGER NOT NULL DEFAULT 1,
    is_enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    description   TEXT,
    updated_at    TIMESTAMPTZ DEFAULT NOW()
);
INSERT INTO operation_costs (operation, credit_cost, description) VALUES
    ('scrape_instagram', 1, 'Mock scraper run for an Instagram brand'),
    ('scrape_tiktok',    1, 'Mock scraper run for a TikTok brand');

-- Usage log (one row per scrape job; legacy report_id column kept nullable).
CREATE TABLE usage_log (
    id              BIGSERIAL PRIMARY KEY,
    org_id          BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id         BIGINT,
    operation       VARCHAR(50) NOT NULL,
    credit_cost     INTEGER NOT NULL,
    status          VARCHAR(20) NOT NULL DEFAULT 'completed',
    competitor_id   BIGINT REFERENCES competitors(id) ON DELETE SET NULL,
    report_id       BIGINT, -- legacy column, no FK
    metadata        JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX idx_usage_log_org ON usage_log(org_id, created_at DESC);
