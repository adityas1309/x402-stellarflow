// Package stellar contains helpers for interacting with the Stellar network:
// SEP-10 challenge generation/verification, address validation, and (future)
// x402 facilitator clients.
package stellar

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/stellar/go/keypair"
	"github.com/stellar/go/network"
	"github.com/stellar/go/strkey"
	"github.com/stellar/go/txnbuild"
	"github.com/stellar/go/xdr"
)

// SEP10 holds the server-side keypair, home/web auth domains, and network
// passphrase used to issue and verify SEP-10-style challenge transactions.
//
// We deliberately implement a minimal subset of SEP-10: a single manage_data
// op signed by the user, with TTL enforced via tx time bounds. This is enough
// to prove the user controls the wallet, which is all we need for operator
// sign-in. Full SEP-10 (multiple ops, web_auth_domain validation, client
// account discovery) is overkill for the hackathon.
type SEP10 struct {
	ServerKP          *keypair.Full
	HomeDomain        string
	WebAuthDomain     string
	NetworkPassphrase string
	ChallengeTimeout  time.Duration
}

// New constructs a SEP10 helper for Stellar testnet.
func New(serverSecretKey, homeDomain, webAuthDomain, networkID string) (*SEP10, error) {
	kp, err := keypair.ParseFull(serverSecretKey)
	if err != nil {
		return nil, fmt.Errorf("parse server secret key: %w", err)
	}

	switch networkID {
	case "stellar:testnet", "testnet":
	default:
		return nil, fmt.Errorf("unsupported network: %q (use stellar:testnet)", networkID)
	}

	if homeDomain == "" {
		return nil, errors.New("home domain is required")
	}
	if webAuthDomain == "" {
		webAuthDomain = homeDomain
	}

	return &SEP10{
		ServerKP:          kp,
		HomeDomain:        homeDomain,
		WebAuthDomain:     webAuthDomain,
		NetworkPassphrase: network.TestNetworkPassphrase,
		ChallengeTimeout:  5 * time.Minute,
	}, nil
}

// IsValidStellarAddress returns true if s is a well-formed Stellar G-address.
func IsValidStellarAddress(s string) bool {
	if len(s) != 56 || !strings.HasPrefix(s, "G") {
		return false
	}
	_, err := strkey.Decode(strkey.VersionByteAccountID, s)
	return err == nil
}

// BuildChallenge constructs an unsigned-by-client SEP-10-style challenge
// transaction targeting the given client account. Returns the base64 XDR of
// the transaction (already signed by the server) and the random nonce that
// was embedded in the manage_data operation.
//
// The transaction has sequence 0 so it can never be submitted on-chain — it
// only exists for the client to sign as proof of wallet ownership.
func (s *SEP10) BuildChallenge(clientAccount string) (txXDR, nonce string, err error) {
	if !IsValidStellarAddress(clientAccount) {
		return "", "", fmt.Errorf("invalid client account: %q", clientAccount)
	}

	// 64 bytes of randomness, base64-encoded → 88 chars (fits in manage_data 64-byte value).
	// SEP-10 spec uses 48 bytes; we use 48 to match.
	raw := make([]byte, 48)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("rand: %w", err)
	}
	nonce = base64.StdEncoding.EncodeToString(raw)

	// The manage_data op is sourced by the CLIENT account so the client must
	// sign the transaction (Stellar requires every op-source to authorize).
	dataKey := s.HomeDomain + " auth"
	if len(dataKey) > 64 {
		return "", "", fmt.Errorf("home domain too long: %q (data key must be ≤ 64 bytes)", s.HomeDomain)
	}

	op := &txnbuild.ManageData{
		SourceAccount: clientAccount,
		Name:          dataKey,
		Value:         []byte(nonce),
	}

	// Server account at sequence 0 — sequence is bumped before submission, so
	// using 0 here means the tx can never reach the network even if it leaks.
	serverAccount := &txnbuild.SimpleAccount{
		AccountID: s.ServerKP.Address(),
		Sequence:  0,
	}

	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        serverAccount,
		IncrementSequenceNum: false,
		Operations:           []txnbuild.Operation{op},
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions: txnbuild.Preconditions{
			TimeBounds: txnbuild.NewTimebounds(time.Now().Unix(), time.Now().Add(s.ChallengeTimeout).Unix()),
		},
	})
	if err != nil {
		return "", "", fmt.Errorf("build tx: %w", err)
	}

	signed, err := tx.Sign(s.NetworkPassphrase, s.ServerKP)
	if err != nil {
		return "", "", fmt.Errorf("sign tx: %w", err)
	}

	xdrStr, err := signed.Base64()
	if err != nil {
		return "", "", fmt.Errorf("encode xdr: %w", err)
	}

	return xdrStr, nonce, nil
}

