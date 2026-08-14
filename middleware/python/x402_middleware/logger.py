"""In-memory call logger for the dashboard."""

from __future__ import annotations

import time
from collections import deque, defaultdict
from dataclasses import dataclass, asdict
from typing import Any


@dataclass
class PaidCallLog:
    timestamp: float  # unix seconds
    endpoint: str
    payer: str
    amount: float  # USDC
    facilitator: str
    duration_ms: int


class CallLogger:
    def __init__(self, max_entries: int = 500):
        self._buffer: deque[PaidCallLog] = deque(maxlen=max_entries)

    def log(self, entry: PaidCallLog) -> None:
        self._buffer.appendleft(entry)

    def stats(self, limit: int = 50) -> dict[str, Any]:
        total_calls = len(self._buffer)
        total_revenue = round(sum(e.amount for e in self._buffer), 4)
        unique_payers = len({e.payer for e in self._buffer})

        by_ep: dict[str, dict[str, float]] = defaultdict(lambda: {"calls": 0, "revenue": 0.0})
        for e in self._buffer:
            by_ep[e.endpoint]["calls"] += 1
            by_ep[e.endpoint]["revenue"] += e.amount

        by_endpoint = sorted(
            [{"endpoint": k, "calls": v["calls"], "revenue": round(v["revenue"], 4)} for k, v in by_ep.items()],
            key=lambda x: -x["revenue"],
        )

        recent = [
            {
                "timestamp": int(e.timestamp * 1000),  # JS ms to match Node dashboard
                "endpoint": e.endpoint,
                "payer": e.payer,
                "amount": e.amount,
                "facilitator": e.facilitator,
                "durationMs": e.duration_ms,
            }
            for e in list(self._buffer)[:limit]
        ]

        return {
            "totalCalls": total_calls,
            "totalRevenue": total_revenue,
            "uniquePayers": unique_payers,
            "byEndpoint": by_endpoint,
            "recent": recent,
        }


# Singleton shared by middleware + dashboard.
default_logger = CallLogger()
