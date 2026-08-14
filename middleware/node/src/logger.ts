/**
 * In-memory call logger for the x402 middleware.
 *
 * Every successful paid call adds an entry to a ring buffer, which the
 * dashboard reads via /x402/stats. Lightweight, no DB required, bounded
 * memory (default 500 entries). For production with multiple backend
 * instances, swap this for Redis or a proper DB.
 */

export interface PaidCallLog {
  timestamp: number; // epoch ms
  endpoint: string;
  payer: string;
  amount: number; // USDC
  facilitator: string; // "openzeppelin" or "dry-run"
  durationMs: number;
}

export interface StatsSummary {
  totalCalls: number;
  totalRevenue: number;
  uniquePayers: number;
  byEndpoint: Array<{ endpoint: string; calls: number; revenue: number }>;
  recent: PaidCallLog[];
}

export class CallLogger {
  private buffer: PaidCallLog[] = [];
  private max: number;

  constructor(maxEntries = 500) {
    this.max = maxEntries;
  }

  log(entry: PaidCallLog): void {
    this.buffer.unshift(entry);
    if (this.buffer.length > this.max) {
      this.buffer.length = this.max;
    }
  }

  stats(limit = 50): StatsSummary {
    const totalCalls = this.buffer.length;
    const totalRevenue = this.buffer.reduce((s, e) => s + e.amount, 0);
    const uniquePayers = new Set(this.buffer.map((e) => e.payer)).size;

    const byEp = new Map<string, { calls: number; revenue: number }>();
    for (const e of this.buffer) {
      const cur = byEp.get(e.endpoint) ?? { calls: 0, revenue: 0 };
      cur.calls += 1;
      cur.revenue += e.amount;
      byEp.set(e.endpoint, cur);
    }
    const byEndpoint = Array.from(byEp.entries())
      .map(([endpoint, v]) => ({ endpoint, ...v }))
      .sort((a, b) => b.revenue - a.revenue);

    return {
      totalCalls,
      totalRevenue: Math.round(totalRevenue * 10000) / 10000,
      uniquePayers,
      byEndpoint,
      recent: this.buffer.slice(0, limit),
    };
  }
}

// Singleton — the x402 middleware and the dashboard both import this.
export const defaultLogger = new CallLogger();