// VerifyChallenge takes the original unsigned-by-client transaction XDR (as
// returned by BuildChallenge) and the client-signed XDR submitted to
// /auth/token, and confirms:
//
//  1. The two transactions are byte-identical at the envelope level (the
//     client did not tamper with the operations).
//  2. The signed envelope contains a valid signature from clientAccount.
//
// Returns nil on success.
func (s *SEP10) VerifyChallenge(originalXDR, signedXDR, clientAccount string) error {
	if !IsValidStellarAddress(clientAccount) {
		return fmt.Errorf("invalid client account: %q", clientAccount)
	}

	// Parse the signed XDR submitted by the client.
	signedGeneric, err := txnbuild.TransactionFromXDR(signedXDR)
	if err != nil {
		return fmt.Errorf("parse signed xdr: %w", err)
	}
	signedTx, ok := signedGeneric.Transaction()
	if !ok {
		return errors.New("signed xdr is a fee-bump tx, expected plain tx")
	}

	// Parse the original (server-issued) XDR.
	origGeneric, err := txnbuild.TransactionFromXDR(originalXDR)
	if err != nil {
		return fmt.Errorf("parse original xdr: %w", err)
	}
	origTx, ok := origGeneric.Transaction()
	if !ok {
		return errors.New("original xdr is a fee-bump tx, expected plain tx")
	}

	// Compare the tx hashes (without signatures). If equal, the operations,
	// account, sequence, and time bounds are all unchanged.
	origHash, err := origTx.Hash(s.NetworkPassphrase)
	if err != nil {
		return fmt.Errorf("hash original: %w", err)
	}
	signedHash, err := signedTx.Hash(s.NetworkPassphrase)
	if err != nil {
		return fmt.Errorf("hash signed: %w", err)
	}
	if origHash != signedHash {
		return errors.New("signed transaction does not match the issued challenge")
	}

	// The client account is what the manage_data op is sourced by. Verify the
	// signed envelope has a valid signature from this account on the tx hash.
	clientKP, err := keypair.ParseAddress(clientAccount)
	if err != nil {
		return fmt.Errorf("parse client account: %w", err)
	}

	for _, sig := range signedTx.Signatures() {
		if err := clientKP.Verify(signedHash[:], sig.Signature); err == nil {
			// Bonus check: the signature hint should match the client account's last 4 bytes.
			if matchesHint(clientKP, sig.Hint) {
				return nil
			}
		}
	}

	return errors.New("no valid client signature found on challenge")
}

// matchesHint checks that the signature hint corresponds to the given keypair.
// Stellar signature hints are the last 4 bytes of the public key.
func matchesHint(kp *keypair.FromAddress, hint xdr.SignatureHint) bool {
	pubBytes, err := strkey.Decode(strkey.VersionByteAccountID, kp.Address())
	if err != nil || len(pubBytes) < 4 {
		return false
	}
	last4 := pubBytes[len(pubBytes)-4:]
	for i := 0; i < 4; i++ {
		if last4[i] != hint[i] {
			return false
		}
	}
	return true
}
