#!/usr/bin/env bash
# demo-flood.sh — fire one paid call against each of the 5 x402 endpoints,
# each with a distinct payer wallet, so the live feed in the operator
# dashboard fills up with variety. Useful for video demos and screenshots.
#
# Requires the stellarflow server running on http://localhost:8086.

set -euo pipefail

BASE="${BASE:-http://localhost:8086}"

mk_payment() {
  local from="$1"
  printf '{"x402Version":2,"scheme":"exact","network":"stellar:testnet","payload":{"from":"%s"}}' "$from" | base64
}

# Pad a label to a 56-char Stellar-shaped address: G + LABEL + Xs
mk_addr() {
  local label="$1"
  python3 -c "import sys; s='G'+sys.argv[1].ljust(55,'X')[:55]; assert len(s)==56; print(s)" "$label"
}

call() {
  local endpoint="$1"
  local body="$2"
  local payer_label="$3"
  local reason="$4"
  local payer
  payer=$(mk_addr "$payer_label")
  local payment
  payment=$(mk_payment "$payer")

  curl -s -X POST "$BASE/api/x402/$endpoint" \
    -H "Content-Type: application/json" \
    -H "X-Payment: $payment" \
    -H "X-StellarFlow-Client: claude-code" \
    -H "X-StellarFlow-Reason: $reason" \
    -d "$body" \
    -o /dev/null \
    -w "  %{http_code}  $endpoint  ($payer_label)\n"
  sleep 0.6
}

echo "Firing 5 paid calls against $BASE — open the Agent Desk to watch them land:"
echo

call posting_heatmap     '{"competitor_id":1,"top_n":5}'           "DEMOAGENT01" "what is the best time to post for fitness brands"
call trending_hashtags   '{"competitor_id":2,"top_n":5}'           "DEMOAGENT02" "find top hashtags nike is using right now"
call breakout_posts      '{"competitor_id":1,"threshold":1.5}'     "DEMOAGENT03" "which gymshark posts went viral this month"
call competitor_snapshot '{"competitor_id":3}'                     "DEMOAGENT04" "give me a one-shot profile of aloyoga"
call compare_competitors '{"competitor_ids":[1,2,3]}'              "DEMOAGENT05" "side-by-side comparison of all three brands"

echo
echo "Done. Check http://localhost:8086 for the live feed."
