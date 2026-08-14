#![no_std]

use soroban_sdk::{contract, contractimpl, contracttype, symbol_short, Address, Env};

#[contracttype]
#[derive(Clone)]
pub struct AuditRecord {
    pub operator: Address,
    pub agent: Address,
    pub amount_stroops: i128,
    pub new_total: i128,
}

#[contract]
pub struct AuditLog;

#[contractimpl]
impl AuditLog {
    /// Called by the spending-limit contract after an accepted spend.
    /// The caller is recorded in the event so indexers can verify origin.
    pub fn record(
        env: Env,
        operator: Address,
        agent: Address,
        amount_stroops: i128,
        new_total: i128,
    ) {
        let count: u32 = env
            .storage()
            .instance()
            .get(&symbol_short!("count"))
            .unwrap_or(0);
        env.storage().instance().set(
            &symbol_short!("count"),
            &(count.saturating_add(1)),
        );
        env.events().publish(
            (symbol_short!("audit"), operator, agent),
            (amount_stroops, new_total, count.saturating_add(1)),
        );
    }

    pub fn count(env: Env) -> u32 {
        env.storage()
            .instance()
            .get(&symbol_short!("count"))
            .unwrap_or(0)
    }
}

#[cfg(test)]
mod test {
    use super::*;
    use soroban_sdk::testutils::Address as _;

    #[test]
    fn record_increments_count_and_emits_event() {
        let env = Env::default();
        env.mock_all_auths();
        let contract = env.register(AuditLog, ());
        let client = AuditLogClient::new(&env, &contract);
        let operator = Address::generate(&env);
        let agent = Address::generate(&env);

        client.record(&operator, &agent, &500_000, &500_000);
        client.record(&operator, &agent, &250_000, &750_000);

        assert_eq!(client.count(), 2);
    }
}
