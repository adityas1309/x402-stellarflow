# Example: StellarFlow marketing analytics

**This entire `example/stellarflow/` directory is the bundled example for
the template. None of it is part of the wire.** When you fork this
template for your own agent-monetized API, you delete (or replace) the
contents of this directory and your wire keeps working unchanged.

## What's in here

| Path | What it does |
|---|---|
| `analysis/` | Pure-Go statistical functions (heatmap, hashtags, breakouts, engagement) over fake Instagram post data. Generic stats — no proprietary logic. |
| `mockscraper/` | Deterministic mock data generator. Implements `worker.Scraper` interface from the wire. Hashes the username to produce ~30 fake posts seeded by it. NO real network calls, NO Apify, NO scraping IP. |
| `handlers/` | The 5 paid x402 endpoint handlers (`posting_heatmap`, `trending_hashtags`, `breakout_posts`, `competitor_snapshot`, `compare_competitors`). Implements `api.X402EndpointRegistry` so the wire's router can register them via dependency injection. |
| `seed/` | Idempotent seed program that wipes + creates 3 demo brands (gymshark, nike, aloyoga) with mock posts. Run via `make seed`. |

## How the wire and the example connect

```
┌────────────────────────────────────────────────────────────┐
│ cmd/api/main.go (the runnable backend)                      │
│ ─────────────────────────────────────                       │
│   1. Loads config + connects DB + Redis                     │
│   2. Constructs the example: stellarflowhandlers.New(store)    │
│   3. Passes it to api.NewServer(...) as X402EndpointRegistry│
└────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌────────────────────────────────────────────────────────────┐
│ internal/api/server.go + router.go (the wire)               │
│ ──────────────────────────────────────                      │
│   - Sets up SEP-10, sponsor, x402 middleware, dashboards    │
│   - Calls s.x402Endpoints.RegisterRoutes(x402, X402Middleware)│
│   - The wire knows NOTHING about specific endpoint names    │
└────────────────────────────────────────────────────────────┘
                            │ (interface call, no concrete dep)
                            ▼
┌────────────────────────────────────────────────────────────┐
│ example/stellarflow/handlers/handlers.go (this directory)      │
│ ──────────────────────────────────────                      │
│   - ExampleService implements RegisterRoutes                │
│   - Registers /api/x402/posting_heatmap etc with prices     │
│   - Each handler reads from competitors table + analysis    │
└────────────────────────────────────────────────────────────┘
```

The wire (`internal/api`, `internal/stellar`, `internal/x402`, etc.) has
ZERO compile-time dependencies on this example package. Verify with:

```bash
go list -deps github.com/your-org/stellarflow/internal/api | grep stellarflow
# (should print nothing)
```

## How to remove this example completely

To strip StellarFlow and start with a blank example:

1. `rm -rf backend/example/stellarflow/`
2. Edit `backend/cmd/api/main.go`:
   - Remove `import "github.com/your-org/stellarflow/example/stellarflow/handlers"`
   - Remove `import "github.com/your-org/stellarflow/example/stellarflow/mockscraper"`
   - Remove the `processor.SetScraper(mockscraper.NewCompetitorScraper(store))` line
   - Change `api.NewServer(..., exampleEndpoints)` to `api.NewServer(..., nil)`
3. Edit `backend/internal/db/migration/001_init.up.sql` and remove the
   `competitors`, `competitor_metrics`, `metrics_history` tables (they're
   the example schema). The wire-only tables (organizations, users,
   refresh_tokens, operation_costs, usage_log, credit_transactions) stay.
4. `rm -rf frontend/src/pages/example_stellarflow_brands/`
5. Edit `frontend/src/App.tsx` to remove the `<Route path="brands">` line
6. Edit `frontend/src/components/layout/Sidebar.tsx` to remove the brands nav item
7. `rm mcp-server/src/example_stellarflow_tools.ts`
8. Edit `mcp-server/src/run.ts` to remove the `import { dispatch, listTools } from "./example_stellarflow_tools.js"` and the route registration. The wire handles everything else.
9. `make migrate-down && make migrate-up` to rebuild the schema.

After this you have a clean wire with no example. Add your own handlers,
data model, frontend pages, and MCP tools following the patterns the
StellarFlow example demonstrated.

## How to write your own example on top of the wire

See `TEMPLATE.md` at the repo root for the adaptation guide. The short
version is: copy this directory to `example/yourapi/`, replace the
contents, and update `backend/cmd/api/main.go` to import yours instead.
