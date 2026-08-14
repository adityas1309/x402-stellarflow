#![cfg(test)]

use super::*;
use soroban_sdk::{
    testutils::{Address as _, Ledger, LedgerInfo},
    Env,
};

fn setup() -> (Env, AgentBudgetContractClient<'static>, Address, Address) {
    let env = Env::default();
    env.mock_all_auths();

    let contract_id = env.register(AgentBudgetContract, ());
    let client = AgentBudgetContractClient::new(&env, &contract_id);

    let operator = Address::generate(&env);
    let agent = Address::generate(&env);

    (env, client, operator, agent)
}

#[test]
fn set_cap_stores_budget_and_emits_event() {
    let (env, client, operator, agent) = setup();

    client.set_cap(&operator, &agent, &1_000_000); // 0.1 USDC

    let (spent, cap) = client.get_budget(&operator, &agent);
    assert_eq!(spent, 0);
    assert_eq!(cap, 1_000_000);
}

#[test]
fn try_spend_within_cap_succeeds() {
    let (env, client, operator, agent) = setup();

    client.set_cap(&operator, &agent, &1_000_000); // 0.1 USDC

    // Spend 0.05 USDC
    let remaining = client.try_spend(&operator, &agent, &500_000);
    assert_eq!(remaining, 500_000);

    let (spent, _cap) = client.get_budget(&operator, &agent);
    assert_eq!(spent, 500_000);
}

#[test]
fn try_spend_accumulates_across_calls() {
    let (env, client, operator, agent) = setup();

    client.set_cap(&operator, &agent, &1_000_000);

    client.try_spend(&operator, &agent, &300_000);
    client.try_spend(&operator, &agent, &200_000);

    let (spent, _) = client.get_budget(&operator, &agent);
    assert_eq!(spent, 500_000);
}

#[test]
#[should_panic(expected = "Error(Contract, #3)")]
fn try_spend_over_cap_panics() {
    let (env, client, operator, agent) = setup();

    client.set_cap(&operator, &agent, &1_000_000);

    // Spend 0.09 (ok), then try to spend another 0.02 (would total 0.11 > 0.10)
    client.try_spend(&operator, &agent, &900_000);
    client.try_spend(&operator, &agent, &200_000);
}

#[test]
fn try_spend_resets_on_new_day() {
    let (env, client, operator, agent) = setup();

    client.set_cap(&operator, &agent, &1_000_000);

    // Spend the full cap
    client.try_spend(&operator, &agent, &1_000_000);
    let (spent, _) = client.get_budget(&operator, &agent);
    assert_eq!(spent, 1_000_000);

    // Advance the ledger timestamp by 2 days
    env.ledger().set(LedgerInfo {
        timestamp: env.ledger().timestamp() + 2 * 86_400,
        protocol_version: env.ledger().protocol_version(),
        sequence_number: env.ledger().sequence(),
        network_id: Default::default(),
        base_reserve: 10,
        min_temp_entry_ttl: 16,
        min_persistent_entry_ttl: 4096,
        max_entry_ttl: 6_312_000,
    });

    // Now the counter should have rolled over and we can spend again
    let remaining = client.try_spend(&operator, &agent, &500_000);
    assert_eq!(remaining, 500_000);
}

#[test]
#[should_panic(expected = "Error(Contract, #1)")]
fn try_spend_fails_if_no_budget_set() {
    let (env, client, operator, agent) = setup();
    client.try_spend(&operator, &agent, &100_000);
}

#[test]
#[should_panic(expected = "Error(Contract, #2)")]
fn try_spend_fails_if_cap_is_zero() {
    let (env, client, operator, agent) = setup();
    client.set_cap(&operator, &agent, &0);
    client.try_spend(&operator, &agent, &100_000);
}
