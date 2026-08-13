// sponsor.go — operator-side wallet provisioning for new agents.
//
// When a new agent wants to start paying x402-priced endpoints, it needs a
// funded Stellar account with a USDC trustline. Bootstrapping that on
// Testnet Friendbot handles account activation and provides free XLM.
//
// This package gives the operator a one-call helper that funds a freshly
// generated agent G-address with just enough XLM to be operational:
//   - 1 XLM base reserve (Stellar account minimum)
//   - 0.5 XLM trustline reserve (so the agent can establish its USDC trustline)
//   - ~0.5 XLM operating buffer for tx fees
//
// The operator pays this XLM out of their own treasury wallet. For the
// hackathon demo, the same wallet that receives x402 payments also acts as
// the sponsor — a single "operator hot wallet" with its S-key in
// backend/.env. Production should split these into separate wallets with
// HSM/KMS-managed keys.

package stellar

import (
	"context"
	"errors"
	"fmt"

	"github.com/stellar/go/clients/horizonclient"
	"github.com/stellar/go/keypair"
	"github.com/stellar/go/network"
	"github.com/stellar/go/protocols/horizon"
	"github.com/stellar/go/strkey"
	"github.com/stellar/go/txnbuild"
)

// SponsorAmountXLM is the XLM amount funded into each new agent account.
// 2 XLM covers the 1 XLM base reserve + 0.5 XLM trustline reserve + 0.5 XLM
// operating buffer for tx fees and a small margin.
const SponsorAmountXLM = "2"

// Sponsor handles funding of fresh agent accounts on Stellar testnet.
// One Sponsor instance is created at backend startup from the operator's
// treasury keypair and reused across all sponsoring requests.
type Sponsor struct {
	treasuryKP        *keypair.Full
	networkPassphrase string
	client            *horizonclient.Client
}

// NewSponsor builds a Sponsor from the treasury secret key. Returns an
// error if the secret is invalid, the network passphrase is unknown, or
// the secret doesn't decode as a Stellar S-key.
//
// networkPassphrase must be exactly one of:
//   - network.TestNetworkPassphrase    (testnet)
func NewSponsor(treasurySecret, networkPassphrase string) (*Sponsor, error) {
	if treasurySecret == "" {
		return nil, errors.New("treasury secret is empty")
	}
	parsed, err := keypair.Parse(treasurySecret)
	if err != nil {
		return nil, fmt.Errorf("invalid treasury secret: %w", err)
	}
	full, ok := parsed.(*keypair.Full)
	if !ok {
		return nil, errors.New("treasury secret must be an S-key (full keypair), not a G-address")
	}

	if networkPassphrase != network.TestNetworkPassphrase {
		return nil, fmt.Errorf("unsupported network passphrase: %q", networkPassphrase)
	}
	client := horizonclient.DefaultTestNetClient

	return &Sponsor{
		treasuryKP:        full,
		networkPassphrase: networkPassphrase,
		client:            client,
	}, nil
}

// TreasuryAddress returns the public G-address of the operator treasury.
// Useful for sanity checks ("the address my code signs for matches the
// X402_TREASURY_ADDRESS configured for receiving payments").
func (s *Sponsor) TreasuryAddress() string {
	return s.treasuryKP.Address()
}

// SponsorAgent funds a fresh agent G-address with SponsorAmountXLM of XLM
// via a single CreateAccount operation signed by the treasury.
//
// Behavior:
//   - If targetAddr is not a valid 56-char G-address, returns an error.
//   - If the account already exists on the network, returns ("", nil) —
//     idempotent: nothing to do.
//   - Otherwise builds, signs, and submits a CreateAccount transaction.
//     Returns the Horizon tx hash on success.
//
// Note: this does NOT establish the USDC trustline on the new account.
// Trustlines must be authorized by the account holder, so the agent
// itself signs the ChangeTrust op separately (the MCP server's `init`
// command does this client-side after receiving the create-account
// tx hash from this endpoint).
func (s *Sponsor) SponsorAgent(ctx context.Context, targetAddr string) (string, error) {
	if !strkey.IsValidEd25519PublicKey(targetAddr) {
		return "", fmt.Errorf("invalid agent G-address: %q", targetAddr)
	}

	// Idempotency: if the account already exists, this is a no-op success.
	exists, err := s.accountExists(ctx, targetAddr)
	if err != nil {
		return "", fmt.Errorf("check existing account: %w", err)
	}
	if exists {
		return "", nil
	}

	// Load the treasury account to get its current sequence number.
	treasuryAccount, err := s.client.AccountDetail(horizonclient.AccountRequest{
		AccountID: s.treasuryKP.Address(),
	})
	if err != nil {
		return "", fmt.Errorf("load treasury account: %w", err)
	}

	createOp := &txnbuild.CreateAccount{
		Destination:   targetAddr,
		Amount:        SponsorAmountXLM,
		SourceAccount: s.treasuryKP.Address(),
	}

	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &treasuryAccount,
		IncrementSequenceNum: true,
		Operations:           []txnbuild.Operation{createOp},
		BaseFee:              txnbuild.MinBaseFee * 10, // 1000 stroops, ~0.0001 XLM, plenty of priority
		Preconditions: txnbuild.Preconditions{
			TimeBounds: txnbuild.NewTimeout(120),
		},
	})
	if err != nil {
		return "", fmt.Errorf("build create-account tx: %w", err)
	}

	signedTx, err := tx.Sign(s.networkPassphrase, s.treasuryKP)
	if err != nil {
		return "", fmt.Errorf("sign create-account tx: %w", err)
	}

	resp, err := s.client.SubmitTransaction(signedTx)
	if err != nil {
		// Horizon often wraps the actual reason in the result_codes, surface them.
		return "", wrapHorizonErr("submit create-account tx", err)
	}

	return resp.Hash, nil
}

