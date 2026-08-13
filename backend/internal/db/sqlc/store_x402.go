package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// ---- x402 Pricing types ----

// X402Pricing is the operator-configurable price for an x402 endpoint.
// One row per (org_id, endpoint) combination.
type X402Pricing struct {
	ID        int64     `json:"id"`
	OrgID     int64     `json:"org_id"`
	Endpoint  string    `json:"endpoint"`
	PriceUSDC float64   `json:"price_usdc"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type UpsertX402PricingParams struct {
	OrgID     int64
	Endpoint  string
	PriceUSDC float64
	Enabled   bool
}

func (s *Store) ListX402PricingByOrg(ctx context.Context, orgID int64) ([]X402Pricing, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, endpoint, price_usdc, enabled, created_at, updated_at
		 FROM x402_pricing WHERE org_id = $1 ORDER BY endpoint`,
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []X402Pricing
	for rows.Next() {
		var p X402Pricing
		if err := rows.Scan(&p.ID, &p.OrgID, &p.Endpoint, &p.PriceUSDC, &p.Enabled, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ListEnabledX402PricingByOrg(ctx context.Context, orgID int64) ([]X402Pricing, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, endpoint, price_usdc, enabled, created_at, updated_at
		 FROM x402_pricing WHERE org_id = $1 AND enabled = TRUE ORDER BY endpoint`,
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []X402Pricing
	for rows.Next() {
		var p X402Pricing
		if err := rows.Scan(&p.ID, &p.OrgID, &p.Endpoint, &p.PriceUSDC, &p.Enabled, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetX402PricingByOrgEndpoint(ctx context.Context, orgID int64, endpoint string) (X402Pricing, error) {
	var p X402Pricing
	row := s.db.QueryRowContext(ctx,
		`SELECT id, org_id, endpoint, price_usdc, enabled, created_at, updated_at
		 FROM x402_pricing WHERE org_id = $1 AND endpoint = $2`,
		orgID, endpoint,
	)
	err := row.Scan(&p.ID, &p.OrgID, &p.Endpoint, &p.PriceUSDC, &p.Enabled, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

func (s *Store) UpsertX402Pricing(ctx context.Context, arg UpsertX402PricingParams) (X402Pricing, error) {
	var p X402Pricing
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO x402_pricing (org_id, endpoint, price_usdc, enabled)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (org_id, endpoint) DO UPDATE
		 SET price_usdc = EXCLUDED.price_usdc,
		     enabled    = EXCLUDED.enabled,
		     updated_at = NOW()
		 RETURNING id, org_id, endpoint, price_usdc, enabled, created_at, updated_at`,
		arg.OrgID, arg.Endpoint, arg.PriceUSDC, arg.Enabled,
	)
	err := row.Scan(&p.ID, &p.OrgID, &p.Endpoint, &p.PriceUSDC, &p.Enabled, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

func (s *Store) DeleteX402Pricing(ctx context.Context, orgID int64, endpoint string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM x402_pricing WHERE org_id = $1 AND endpoint = $2`,
		orgID, endpoint,
	)
	return err
}

// ---- Agent Calls types ----

// AgentCall is one row in the append-only log of paid x402 calls.
// Drives the live agent feed and the operator revenue dashboard.
type AgentCall struct {
	ID            int64           `json:"id"`
	OrgID         int64           `json:"org_id"`
	PayerAddress  string          `json:"payer_address"`
	Endpoint      string          `json:"endpoint"`
	RequestInput  json.RawMessage `json:"request_input"`
	ResponseSize  sql.NullInt32   `json:"response_size"`
	PriceUSDC     float64         `json:"price_usdc"`
	TxHash        sql.NullString  `json:"tx_hash"`
	Facilitator   string          `json:"facilitator"`
	Reasoning     sql.NullString  `json:"reasoning"`
	ClientID      sql.NullString  `json:"client_id"`
	Status        string          `json:"status"`
	ErrorMessage  sql.NullString  `json:"error_message"`
	DurationMs    sql.NullInt32   `json:"duration_ms"`
	CreatedAt     time.Time       `json:"created_at"`
}

type CreateAgentCallParams struct {
	OrgID        int64
	PayerAddress string
	Endpoint     string
	RequestInput json.RawMessage
	ResponseSize sql.NullInt32
	PriceUSDC    float64
	TxHash       sql.NullString
	Facilitator  string
	Reasoning    sql.NullString
	ClientID     sql.NullString
	Status       string
	ErrorMessage sql.NullString
	DurationMs   sql.NullInt32
}

func (s *Store) CreateAgentCall(ctx context.Context, arg CreateAgentCallParams) (AgentCall, error) {
	if arg.RequestInput == nil {
		arg.RequestInput = json.RawMessage(`{}`)
	}
	if arg.Status == "" {
		arg.Status = "paid"
	}

	var a AgentCall
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO agent_calls (
		    org_id, payer_address, endpoint, request_input, response_size,
		    price_usdc, tx_hash, facilitator, reasoning, client_id,
		    status, error_message, duration_ms
		 )
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		 RETURNING id, org_id, payer_address, endpoint, request_input, response_size,
		           price_usdc, tx_hash, facilitator, reasoning, client_id,
		           status, error_message, duration_ms, created_at`,
		arg.OrgID, arg.PayerAddress, arg.Endpoint, arg.RequestInput, arg.ResponseSize,
		arg.PriceUSDC, arg.TxHash, arg.Facilitator, arg.Reasoning, arg.ClientID,
		arg.Status, arg.ErrorMessage, arg.DurationMs,
	)
	err := row.Scan(
		&a.ID, &a.OrgID, &a.PayerAddress, &a.Endpoint, &a.RequestInput, &a.ResponseSize,
		&a.PriceUSDC, &a.TxHash, &a.Facilitator, &a.Reasoning, &a.ClientID,
		&a.Status, &a.ErrorMessage, &a.DurationMs, &a.CreatedAt,
	)
	return a, err
}

func (s *Store) GetAgentCallByID(ctx context.Context, id int64) (AgentCall, error) {
	var a AgentCall
	row := s.db.QueryRowContext(ctx,
		`SELECT id, org_id, payer_address, endpoint, request_input, response_size,
		        price_usdc, tx_hash, facilitator, reasoning, client_id,
		        status, error_message, duration_ms, created_at
		 FROM agent_calls WHERE id = $1`,
		id,
	)
	err := row.Scan(
		&a.ID, &a.OrgID, &a.PayerAddress, &a.Endpoint, &a.RequestInput, &a.ResponseSize,
		&a.PriceUSDC, &a.TxHash, &a.Facilitator, &a.Reasoning, &a.ClientID,
		&a.Status, &a.ErrorMessage, &a.DurationMs, &a.CreatedAt,
	)
	return a, err
}

func (s *Store) ListRecentAgentCallsByOrg(ctx context.Context, orgID int64, limit int32) ([]AgentCall, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, payer_address, endpoint, request_input, response_size,
		        price_usdc, tx_hash, facilitator, reasoning, client_id,
		        status, error_message, duration_ms, created_at
		 FROM agent_calls WHERE org_id = $1
		 ORDER BY created_at DESC LIMIT $2`,
		orgID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanAgentCalls(rows)
}

func (s *Store) ListAgentCallsByOrgSince(ctx context.Context, orgID int64, since time.Time) ([]AgentCall, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, payer_address, endpoint, request_input, response_size,
		        price_usdc, tx_hash, facilitator, reasoning, client_id,
		        status, error_message, duration_ms, created_at
		 FROM agent_calls WHERE org_id = $1 AND created_at > $2
		 ORDER BY created_at DESC`,
		orgID, since,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanAgentCalls(rows)
}

// ListRecentAgentCallsByPayer returns the most recent calls SCOPED to a
// single payer wallet. Used by the per-agent dashboard at /wallet/:address
// — the agent dev sees only their own activity.
func (s *Store) ListRecentAgentCallsByPayer(ctx context.Context, payerAddress string, limit int32) ([]AgentCall, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, payer_address, endpoint, request_input, response_size,
		        price_usdc, tx_hash, facilitator, reasoning, client_id,
		        status, error_message, duration_ms, created_at
		 FROM agent_calls WHERE payer_address = $1
		 ORDER BY created_at DESC LIMIT $2`,
		payerAddress, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanAgentCalls(rows)
}

// AgentUsageSummary aggregates one wallet's spending across all paid calls.
type AgentUsageSummary struct {
	TotalCalls   int64   `json:"total_calls"`
	TotalUSDC    float64 `json:"total_usdc"`
	FirstSeenAt  *time.Time `json:"first_seen_at,omitempty"`
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`
}

// GetAgentUsageSummary returns aggregate stats for a single payer wallet.
// Used as the headline numbers on the agent's own dashboard.
func (s *Store) GetAgentUsageSummary(ctx context.Context, payerAddress string) (AgentUsageSummary, error) {
	var r AgentUsageSummary
	row := s.db.QueryRowContext(ctx,
		`SELECT
		    COUNT(*)                                  AS total_calls,
		    COALESCE(SUM(price_usdc), 0)::FLOAT8      AS total_usdc,
		    MIN(created_at)                           AS first_seen_at,
		    MAX(created_at)                           AS last_seen_at
		 FROM agent_calls
		 WHERE payer_address = $1 AND status = 'paid'`,
		payerAddress,
	)
	var firstSeen, lastSeen sql.NullTime
	err := row.Scan(&r.TotalCalls, &r.TotalUSDC, &firstSeen, &lastSeen)
	if err != nil {
		return r, err
	}
	if firstSeen.Valid {
		r.FirstSeenAt = &firstSeen.Time
	}
	if lastSeen.Valid {
		r.LastSeenAt = &lastSeen.Time
	}
	return r, nil
}

// AgentEndpointBreakdown is one row of the per-agent endpoint breakdown.
type AgentEndpointBreakdown struct {
	Endpoint  string  `json:"endpoint"`
	CallCount int64   `json:"call_count"`
	TotalUSDC float64 `json:"total_usdc"`
}

// GetAgentEndpointBreakdown returns the per-endpoint distribution of one
// payer's spending. Used to render the "what did I spend on" section of
// the agent dashboard.
func (s *Store) GetAgentEndpointBreakdown(ctx context.Context, payerAddress string) ([]AgentEndpointBreakdown, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT
		    endpoint,
		    COUNT(*)::BIGINT                            AS call_count,
		    COALESCE(SUM(price_usdc), 0)::FLOAT8        AS total_usdc
		 FROM agent_calls
		 WHERE payer_address = $1 AND status = 'paid'
		 GROUP BY endpoint
		 ORDER BY total_usdc DESC`,
		payerAddress,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentEndpointBreakdown
	for rows.Next() {
		var b AgentEndpointBreakdown
		if err := rows.Scan(&b.Endpoint, &b.CallCount, &b.TotalUSDC); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func scanAgentCalls(rows *sql.Rows) ([]AgentCall, error) {
	var out []AgentCall
	for rows.Next() {
		var a AgentCall
		if err := rows.Scan(
			&a.ID, &a.OrgID, &a.PayerAddress, &a.Endpoint, &a.RequestInput, &a.ResponseSize,
			&a.PriceUSDC, &a.TxHash, &a.Facilitator, &a.Reasoning, &a.ClientID,
			&a.Status, &a.ErrorMessage, &a.DurationMs, &a.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- Aggregation queries (for the operator dashboard) ----

// RevenueSummary is the headline number on the operator dashboard.
type RevenueSummary struct {
	TotalUSDC    float64 `json:"total_usdc"`
	TotalCalls   int64   `json:"total_calls"`
	UniqueAgents int64   `json:"unique_agents"`
}

func (s *Store) GetRevenueSummaryByOrg(ctx context.Context, orgID int64, since time.Time) (RevenueSummary, error) {
	var r RevenueSummary
	row := s.db.QueryRowContext(ctx,
		`SELECT
		    COALESCE(SUM(price_usdc), 0)::FLOAT8        AS total_usdc,
		    COUNT(*)                                     AS total_calls,
		    COUNT(DISTINCT payer_address)                AS unique_agents
		 FROM agent_calls
		 WHERE org_id = $1 AND status = 'paid' AND created_at > $2`,
		orgID, since,
	)
	err := row.Scan(&r.TotalUSDC, &r.TotalCalls, &r.UniqueAgents)
	return r, err
}

// EndpointStat is one row of the "top endpoints" leaderboard.
type EndpointStat struct {
	Endpoint    string  `json:"endpoint"`
	CallCount   int64   `json:"call_count"`
	RevenueUSDC float64 `json:"revenue_usdc"`
}

func (s *Store) GetTopEndpointsByOrg(ctx context.Context, orgID int64, since time.Time, limit int32) ([]EndpointStat, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT
		    endpoint,
		    COUNT(*)::BIGINT                             AS call_count,
		    COALESCE(SUM(price_usdc), 0)::FLOAT8         AS revenue_usdc
		 FROM agent_calls
		 WHERE org_id = $1 AND status = 'paid' AND created_at > $2
		 GROUP BY endpoint
		 ORDER BY call_count DESC
		 LIMIT $3`,
		orgID, since, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EndpointStat
	for rows.Next() {
		var e EndpointStat
		if err := rows.Scan(&e.Endpoint, &e.CallCount, &e.RevenueUSDC); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PayerStat is one row of the "top agents" leaderboard.
type PayerStat struct {
	PayerAddress string  `json:"payer_address"`
	CallCount    int64   `json:"call_count"`
	SpendUSDC    float64 `json:"spend_usdc"`
}

func (s *Store) GetTopPayersByOrg(ctx context.Context, orgID int64, since time.Time, limit int32) ([]PayerStat, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT
		    payer_address,
		    COUNT(*)::BIGINT                             AS call_count,
		    COALESCE(SUM(price_usdc), 0)::FLOAT8         AS spend_usdc
		 FROM agent_calls
		 WHERE org_id = $1 AND status = 'paid' AND created_at > $2
		 GROUP BY payer_address
		 ORDER BY spend_usdc DESC
		 LIMIT $3`,
		orgID, since, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PayerStat
	for rows.Next() {
		var p PayerStat
		if err := rows.Scan(&p.PayerAddress, &p.CallCount, &p.SpendUSDC); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
