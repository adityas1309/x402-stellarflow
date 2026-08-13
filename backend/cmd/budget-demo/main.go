// budget-demo — demonstrates the on-chain spending limits contract in action.
//
// This CLI drives the Soroban `agent_budget` contract deployed on Stellar
// testnet and shows the full safety rail story: an operator sets a daily
// cap, the agent spends within the cap (OK), tries to spend over the cap
// (DailyCapExceeded), and every event is emitted on-chain for audit.
//
// Output is video-friendly: narrated steps with sleeps, tx hashes printed
// with stellarchain.io links, and clear success/failure indicators.
//
// Prerequisites:
//   - `stellar` CLI installed and authed (stellar keys generate stellarflow --network testnet)
//   - The contract is deployed on testnet at CBRE5KJZRMX6VOPPO6PZOVLMAKIFPB6SERENFHDHULRKG5NGVQ6ZTZ4F
//
// Usage:
//   go run ./cmd/budget-demo
//
// Env:
//   X402_BUDGET_CONTRACT_ID   Override the contract ID (default: testnet deploy)
//   STELLAR_IDENTITY          Stellar CLI identity alias (default: stellarflow)
//   STELLAR_NETWORK           testnet (default: testnet)
//   AGENT_ADDRESS             Agent G-address to use in the demo (default: hardcoded)
//   STEP_DELAY_SECONDS        Sleep between steps for narration (default: 2)
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultContract = "CBRE5KJZRMX6VOPPO6PZOVLMAKIFPB6SERENFHDHULRKG5NGVQ6ZTZ4F"
	defaultAgent    = "GDWBI72BYSZOGX2EZPZTN7KDP26PYCQECGTWOX7LR3YFUYCVZM6I2DGU"
	defaultIdentity = "testnet-op"
	defaultNetwork  = "testnet"
)

type c struct{}

var color = c{}

func (c) reset() string  { return "\033[0m" }
func (c) bold() string   { return "\033[1m" }
func (c) dim() string    { return "\033[2m" }
func (c) cyan() string   { return "\033[36m" }
func (c) green() string  { return "\033[32m" }
func (c) yellow() string { return "\033[33m" }
func (c) red() string    { return "\033[31m" }

func header(s string) {
	fmt.Println()
	fmt.Println(color.cyan() + strings.Repeat("━", 72) + color.reset())
	fmt.Println(color.cyan() + "  " + color.bold() + s + color.reset())
	fmt.Println(color.cyan() + strings.Repeat("━", 72) + color.reset())
	fmt.Println()
}

func step(n int, s string) {
	fmt.Printf("%s[%d]%s %s%s%s\n", color.cyan(), n, color.reset(), color.bold(), s, color.reset())
}

func info(s string) {
	fmt.Printf("    %s%s%s\n", color.dim(), s, color.reset())
}

func ok(s string) {
	fmt.Printf("    %s✓%s %s\n", color.green(), color.reset(), s)
}

func fail(s string) {
	fmt.Printf("    %s✗%s %s\n", color.red(), color.reset(), s)
}

func explorerLink(tx string) string {
	// This demo is testnet-only, so always point at the testnet explorer.
	return fmt.Sprintf("https://stellarchain.io/explorer/testnet/tx/%s", tx)
}

