package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// ---- Wallet types ----

// Wallet links a Stellar account to a StellarFlow user. 1:1 for now — a wallet
// belongs to exactly one user. Created on first SEP-10 sign-in.
type Wallet struct {
	ID          int64        `json:"id"`
	UserID      int64        `json:"user_id"`
	Address     string       `json:"address"`
	Network     string       `json:"network"`
	CreatedAt   time.Time    `json:"created_at"`
	LastLoginAt sql.NullTime `json:"last_login_at"`
}

type CreateWalletParams struct {
	UserID  int64
	Address string
	Network string
}

func (s *Store) CreateWallet(ctx context.Context, arg CreateWalletParams) (Wallet, error) {
	var w Wallet
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO wallets (user_id, address, network)
		 VALUES ($1, $2, $3)
		 RETURNING id, user_id, address, network, created_at, last_login_at`,
		arg.UserID, arg.Address, arg.Network,
	)
	err := row.Scan(&w.ID, &w.UserID, &w.Address, &w.Network, &w.CreatedAt, &w.LastLoginAt)
	return w, err
}

func (s *Store) GetWalletByAddress(ctx context.Context, address string) (Wallet, error) {
	var w Wallet
	row := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, address, network, created_at, last_login_at
		 FROM wallets WHERE address = $1`,
		address,
	)
	err := row.Scan(&w.ID, &w.UserID, &w.Address, &w.Network, &w.CreatedAt, &w.LastLoginAt)
	return w, err
}

func (s *Store) GetWalletByUserID(ctx context.Context, userID int64) (Wallet, error) {
	var w Wallet
	row := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, address, network, created_at, last_login_at
		 FROM wallets WHERE user_id = $1`,
		userID,
	)
	err := row.Scan(&w.ID, &w.UserID, &w.Address, &w.Network, &w.CreatedAt, &w.LastLoginAt)
	return w, err
}

func (s *Store) TouchWalletLogin(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE wallets SET last_login_at = NOW() WHERE id = $1`,
		id,
	)
	return err
}

// ---- SEP-10 Challenge types ----

// Sep10Challenge is a short-lived auth challenge issued by GET /auth/challenge.
// The client signs the transaction_xdr with their wallet and posts it back to
// /auth/token, where the server verifies the signature and marks the challenge
// as used.
type Sep10Challenge struct {
	ID             uuid.UUID    `json:"id"`
	Address        string       `json:"address"`
	TransactionXDR string       `json:"transaction_xdr"`
	Nonce          string       `json:"nonce"`
	ExpiresAt      time.Time    `json:"expires_at"`
	UsedAt         sql.NullTime `json:"used_at"`
	CreatedAt      time.Time    `json:"created_at"`
}

type CreateSep10ChallengeParams struct {
	Address        string
	TransactionXDR string
	Nonce          string
	ExpiresAt      time.Time
}

func (s *Store) CreateSep10Challenge(ctx context.Context, arg CreateSep10ChallengeParams) (Sep10Challenge, error) {
	var c Sep10Challenge
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO sep10_challenges (address, transaction_xdr, nonce, expires_at)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, address, transaction_xdr, nonce, expires_at, used_at, created_at`,
		arg.Address, arg.TransactionXDR, arg.Nonce, arg.ExpiresAt,
	)
	err := row.Scan(&c.ID, &c.Address, &c.TransactionXDR, &c.Nonce, &c.ExpiresAt, &c.UsedAt, &c.CreatedAt)
	return c, err
}

func (s *Store) GetSep10ChallengeByID(ctx context.Context, id uuid.UUID) (Sep10Challenge, error) {
	var c Sep10Challenge
	row := s.db.QueryRowContext(ctx,
		`SELECT id, address, transaction_xdr, nonce, expires_at, used_at, created_at
		 FROM sep10_challenges WHERE id = $1`,
		id,
	)
	err := row.Scan(&c.ID, &c.Address, &c.TransactionXDR, &c.Nonce, &c.ExpiresAt, &c.UsedAt, &c.CreatedAt)
	return c, err
}

// MarkSep10ChallengeUsed atomically marks a challenge as used. Returns nil
// even if the challenge was already used (idempotent), but callers should
// check used_at separately if they need to reject replay attacks.
func (s *Store) MarkSep10ChallengeUsed(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sep10_challenges SET used_at = NOW()
		 WHERE id = $1 AND used_at IS NULL`,
		id,
	)
	return err
}

// DeleteExpiredSep10Challenges cleans up unused challenges past their TTL.
// Safe to call from a periodic janitor goroutine.
func (s *Store) DeleteExpiredSep10Challenges(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM sep10_challenges WHERE expires_at < NOW() AND used_at IS NULL`,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
