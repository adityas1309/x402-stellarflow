// Per-session and per-day budget enforcement for the stellarflow MCP server.
//
// The whole point of x402 + smart accounts is that an autonomous agent can
// spend money under bounded conditions. The MCP server runs in the agent's
// process, so it's the right place to enforce those bounds before sending a
// payment payload to the network.
//
// Three caps, all configurable via env vars:
//
//   BUDGET_USDC_PER_CALL    — single-call ceiling. Anything above is rejected
//                             outright. Default: 0.20.
//   BUDGET_USDC_PER_SESSION — total spend across the lifetime of this MCP
//                             process. When the cap is hit, all subsequent
//                             calls fail. Default: 1.00.
//   BUDGET_USDC_PER_DAY     — total spend over a rolling 24h window kept in
//                             memory (resets when the process restarts).
//                             Default: 5.00.

export interface BudgetConfig {
  perCall: number;
  perSession: number;
  perDay: number;
}

export class Budget {
  private spentSession = 0;
  private spentDay = 0;
  private dayWindowStart = Date.now();

  constructor(public readonly cfg: BudgetConfig) {}

  /**
   * Check whether a call of the given price is allowed. Throws if it would
   * exceed any of the configured caps. Caller MUST call commit() with the
   * same amount once the call has actually been made.
   */
  check(amountUSDC: number): void {
    this.rotateDayWindowIfStale();
    if (amountUSDC > this.cfg.perCall) {
      throw new Error(
        `budget exceeded: \$${amountUSDC.toFixed(4)} > per-call cap \$${this.cfg.perCall.toFixed(2)}`,
      );
    }
    if (this.spentSession + amountUSDC > this.cfg.perSession) {
      throw new Error(
        `budget exceeded: session would reach \$${(this.spentSession + amountUSDC).toFixed(4)} > cap \$${this.cfg.perSession.toFixed(2)}`,
      );
    }
    if (this.spentDay + amountUSDC > this.cfg.perDay) {
      throw new Error(
        `budget exceeded: day would reach \$${(this.spentDay + amountUSDC).toFixed(4)} > cap \$${this.cfg.perDay.toFixed(2)}`,
      );
    }
  }

  commit(amountUSDC: number): void {
    this.rotateDayWindowIfStale();
    this.spentSession += amountUSDC;
    this.spentDay += amountUSDC;
  }

  status(): { session: number; day: number; remainingSession: number; remainingDay: number } {
    this.rotateDayWindowIfStale();
    return {
      session: this.spentSession,
      day: this.spentDay,
      remainingSession: this.cfg.perSession - this.spentSession,
      remainingDay: this.cfg.perDay - this.spentDay,
    };
  }

  private rotateDayWindowIfStale(): void {
    const dayMs = 24 * 60 * 60 * 1000;
    if (Date.now() - this.dayWindowStart >= dayMs) {
      this.dayWindowStart = Date.now();
      this.spentDay = 0;
    }
  }
}

export function loadBudgetFromEnv(): BudgetConfig {
  return {
    perCall: parseFloat(process.env.BUDGET_USDC_PER_CALL ?? "0.20"),
    perSession: parseFloat(process.env.BUDGET_USDC_PER_SESSION ?? "1.00"),
    perDay: parseFloat(process.env.BUDGET_USDC_PER_DAY ?? "5.00"),
  };
}
