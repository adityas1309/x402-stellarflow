//! Agent Budget Registry — on-chain daily spending caps for x402 agents.
//!
//! This contract is the on-chain safety rail for the stellarflow.
//! Operators register it once, set a per-wallet daily USDC cap, and the backend
//! middleware calls `try_spend` before settling each x402 payment. If the agent
//! would exceed its daily cap, the call is rejected BEFORE any on-chain USDC
//! transfer happens.
//!
//! This pushes the spending-limit enforcement from off-chain (client-side MCP
//! budget) to on-chain (trustless, tamper-proof, auditable via events).
//!
//! ## Data model
//!
//! Per agent wallet, we store:
//!   - `daily_cap_stroops`: max USDC the agent can spend per UTC day (in stroops)
//!   - `spent_today_stroops`: running total for the current day
//!   - `day_started_at`: UTC timestamp of the start of the current day window
//!
//! Amounts are in stroops (7 decimals, 10_000_000 stroops = 1 USDC) to match
//! the x402 v2 integer-amount convention.
//!
//! ## Authorization model
//!
//! - `set_cap(operator, agent, cap)` requires `operator.require_auth()` — the
//!   operator wallet (the x402 treasury) is the only one who can change caps.
//! - `try_spend(operator, agent, amount)` also requires `operator.require_auth()`
//!   so a random third party can't poison an agent's daily counter.
//!
//! ## Events
//!
//! Emitted on every `try_spend`:
//!   `("paid_call", operator, agent) → (amount, new_total, day_started_at)`
//!
//! Emitted on every `set_cap`:
//!   `("cap_set", operator, agent) → new_cap`
//!
//! These events are the "on-chain attestation" trail — any auditor can verify
//! the agent's spending history against the operator's treasury income.

#![no_std]

use soroban_sdk::{
    contract, contracterror, contractimpl, contracttype, symbol_short, Address, Env, IntoVal,
};

const SECONDS_PER_DAY: u64 = 86_400;

/// Storage key: (operator, agent) → AgentBudget.
/// The operator is part of the key so multiple operators can use the same
/// contract instance without polluting each other's agent registries.
#[contracttype]
#[derive(Clone)]
pub struct AgentKey {
    pub operator: Address,
    pub agent: Address,
}

/// Per-agent budget state stored on-chain.
#[contracttype]
#[derive(Clone)]
pub struct AgentBudget {
    pub daily_cap_stroops: i128,
    pub spent_today_stroops: i128,
    pub day_started_at: u64,
}

/// Errors returned to the caller. The backend middleware translates these
/// into x402 402 responses with human-readable messages.
#[contracterror]
#[derive(Copy, Clone, PartialEq, Eq, Debug)]
#[repr(u32)]
pub enum Error {
    /// The operator hasn't set a cap for this agent yet.
    NoBudgetSet = 1,
    /// The agent's daily cap is zero (effectively blocked).
    CapIsZero = 2,
    /// The agent has already spent their daily allowance.
    DailyCapExceeded = 3,
    /// Negative or zero amount passed to try_spend.
    InvalidAmount = 4,
}

#[contract]
pub struct AgentBudgetContract;

const AUDIT_CONTRACT_KEY: soroban_sdk::Symbol = symbol_short!("audit");

#[contractimpl]
impl AgentBudgetContract {
    /// Set (or update) the daily spending cap for an agent, in USDC stroops.
    /// Only the operator can call this. Passing 0 effectively blocks the agent.
    pub fn set_cap(env: Env, operator: Address, agent: Address, cap_stroops: i128) {
        operator.require_auth();

        let key = AgentKey {
            operator: operator.clone(),
            agent: agent.clone(),
        };

        // Preserve existing spent counter if the agent already has a budget
        // (a cap change mid-day should not reset the running total).
        let budget = if let Some(existing) = env.storage().persistent().get::<_, AgentBudget>(&key)
        {
            AgentBudget {
                daily_cap_stroops: cap_stroops,
                spent_today_stroops: existing.spent_today_stroops,
                day_started_at: existing.day_started_at,
            }
        } else {
            AgentBudget {
                daily_cap_stroops: cap_stroops,
                spent_today_stroops: 0,
                day_started_at: current_day_start(&env),
            }
        };

        env.storage().persistent().set(&key, &budget);

        // Emit attestation event: operator + agent + new cap
        env.events().publish(
            (symbol_short!("cap_set"), operator, agent),
            cap_stroops,
        );
    }

