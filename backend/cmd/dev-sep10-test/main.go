// dev-sep10-test exercises the full SEP-10 flow against a running stellarflow
// backend. It generates a fresh keypair, requests a challenge, signs it with
// the test keypair, and exchanges it for a Paseto access token. Used in dev
// for end-to-end smoke testing without needing a browser/Freighter.
//
// Usage:
//
//	go run ./cmd/dev-sep10-test -url=http://localhost:8086
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/stellar/go/keypair"
	"github.com/stellar/go/txnbuild"
)

type challengeResponse struct {
	ID                string    `json:"id"`
	Transaction       string    `json:"transaction"`
	NetworkPassphrase string    `json:"network_passphrase"`
	HomeDomain        string    `json:"home_domain"`
	ExpiresAt         time.Time `json:"expires_at"`
}

type tokenRequest struct {
	ID          string `json:"id"`
	Transaction string `json:"transaction"`
}

func main() {
	baseURL := flag.String("url", "http://localhost:8086", "stellarflow backend base URL")
	flag.Parse()

	// 1. Generate a fresh test keypair (NOT the treasury wallet — this is
	//    a throwaway identity for the smoke test).
	kp, err := keypair.Random()
	must("generate keypair", err)
	fmt.Printf("test keypair:\n  G %s\n  S %s\n\n", kp.Address(), kp.Seed())

	// 2. GET /api/auth/challenge?account=G...
	chURL := fmt.Sprintf("%s/api/auth/challenge?account=%s", *baseURL, kp.Address())
	fmt.Printf("[1] GET %s\n", chURL)
	resp, err := http.Get(chURL)
	must("challenge request", err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "challenge failed: %s\n%s\n", resp.Status, body)
		os.Exit(1)
	}
	var ch challengeResponse
	must("parse challenge", json.Unmarshal(body, &ch))
	fmt.Printf("    challenge id: %s\n", ch.ID)
	fmt.Printf("    network:      %s\n", ch.NetworkPassphrase)
	fmt.Printf("    expires:      %s\n", ch.ExpiresAt.Format(time.RFC3339))
	fmt.Printf("    tx xdr:       %s...\n\n", ch.Transaction[:60])

	// 3. Parse the unsigned tx and sign it with our keypair.
	genericTx, err := txnbuild.TransactionFromXDR(ch.Transaction)
	must("parse tx xdr", err)
	tx, ok := genericTx.Transaction()
	if !ok {
		fmt.Fprintln(os.Stderr, "expected plain tx, got fee-bump")
		os.Exit(1)
	}
	signedTx, err := tx.Sign(ch.NetworkPassphrase, kp)
	must("sign tx", err)
	signedXDR, err := signedTx.Base64()
	must("encode signed xdr", err)
	fmt.Printf("[2] signed challenge with %s\n\n", kp.Address()[:8])

	// 4. POST /api/auth/token with {id, signed_transaction}
	tokenURL := fmt.Sprintf("%s/api/auth/token", *baseURL)
	reqBody, _ := json.Marshal(tokenRequest{ID: ch.ID, Transaction: signedXDR})
	fmt.Printf("[3] POST %s\n", tokenURL)
	resp, err = http.Post(tokenURL, "application/json", bytes.NewReader(reqBody))
	must("token request", err)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "token exchange failed: %s\n%s\n", resp.Status, body)
		os.Exit(1)
	}
	pretty := bytes.Buffer{}
	json.Indent(&pretty, body, "    ", "  ")
	fmt.Printf("    response:\n    %s\n\n", pretty.String())

	fmt.Println("✓ SEP-10 round-trip successful")
}

func must(label string, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", label, err)
		os.Exit(1)
	}
}
