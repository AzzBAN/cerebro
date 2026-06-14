// TypeScript mirrors of the Go JSON DTOs in internal/adapter/web/state.go.
// All monetary values arrive as strings (full decimal precision) and are
// formatted for display client-side — never parsed into floats for math.

export interface Quote {
  symbol: string;
  bid: string;
  ask: string;
  mid: string;
  last: string;
  priceChange: string;
  priceChangePercent: string;
  volume24h: string;
  timestamp: number;
}

export interface Position {
  symbol: string;
  venue: string;
  side: string;
  quantity: string;
  entryPrice: string;
  currentPrice: string;
  stopLoss: string;
  takeProfit1: string;
  strategy: string;
  leverage: number;
  margin: string;
  isolated: boolean;
  unrealizedPnl: string;
  pnlPct: string;
  pnlRoi: string;
  openedAt: number;
}

export interface Proposal {
  id: string;
  symbol: string;
  venue: string;
  side: string;
  currentStop: string;
  currentTp: string;
  proposedStop: string;
  proposedTp: string;
  reasoning: string;
  createdAt: number;
}

export interface LogLine {
  level: string;
  text: string;
  at: number;
}

export interface Bias {
  symbol: string;
  score: number;
  label: string;
  reasoning: string;
  cachedAt: number;
}

export interface AgentRun {
  runId: string;
  agent: string;
  step: string;
  toolName: string;
  provider: string;
  model: string;
  symbol: string;
  description: string;
  content: string;
  stepNum: number;
  maxSteps: number;
  at: number;
}

export interface Macro {
  fearGreedValue: number;
  fearGreedCategory: string;
  fundingRate: number;
  openInterestUsd: string;
  longShortRatio: number;
  updatedAt: number;
}

export interface NewsItem {
  title: string;
  source: string;
  domain: string;
  url: string;
  sentiment: string;
}

export interface Budget {
  date: string;
  tokensUsed: number;
  costUsd: number;
  tokenBudget: number;
  costBudgetUsd: number;
}

export interface Snapshot {
  quotes: Quote[];
  positions: Position[];
  proposals: Proposal[];
  logs: LogLine[];
  agentRuns: AgentRun[];
  bias: Bias[];
  macro: Macro | null;
  news: NewsItem[];
  budget: Budget | null;
  heartbeat: string;
}

// Envelope is the WebSocket frame: a type tag + one payload.
export interface Envelope {
  type: string;
  data: unknown;
}