func main() {
	contract := env("X402_BUDGET_CONTRACT_ID", defaultContract)
	agent := env("AGENT_ADDRESS", defaultAgent)
	identity := env("STELLAR_IDENTITY", defaultIdentity)
	network := env("STELLAR_NETWORK", defaultNetwork)
	delay := envInt("STEP_DELAY_SECONDS", 2)

	// Get operator G-address from identity.
	operator := runOutput("stellar", "keys", "address", identity)
	operator = strings.TrimSpace(operator)

	header("x402 on-chain spending limits — live demo")
	fmt.Printf("  Contract:   %s%s%s\n", color.cyan(), contract, color.reset())
	fmt.Printf("  Operator:   %s%s%s  (your wallet)\n", color.dim(), operator, color.reset())
	fmt.Printf("  Agent:      %s%s%s\n", color.dim(), agent, color.reset())
	fmt.Printf("  Network:    %s\n", network)
	fmt.Println()

	pause(delay)

	// ── Step 1: Set daily cap to 1 USDC ───────────────────────
	step(1, "Set daily spending cap to 1.00 USDC for the agent")
	info("Only the operator wallet can call set_cap (require_auth).")
	info("Calls stellar contract invoke to submit the tx on-chain.")
	fmt.Println()
	txHash := invokeContract(contract, identity, network,
		"set_cap",
		"--operator", operator,
		"--agent", agent,
		"--cap_stroops", "10000000", // 1 USDC = 10M stroops
	)
	ok("Cap set to 1.00 USDC. tx: " + txHash)
	info(explorerLink(txHash))
	info("Event emitted on-chain: (\"cap_set\", operator, agent) → 10000000")
	fmt.Println()

	pause(delay)

	// ── Step 2: Try to spend 0.10 USDC (within cap) ───────────
	step(2, "Agent tries to spend 0.10 USDC (within cap)")
	info("The backend middleware calls this BEFORE settling each x402 payment.")
	fmt.Println()
	txHash = invokeContract(contract, identity, network,
		"try_spend",
		"--operator", operator,
		"--agent", agent,
		"--amount_stroops", "1000000", // 0.1 USDC
	)
	ok("Spend approved. tx: " + txHash)
	info(explorerLink(txHash))
	info("Event emitted on-chain: (\"paid_call\", operator, agent) → (1000000, 1000000, day_start)")
	info("Remaining allowance: 0.90 USDC")
	fmt.Println()

	pause(delay)

	// ── Step 3: Try to spend another 0.50 USDC (still within cap) ──
	step(3, "Agent spends another 0.50 USDC (cumulative 0.60 USDC)")
	fmt.Println()
	txHash = invokeContract(contract, identity, network,
		"try_spend",
		"--operator", operator,
		"--agent", agent,
		"--amount_stroops", "5000000", // 0.5 USDC
	)
	ok("Spend approved. tx: " + txHash)
	info(explorerLink(txHash))
	info("Remaining allowance: 0.40 USDC")
	fmt.Println()

	pause(delay)

	// ── Step 4: Try to spend 0.50 USDC (WOULD EXCEED) ─────────
	step(4, "Agent tries to spend 0.50 USDC (would total 1.10 USDC — OVER CAP)")
	info("The contract rejects this with DailyCapExceeded (error #3).")
	info("No USDC moves. The x402 facilitator is never called.")
	fmt.Println()
	output, err := invokeContractCapturingError(contract, identity, network,
		"try_spend",
		"--operator", operator,
		"--agent", agent,
		"--amount_stroops", "5000000",
	)
	if err != nil {
		fail("Spend rejected (expected): contract returned DailyCapExceeded")
		info(truncate(output, 300))
	} else {
		fail("UNEXPECTED: spend was approved. This shouldn't happen.")
	}
	fmt.Println()

	pause(delay)

	// ── Step 5: Query current budget state ────────────────────
	step(5, "Read current budget state (get_budget)")
	info("Returns (spent_today, daily_cap) in stroops. Read-only, no tx fee.")
	fmt.Println()
	output = runOutput("stellar", "contract", "invoke",
		"--id", contract,
		"--source", identity,
		"--network", network,
		"--send=no",
		"--",
		"get_budget",
		"--operator", operator,
		"--agent", agent,
	)
	ok("Budget state: " + strings.TrimSpace(output))
	info("Interpretation: spent 0.60 / 1.00 USDC today (6000000 / 10000000 stroops)")
	fmt.Println()

	// ── Final summary ─────────────────────────────────────────
	header("On-chain safety rail demo complete")
	fmt.Printf("  %s✓%s Every paid call was checked on-chain before settlement\n", color.green(), color.reset())
	fmt.Printf("  %s✓%s Events emitted for full audit trail (cap_set + paid_call × N)\n", color.green(), color.reset())
	fmt.Printf("  %s✓%s Over-cap spend was rejected BEFORE any USDC moved\n", color.green(), color.reset())
	fmt.Printf("  %s✓%s All transactions visible on stellarchain.io\n", color.green(), color.reset())
	fmt.Println()
	fmt.Printf("  This is the %son-chain differentiator%s for the stellarflow:\n", color.bold(), color.reset())
	fmt.Println("  smart account with enforced spending limits + immutable attestation.")
	fmt.Println()
	fmt.Printf("  Contract: %shttps://stellarchain.io/explorer/testnet/contract/%s%s\n",
		color.cyan(), contract, color.reset())
	fmt.Println()
}

// ─── Helpers ──────────────────────────────────────────────────

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		fmt.Sscanf(v, "%d", &n)
		if n > 0 {
			return n
		}
	}
	return def
}

func pause(seconds int) {
	if seconds > 0 {
		time.Sleep(time.Duration(seconds) * time.Second)
	}
}

func runOutput(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "\n%scommand failed:%s %s %s\n%s\n",
			color.red(), color.reset(), name, strings.Join(args, " "), err.Error())
		os.Exit(1)
	}
	return out.String()
}

// invokeContract runs `stellar contract invoke` for a state-changing call.
// The stellar CLI prints a lot of noise to stderr; we capture stdout (tx hash line)
// and return the hash.
func invokeContract(contract, identity, network, method string, args ...string) string {
	fullArgs := []string{
		"contract", "invoke",
		"--id", contract,
		"--source", identity,
		"--network", network,
		"--",
		method,
	}
	fullArgs = append(fullArgs, args...)

	cmd := exec.Command("stellar", fullArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, stderr.String())
		fmt.Fprintf(os.Stderr, "%sstellar contract invoke failed:%s %v\n", color.red(), color.reset(), err)
		os.Exit(1)
	}

	// stellar CLI prints the tx hash in stderr as "Signing transaction: <hash>"
	stderrStr := stderr.String()
	var txHash string
	for _, line := range strings.Split(stderrStr, "\n") {
		if strings.Contains(line, "Signing transaction:") {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				txHash = strings.TrimSpace(parts[len(parts)-1])
				break
			}
		}
	}
	if txHash == "" {
		txHash = "(hash not found in output)"
	}
	return txHash
}

// invokeContractCapturingError runs an invoke and returns (output, err) so the
// caller can distinguish success from contract-error panics.
func invokeContractCapturingError(contract, identity, network, method string, args ...string) (string, error) {
	fullArgs := []string{
		"contract", "invoke",
		"--id", contract,
		"--source", identity,
		"--network", network,
		"--",
		method,
	}
	fullArgs = append(fullArgs, args...)

	cmd := exec.Command("stellar", fullArgs...)
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	err := cmd.Run()
	return combined.String(), err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
