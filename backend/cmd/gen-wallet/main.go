// gen-wallet generates a fresh Stellar keypair (G-address + S-key) and
// prints it to stdout. The keypair lives only in memory — nothing is
// written to disk, nothing is submitted to the network.
//
// Use this once at the start of the hackathon to create the operator's
// "all-in-one" wallet that will:
//
//   1. Receive x402 payments from agents (treasury role)
//   2. Sponsor new agent accounts (the X402_TREASURY_SECRET in backend/.env)
//   3. Fund the test wallets in seed-test-wallets
//
// After running this:
//
//   1. Copy the G-address and S-key to a safe place (1Password, etc.)
//   2. Open Freighter, transfer your existing XLM + USDC from the old
//      treasury (GAWWHN…X2UX) to the new G-address
//   3. Paste the S-key into backend/.env as X402_TREASURY_SECRET
//   4. Replace X402_TREASURY_ADDRESS in backend/.env with the new G-address
//   5. Restart the backend
//
// Usage:
//
//   go run ./cmd/gen-wallet
//   # or via Makefile:
//   make gen-wallet
package main

import (
	"fmt"
	"os"

	"github.com/stellar/go/keypair"
)

func main() {
	kp, err := keypair.Random()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to generate keypair: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("════════════════════════════════════════════════════════════════")
	fmt.Println("  Fresh Stellar wallet generated")
	fmt.Println("════════════════════════════════════════════════════════════════")
	fmt.Println()
	fmt.Printf("  Public  (G…):  %s\n", kp.Address())
	fmt.Printf("  Secret  (S…):  %s\n", kp.Seed())
	fmt.Println()
	fmt.Println("════════════════════════════════════════════════════════════════")
	fmt.Println("  Next steps:")
	fmt.Println("════════════════════════════════════════════════════════════════")
	fmt.Println()
	fmt.Println("  1. SAVE the secret key somewhere safe (1Password, Bitwarden, ...)")
	fmt.Println("     This is the ONLY time it will be shown.")
	fmt.Println()
	fmt.Println("  2. Open Freighter, transfer all your XLM + USDC from the old")
	fmt.Println("     treasury wallet to the new G-address shown above.")
	fmt.Println("     (Don't forget to establish the USDC trustline on this new")
	fmt.Println("     wallet first — Freighter will prompt you when you try to")
	fmt.Println("     send USDC to it.)")
	fmt.Println()
	fmt.Println("  3. Update backend/.env:")
	fmt.Println("       X402_TREASURY_ADDRESS=<new G-address>")
	fmt.Println("       X402_TREASURY_SECRET=<new S-key>")
	fmt.Println()
	fmt.Println("  4. Restart the backend (make run).")
	fmt.Println()
}
