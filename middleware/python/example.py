"""
Minimal example: monetize a FastAPI endpoint with x402 in 3 lines.

Run with:
    pip install fastapi uvicorn httpx x402-middleware
    uvicorn example:app --port 3000

Then:
    curl -X POST http://localhost:3000/api/sentiment \
      -H "Content-Type: application/json" \
      -d '{"topic":"stellar"}'
    → 402 Payment Required (with payment requirements)
"""

from fastapi import FastAPI, Request
from x402_middleware import x402_paywall

app = FastAPI()


# One decorator turns your endpoint into a paid x402 endpoint.
@app.post("/api/sentiment")
@x402_paywall(
    price=0.05,
    treasury="GBLDFWELHTPY4SIW6BNHDPFAYLH3NR5N2HK5VTK5GPAUMK5OESE4SYR7",
    # facilitator_key="your-api-key",  # uncomment for live mode
    dry_run=True,  # use Stellar testnet for live settlement
)
async def sentiment(request: Request):
    body = await request.json()
    topic = body.get("topic", "unknown")
    return {
        "topic": topic,
        "sentiment": "positive",
        "score": 0.82,
        "paid_by": request.state.x402_payer,  # the Stellar G-address that paid
    }