// accountExists returns true if Horizon already knows about the account.
// Used for idempotency: if a developer calls /api/agent/sponsor twice with
// the same address, the second call is a no-op.
func (s *Sponsor) accountExists(_ context.Context, addr string) (bool, error) {
	_, err := s.client.AccountDetail(horizonclient.AccountRequest{AccountID: addr})
	if err == nil {
		return true, nil
	}
	if hErr, ok := err.(*horizonclient.Error); ok && hErr.Response != nil && hErr.Response.StatusCode == 404 {
		return false, nil
	}
	return false, err
}

// TreasuryBalance returns the operator treasury's current XLM balance as a
// decimal string ("69.0000000"). Used by the /api/agent/sponsor handler to
// short-circuit and return a clear error if the operator is out of XLM,
// instead of letting Horizon return an opaque "underfunded" failure.
func (s *Sponsor) TreasuryBalance(_ context.Context) (string, error) {
	acc, err := s.client.AccountDetail(horizonclient.AccountRequest{
		AccountID: s.treasuryKP.Address(),
	})
	if err != nil {
		return "", fmt.Errorf("load treasury account: %w", err)
	}
	for _, b := range acc.Balances {
		if b.Asset.Type == "native" {
			return b.Balance, nil
		}
	}
	return "0", nil
}

// TransferUSDC sends a USDC payment from the operator treasury to a target
// account. Used by the local `cmd/fund-wallet` admin tool to fund test
// wallets or our own demo agent wallet for video recording. NOT exposed via
// any HTTP endpoint — strictly a local operator command.
//
// Constraints:
//   - The target account must already exist on the network and have a USDC
//     trustline established. The function will return a clear error if not.
//   - amount is a decimal string in USDC (e.g. "0.50", "1.0000000").
//   - usdcIssuerAddr is the classic Circle issuer (G-address), not the SAC.
//
// Returns the Horizon tx hash on success.
func (s *Sponsor) TransferUSDC(_ context.Context, targetAddr, amount, usdcIssuerAddr string) (string, error) {
	if !strkey.IsValidEd25519PublicKey(targetAddr) {
		return "", fmt.Errorf("invalid target G-address: %q", targetAddr)
	}
	if !strkey.IsValidEd25519PublicKey(usdcIssuerAddr) {
		return "", fmt.Errorf("invalid USDC issuer G-address: %q", usdcIssuerAddr)
	}
	if amount == "" {
		return "", errors.New("amount is required")
	}

	// Refuse if target account doesn't exist (cleaner error than letting
	// the tx fail at submit time).
	exists, err := s.accountExists(nil, targetAddr)
	if err != nil {
		return "", fmt.Errorf("check target account: %w", err)
	}
	if !exists {
		return "", fmt.Errorf("target account %s does not exist on the network — run x402-mcp init first or use SponsorAgent", targetAddr)
	}

	// Load treasury account for the sequence number.
	treasuryAccount, err := s.client.AccountDetail(horizonclient.AccountRequest{
		AccountID: s.treasuryKP.Address(),
	})
	if err != nil {
		return "", fmt.Errorf("load treasury account: %w", err)
	}

	usdcAsset := txnbuild.CreditAsset{Code: "USDC", Issuer: usdcIssuerAddr}

	payOp := &txnbuild.Payment{
		Destination:   targetAddr,
		Amount:        amount,
		Asset:         usdcAsset,
		SourceAccount: s.treasuryKP.Address(),
	}

	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &treasuryAccount,
		IncrementSequenceNum: true,
		Operations:           []txnbuild.Operation{payOp},
		BaseFee:              txnbuild.MinBaseFee * 10,
		Preconditions: txnbuild.Preconditions{
			TimeBounds: txnbuild.NewTimeout(120),
		},
	})
	if err != nil {
		return "", fmt.Errorf("build payment tx: %w", err)
	}

	signedTx, err := tx.Sign(s.networkPassphrase, s.treasuryKP)
	if err != nil {
		return "", fmt.Errorf("sign payment tx: %w", err)
	}

	resp, err := s.client.SubmitTransaction(signedTx)
	if err != nil {
		return "", wrapHorizonErr("submit payment tx", err)
	}

	return resp.Hash, nil
}

// wrapHorizonErr extracts the result_codes from a Horizon error if present
// and returns a friendly error message. Otherwise wraps the original error.
func wrapHorizonErr(prefix string, err error) error {
	if hErr, ok := err.(*horizonclient.Error); ok {
		if rc, rcErr := hErr.ResultCodes(); rcErr == nil {
			return fmt.Errorf("%s: horizon rejected (tx=%s, ops=%v)", prefix, rc.TransactionCode, rc.OperationCodes)
		}
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

// Compile-time check that horizon.Account satisfies the txnbuild needs —
// keeps the import honest if we ever stop using it directly.
var _ horizon.Account
