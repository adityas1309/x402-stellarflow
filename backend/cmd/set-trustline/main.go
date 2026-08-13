// set-trustline — one-shot utility to establish a USDC trustline on the
// operator treasury. Must be run ONCE after generating a fresh treasury
// wallet, before the treasury can receive x402 USDC payments.
//
// Reads the secret key from X402_TREASURY_SECRET and the USDC classic
// issuer from X402_USDC_ISSUER (or defaults to Stellar testnet).
//
// Usage:
//   cd backend
//   go run ./cmd/set-trustline
//
// Idempotent: if the trustline already exists, Stellar returns
// op_already_exists and we exit 0 with a friendly message.
package main

import (
	"fmt"
	"os"

	"github.com/stellar/go/clients/horizonclient"
	"github.com/stellar/go/keypair"
	"github.com/stellar/go/network"
	"github.com/stellar/go/txnbuild"
)

func main() {
	secret := os.Getenv("X402_TREASURY_SECRET")
	if secret == "" {
		fmt.Fprintln(os.Stderr, "error: X402_TREASURY_SECRET not set")
		os.Exit(1)
	}

	issuer := os.Getenv("X402_USDC_ISSUER")
	if issuer == "" {
		// Stellar testnet USDC issuer
		issuer = "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5"
	}

	netPassphrase := network.TestNetworkPassphrase

	kp, err := keypair.ParseFull(secret)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid secret key: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Treasury: %s\n", kp.Address())
	fmt.Printf("Network:  %s\n", netPassphrase)
	fmt.Printf("Asset:    USDC issued by %s\n", issuer)
	fmt.Println()

	client := horizonclient.DefaultTestNetClient

	// Load the source account
	acc, err := client.AccountDetail(horizonclient.AccountRequest{AccountID: kp.Address()})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to load treasury account: %v\n", err)
		os.Exit(1)
	}

	// Check if the trustline already exists
	for _, b := range acc.Balances {
		if b.Asset.Type == "credit_alphanum4" && b.Asset.Code == "USDC" && b.Asset.Issuer == issuer {
			fmt.Printf("✓ USDC trustline already exists (balance: %s)\n", b.Balance)
			return
		}
	}

	// Build the ChangeTrust transaction
	usdcAsset := txnbuild.CreditAsset{Code: "USDC", Issuer: issuer}
	changeTrustAsset, err := usdcAsset.ToChangeTrustAsset()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: asset conversion: %v\n", err)
		os.Exit(1)
	}

	op := &txnbuild.ChangeTrust{
		Line: changeTrustAsset,
		// Default limit (unlimited)
	}

	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &acc,
		IncrementSequenceNum: true,
		Operations:           []txnbuild.Operation{op},
		BaseFee:              txnbuild.MinBaseFee * 10,
		Preconditions: txnbuild.Preconditions{
			TimeBounds: txnbuild.NewTimeout(120),
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: build tx: %v\n", err)
		os.Exit(1)
	}

	signed, err := tx.Sign(netPassphrase, kp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: sign tx: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Submitting ChangeTrust to Horizon...")
	resp, err := client.SubmitTransaction(signed)
	if err != nil {
		// Check if it's "already exists" — idempotency
		if hErr, ok := err.(*horizonclient.Error); ok {
			if codes, codeErr := hErr.ResultCodes(); codeErr == nil {
				for _, opCode := range codes.OperationCodes {
					if opCode == "op_already_exists" {
						fmt.Println("✓ Trustline already exists (idempotent success)")
						return
					}
				}
			}
		}
		fmt.Fprintf(os.Stderr, "error: submit tx: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Trustline established. tx: %s\n", resp.Hash)
	fmt.Printf("  https://stellarchain.io/transactions/%s\n", resp.Hash)
}
