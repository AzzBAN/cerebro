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

export interface Opportunity {
  id: string;
  symbol: string;
  venue: string;
  side: string; // "buy" | "sell"
  confidence: number; // 0.0–1.0
  reasoning: string;
  avoided: boolean;
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

export interface StrategySummary {
  name: string;
  primaryTimeframe: string;
  trendTimeframe: string;
  riskPctPerTrade: number;
  maxPositionSizePct: number;
  dedupWindowSeconds: number;
  confirmationCandles: number;
  requireBiasAlignment: boolean;
  orderType: string;
  markets: number;
}

export interface RiskSummary {
  maxDrawdownPct: number;
  maxDailyLossPct: number;
  maxExposurePct: number;
  maxOpenPositions: number;
  maxOpenPositionsPerVenue: number;
  maxOpenPositionsPerSymbol: number;
  minEquityToTrade: number;
}

export interface Config {
  environment: string;
  aggressiveness: string; // "conservative" | "balanced" | "aggressive"
  strategies: StrategySummary[];
  risk: RiskSummary;
}

// RawStrategy is one strategy's pre-scaling editable values (GET /api/strategies/raw).
export interface RawStrategy {
  name: string;
  enabled: boolean;
  riskPctPerTrade: number;
  maxPositionSizePct: number;
  dedupWindowSeconds: number;
  confirmationCandles: number;
  requireBiasAlignment: boolean;
  stopLossFixedPct: number;
  tp1RrRatio: number;
}

export interface RawStrategiesView {
  aggressiveness: string;
  strategies: RawStrategy[];
}

// StrategyEdit carries only the fields the user changed (all optional).
export interface StrategyEdit {
  name: string;
  enabled?: boolean;
  riskPctPerTrade?: number;
  maxPositionSizePct?: number;
  dedupWindowSeconds?: number;
  confirmationCandles?: number;
  requireBiasAlignment?: boolean;
  stopLossFixedPct?: number;
  tp1RrRatio?: number;
}

export interface EditRequest {
  aggressiveness?: string;
  strategies?: StrategyEdit[];
}

// ─── Config file editing ─────────────────────────────────────────────────────

export interface ConfigFileDTO {
  name: string;
  content: string;
}

export interface ConfigFileSaveRequest {
  content: string;
}

// ─── Config Form Types ───────────────────────────────────────────────────────

export interface ConfigFormField {
  name: string;
  label: string;
  type: string; // string, number, bool, select, text
  value: string | number | boolean | null;
  options?: string[]; // for select type
  description?: string;
  required?: boolean;
  min?: number;
  max?: number;
  step?: number;
  comment?: string;
}

export interface ConfigFormSection {
  title: string;
  description?: string;
  key: string;
  fields: ConfigFormField[];
  sections?: ConfigFormSection[];
}

export interface ConfigFormSchema {
  file: string;
  sections: ConfigFormSection[];
}

export interface Account {
  venue: string;
  marginMode: string;
  totalEquity: string;
  availableBalance: string;
  initialMargin: string;
  maintenanceMargin: string;
  marginBalance: string;
  updatedAt: number;
}

export interface Snapshot {
  quotes: Quote[];
  positions: Position[];
  proposals: Proposal[];
  logs: LogLine[];
  agentRuns: AgentRun[];
  bias: Bias[];
  opportunities: Opportunity[];
  macro: Macro | null;
  news: NewsItem[];
  budget: Budget | null;
  heartbeat: string;
  config: Config | null;
  recentIntents: OrderIntent[];
  recentTrades: Trade[];
  account?: Account | null;
}

// OrderIntent is the JSON DTO for one row in the order-history intent log.
export interface OrderIntent {
  id: string;
  correlationId: string;
  symbol: string;
  venue: string;
  side: string;
  orderType: string;
  quantity: string;
  limitPrice?: string;
  stopPrice?: string;
  stopLoss?: string;
  takeProfit1?: string;
  timeInForce?: string;
  reduceOnly: boolean;
  positionSide?: string;
  leverage: number;
  strategy: string;
  environment: string;
  status: string; // pending|submitted|filled|rejected|cancelled
  createdAt: number;
}

// Trade is the JSON DTO for one closed/filled trade. pnl/closedAt are
// present only once the position has fully closed.
export interface Trade {
  id: string;
  intentId: string;
  correlationId: string;
  symbol: string;
  side: string;
  quantity: string;
  fillPrice: string;
  fees: string;
  pnl?: string;
  strategy: string;
  venue: string;
  closedAt?: number;
  createdAt: number;
}

// History is the response shape of GET /api/history.
export interface HistoryResponse {
  intents: OrderIntent[];
  trades: Trade[];
}

// Envelope is the WebSocket frame: a type tag + one payload.
export interface Envelope {
  type: string;
  data: unknown;
}