    /// Configure an optional audit sink contract. The sink receives a
    /// `record(operator, agent, amount, new_total)` call after each spend.
    /// Existing deployments remain compatible because the sink is optional.
    pub fn set_audit_contract(env: Env, operator: Address, audit_contract: Address) {
        operator.require_auth();
        env.storage().instance().set(&AUDIT_CONTRACT_KEY, &audit_contract);
    }

    /// Attempt to spend `amount_stroops` from the agent's daily budget.
    /// Returns the remaining allowance after the spend. Fails if over cap.
    pub fn try_spend(
        env: Env,
        operator: Address,
        agent: Address,
        amount_stroops: i128,
    ) -> Result<i128, Error> {
        operator.require_auth();

        if amount_stroops <= 0 {
            return Err(Error::InvalidAmount);
        }

        let key = AgentKey {
            operator: operator.clone(),
            agent: agent.clone(),
        };

        let mut budget: AgentBudget = env
            .storage()
            .persistent()
            .get(&key)
            .ok_or(Error::NoBudgetSet)?;

        if budget.daily_cap_stroops == 0 {
            return Err(Error::CapIsZero);
        }

        // Auto-rollover: if a new UTC day has started, reset the counter.
        let today = current_day_start(&env);
        if today > budget.day_started_at {
            budget.spent_today_stroops = 0;
            budget.day_started_at = today;
        }

        let new_total = budget
            .spent_today_stroops
            .checked_add(amount_stroops)
            .ok_or(Error::InvalidAmount)?;

        if new_total > budget.daily_cap_stroops {
            return Err(Error::DailyCapExceeded);
        }

        budget.spent_today_stroops = new_total;
        env.storage().persistent().set(&key, &budget);

        // Emit attestation event: operator + agent + (amount, new_total, day_start)
        env.events().publish(
            (symbol_short!("paid_call"), operator.clone(), agent.clone()),
            (amount_stroops, new_total, budget.day_started_at),
        );

        if let Some(audit_contract) = env
            .storage()
            .instance()
            .get::<_, Address>(&AUDIT_CONTRACT_KEY)
        {
            let _: () = env.invoke_contract(
                &audit_contract,
                &symbol_short!("record"),
                soroban_sdk::vec![
                    &env,
                    operator.into_val(&env),
                    agent.into_val(&env),
                    amount_stroops.into_val(&env),
                    new_total.into_val(&env)
                ],
            );
        }

        let remaining = budget.daily_cap_stroops - new_total;
        Ok(remaining)
    }

    /// Read-only: returns (spent_today, cap) for an agent. Used by dashboards.
    /// Returns (0, 0) if no budget is set.
    pub fn get_budget(env: Env, operator: Address, agent: Address) -> (i128, i128) {
        let key = AgentKey { operator, agent };
        if let Some(budget) = env.storage().persistent().get::<_, AgentBudget>(&key) {
            let today = current_day_start(&env);
            // If a new day has started, spent_today is effectively 0.
            let spent = if today > budget.day_started_at {
                0
            } else {
                budget.spent_today_stroops
            };
            (spent, budget.daily_cap_stroops)
        } else {
            (0, 0)
        }
    }
}

/// Returns the UNIX timestamp of the start of the current UTC day,
/// floored to midnight. Used to determine when to roll over a daily counter.
fn current_day_start(env: &Env) -> u64 {
    let now = env.ledger().timestamp();
    now - (now % SECONDS_PER_DAY)
}

mod test;
