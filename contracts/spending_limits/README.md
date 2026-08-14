# Agent Budget Registry — on-chain spending limits

A Soroban smart contract that enforces **per-agent daily USDC spending caps**
for x402 payments. Pushes the spending-limit check from off-chain (client-side
MCP budget) to on-chain (trustless, tamper-proof, auditable via events).

## Why on-chain?

Off-chain budget enforcement (the `BudgetCaps` in the MCP server) is easy to
bypass — an agent can simply patch the MCP server, or the operator can't
trust the MCP server at all if the agent developer is untrusted. Moving the
check on-chain means:

1. **Trustless**: the operator and the agent both enforce the same rules.
   Neither party can cheat the other.
2. **Auditable**: every `paid_call` event is on-chain forever. Third-party
   auditors can verify the agent's spending history against the operator's
   treasury income without access to any private systems.
3. **Safe by default**: if the agent's cap is zero or not set, the backend
   middleware refuses to settle x402 payments, so a misconfigured agent
   can't drain anything.

## Contract interface

```rust
// Only the operator can set/change caps.
fn set_cap(operator: Address, agent: Address, cap_stroops: i128);

// Only the operator can spend (the backend middleware calls this
// before submitting the x402 settlement to the facilitator).
fn try_spend(operator: Address, agent: Address, amount_stroops: i128) -> Result<i128, Error>;

// Read-only — dashboards use this to show "you've spent X / Y today".
fn get_budget(operator: Address, agent: Address) -> (i128, i128);
```

Amounts are in **stroops** (7 decimals, 10,000,000 stroops = 1 USDC) to
match the x402 v2 integer-amount convention.

## Events (on-chain attestations)

| Topic | Data | Emitted when |
|---|---|---|
| `("cap_set", operator, agent)` | `i128` (new cap) | `set_cap` is called |
| `("paid_call", operator, agent)` | `(i128 amount, i128 new_total, u64 day_start)` | `try_spend` succeeds |

Every event is queryable via `stellarchain.io` or any Horizon-compatible indexer.

## Day rollover

The contract tracks the UTC-day window via `day_started_at = now - (now % 86400)`.
On `try_spend`, if a new day has started since the last spend, the counter
auto-resets. No manual reset or cron job needed.

## Deployments

### Testnet (current default)

- **Contract ID**: `CBRE5KJZRMX6VOPPO6PZOVLMAKIFPB6SERENFHDHULRKG5NGVQ6ZTZ4F`
- **Deploy tx**: [`dd0d3077...`](https://stellar.expert/explorer/testnet/tx/dd0d30778d05b0770690e113d52f865b1ad70f7e284ad68428dcd748d9d21166)
- **Explorer**: https://stellar.expert/explorer/testnet/contract/CBRE5KJZRMX6VOPPO6PZOVLMAKIFPB6SERENFHDHULRKG5NGVQ6ZTZ4F

### Deploying your own fork

If you're forking this template for your own project you will want
a fresh contract instance with your own testnet operator key:

```bash
cd contracts/spending_limits
stellar contract build --network testnet
stellar contract deploy \
  --wasm target/wasm32v1-none/release/agent_budget.wasm \
  --source <your-testnet-key> \
  --network testnet
```

Budget this at only a small amount of testnet XLM from friendbot or your
own faucet-funded account.

## Building and testing

```bash
# Add the wasm target once (host machine must be able to cross-compile):
rustup target add wasm32v1-none

# Build the contract (produces target/wasm32v1-none/release/agent_budget.wasm):
stellar contract build

# Run the test suite (7 tests):
cargo test
```

## Integration with the x402 middleware

The backend middleware calls `try_spend` **before** calling the OZ facilitator
to settle a payment:

```
Agent ──POST /api/x402/sentiment──> Backend middleware
                                         │
                                         │ 1. Verify X-PAYMENT header
                                         │ 2. Call Soroban: try_spend(operator, payer, amount)
                                         │    │
                                         │    └── if DailyCapExceeded → 402
                                         │
                                         │ 3. Call OZ facilitator: /verify
                                         │ 4. Run example handler
                                         │ 5. Call OZ facilitator: /settle
                                         │
                                      Response
```

If step 2 fails, the backend returns 402 with an error explaining the cap
was exceeded. The agent's x402 signed payload is never submitted to the
facilitator, so no USDC moves and no settlement fee is paid.

## Why this is a differentiator

Most x402 reference implementations handle payments but have no on-chain
safety rails — a runaway agent or a compromised MCP server can drain a
wallet up to whatever USDC balance it has. This contract adds the missing
primitive:

> **Smart accounts with on-chain spending limits + immutable attestation trail.**

Off-chain orchestration + on-chain enforcement + emitted events for
auditability — all three in ~200 lines of Rust.
