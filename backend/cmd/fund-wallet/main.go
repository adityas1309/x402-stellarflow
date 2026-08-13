// fund-wallet — local admin tool to send USDC from the operator treasury
// to a target Stellar wallet.
//
// Used for:
//   1. Funding our own demo agent wallet for video recording (one-time)
//   2. Funding any test/demo wallets we want to pre-load with USDC
//
// NOT exposed via any HTTP endpoint. The treasury secret stays in
// backend/.env and is loaded the same way the main API server loads it.
//
// Usage:
//
//   make fund-wallet ADDR=GCJQ... AMOUNT=0.50
//   # or directly:
//   ADDR=GCJQ... AMOUNT=0.50 go run ./cmd/fund-wallet
//
// Pre-conditions on the target wallet:
//   - Account must already exist on Stellar testnet (run x402-mcp init
//     first to provision it via the operator's sponsor endpoint)
//   - Account must have a USDC trustline established (x402-mcp init
//     does this automatically as its second step)
//
// On success, prints the tx hash and a stellarchain.io link.

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/your-org/stellarflow/internal/config"
	"github.com/your-org/stellarflow/internal/stellar"

	"github.com/stellar/go/network"
)

func main() {
	addr := os.Getenv("ADDR")
	amount := os.Getenv("AMOUNT")
	if addr == "" || amount == "" {
		fmt.Fprintln(os.Stderr, "Usage: ADDR=G... AMOUNT=0.50 go run ./cmd/fund-wallet")
		fmt.Fprintln(os.Stderr, "(or via Makefile: make fund-wallet ADDR=G... AMOUNT=0.50)")
		os.Exit(2)
	}

	cfg, err := config.LoadConfig(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	if cfg.X402TreasurySecret == "" {
		fmt.Fprintln(os.Stderr, "X402_TREASURY_SECRET is not set in backend/.env")
		os.Exit(1)
	}

	passphrase := network.TestNetworkPassphrase

	sponsor, err := stellar.NewSponsor(cfg.X402TreasurySecret, passphrase)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init sponsor: %v\n", err)
		os.Exit(1)
	}

	if cfg.X402TreasuryAddress != "" && sponsor.TreasuryAddress() != cfg.X402TreasuryAddress {
		fmt.Fprintf(os.Stderr,
			"sanity check failed: treasury secret resolves to %s but X402_TREASURY_ADDRESS is %s\n",
			sponsor.TreasuryAddress(), cfg.X402TreasuryAddress,
		)
		os.Exit(1)
	}

	fmt.Printf("Treasury:   %s\n", sponsor.TreasuryAddress())
	fmt.Printf("Target:     %s\n", addr)
	fmt.Printf("Amount:     %s USDC\n", amount)
	fmt.Printf("USDC issuer: %s\n", cfg.X402USDCIssuer)
	fmt.Println()
	fmt.Println("Submitting payment to testnet ...")

	hash, err := sponsor.TransferUSDC(context.Background(), addr, amount, cfg.X402USDCIssuer)
	if err != nil {
		fmt.Fprintf(os.Stderr, "transfer failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("✓ Payment submitted successfully")
	fmt.Printf("  tx hash: %s\n", hash)
	fmt.Printf("  view:    https://stellarchain.io/transactions/%s\n", hash)
}
