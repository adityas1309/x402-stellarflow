// ═══ EXAMPLE: StellarFlow — sentiment analysis tool definition ═══
//
// This file is the MCP tool bundle for the StellarFlow example. StellarFlow
// exposes a single paid endpoint (POST /api/x402/sentiment) that reads live
// public stories and calculates a transparent topic signal. The agent pays
// $0.05 USDC per call.
//
// REPLACE THIS FILE for your own template fork. See TEMPLATE.md for the
// adaptation guide. The wire pieces (cli.ts, run.ts, init.ts, client.ts,
// budget.ts, wallet-store.ts) DO NOT need to change.
//
// The tool name is namespaced with `stellarflow_` so it doesn't collide
// with other MCP servers the client might have installed.

import type { Tool } from "@modelcontextprotocol/sdk/types.js";
import { paidCall, type StellarFlowConfig } from "./client.js";
import type { Budget } from "./budget.js";

export interface ToolContext {
  cfg: StellarFlowConfig;
  budget: Budget;
}

interface ToolDef {
  name: string;
  description: string;
  inputSchema: Tool["inputSchema"];
  endpoint: string;
  priceUSDC: number;
  formatResult: (data: unknown) => string;
}

const TOOLS: ToolDef[] = [
  {
    name: "stellarflow_sentiment",
    description:
      "Analyze public sentiment about any topic using current public stories and a transparent language signal. " +
      "Returns a sentiment score (-1 to 1), label (positive/negative/neutral), human-readable summary, " +
      "and the most common keywords/themes. Pay-per-call $0.05 USDC on Stellar via x402. " +
      "Use this when the user asks about public opinion, market sentiment, or how people feel about a brand, " +
      "technology, product, trend, or event.",
    inputSchema: {
      type: "object",
      properties: {
        topic: {
          type: "string",
          description:
            "The topic to analyze. Can be a brand ('tesla'), a technology ('stellar blockchain'), " +
            "a trend ('remote work'), an event, or any free-form string. The backend normalizes it " +
            "to an Instagram hashtag for scraping.",
        },
      },
      required: ["topic"],
    },
    endpoint: "sentiment",
    priceUSDC: 0.05,
    formatResult: (data: unknown) => {
      const d = data as {
        topic: string;
        sentiment: string;
        score: number;
        summary: string;
        sources: number;
        keywords: string[];
        engine: string;
        source: string;
      };
      const emoji =
        d.sentiment === "positive"
          ? "📈"
          : d.sentiment === "negative"
          ? "📉"
          : "➡️";
      const engineTag =
        d.engine === "mock"
          ? " *(mock — set APIFY_TOKEN and OPENAI_API_KEY for real analysis)*"
          : "";
      return [
        `## ${emoji} Sentiment: ${d.sentiment} (${d.score.toFixed(2)})${engineTag}`,
        "",
        `**Topic:** ${d.topic}`,
        `**Sources analyzed:** ${d.sources} via ${d.source}`,
        `**Keywords:** ${d.keywords.join(", ")}`,
        "",
        d.summary,
      ].join("\n");
    },
  },
];

// ─── Public API ────────────────────────────────────────────────

export function listTools(): Tool[] {
  return TOOLS.map((t) => ({
    name: t.name,
    description: t.description,
    inputSchema: t.inputSchema,
  }));
}

export async function dispatch(
  ctx: ToolContext,
  toolName: string,
  args: Record<string, unknown>,
  reasoning?: string,
): Promise<{ content: Array<{ type: "text"; text: string }>; isError?: boolean }> {
  const tool = TOOLS.find((t) => t.name === toolName);
  if (!tool) {
    return {
      content: [{ type: "text", text: `Unknown tool: ${toolName}` }],
      isError: true,
    };
  }

  // Budget check.
  ctx.budget.check(tool.priceUSDC);

  // Paid call to the backend.
  const result = await paidCall(ctx.cfg, {
    endpoint: tool.endpoint,
    body: args,
    reasoning,
    priceUSDC: tool.priceUSDC,
  });

  // Budget commit.
  ctx.budget.commit(tool.priceUSDC);

  // Format the result for the LLM.
  const formatted = tool.formatResult(result.data);
  const receipt = `💳 Paid $${tool.priceUSDC.toFixed(2)} USDC · endpoint: ${tool.endpoint}`;

  return {
    content: [{ type: "text", text: `${receipt}\n\n${formatted}` }],
  };
}
