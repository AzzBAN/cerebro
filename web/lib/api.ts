// REST client for the Go web adapter. The bearer token is read from
// localStorage (set on the login/token screen) and sent on every request.

import type { Snapshot, RawStrategiesView, EditRequest, ConfigFormSchema, HistoryResponse } from "./types";

const TOKEN_KEY = "cerebro_token";

export function getToken(): string {
  if (typeof window === "undefined") return "";
  return window.localStorage.getItem(TOKEN_KEY) ?? "";
}

export function setToken(token: string): void {
  window.localStorage.setItem(TOKEN_KEY, token);
}

function authHeaders(): HeadersInit {
  const t = getToken();
  return t ? { Authorization: `Bearer ${t}` } : {};
}

export async function getState(): Promise<Snapshot> {
  const res = await fetch("/api/state", { headers: authHeaders() });
  if (!res.ok) throw new Error(`state ${res.status}`);
  return res.json();
}

export interface TradeRow {
  id: string;
  symbol: string;
  side: string;
  quantity: string;
  fillPrice: string;
  fees: string;
  pnl?: string;
  strategy: string;
  venue: string;
  createdAt: number;
}

export async function getTrades(fromMs?: number, toMs?: number): Promise<TradeRow[]> {
  const params = new URLSearchParams();
  if (fromMs) params.set("from", String(fromMs));
  if (toMs) params.set("to", String(toMs));
  const res = await fetch(`/api/trades?${params}`, { headers: authHeaders() });
  if (!res.ok) throw new Error(`trades ${res.status}`);
  return res.json();
}
export async function getHistory(fromMs?: number, toMs?: number): Promise<HistoryResponse> {
  const params = new URLSearchParams();
  if (fromMs) params.set("from", String(fromMs));
  if (toMs) params.set("to", String(toMs));
  const res = await fetch(`/api/history?${params}`, { headers: authHeaders() });
  if (!res.ok) throw new Error(`history ${res.status}`);
  return res.json();
}


// postCommand forwards a ChatOps command (e.g. "/pause", "/confirm a1b2") to
// the backend dispatcher and returns its text reply.
export async function postCommand(command: string): Promise<string> {
  const res = await fetch("/api/command", {
    method: "POST",
    headers: { ...authHeaders(), "Content-Type": "application/json" },
    body: JSON.stringify({ command }),
  });
  const body = (await res.json()) as { reply?: string; error?: string };
  if (!res.ok) throw new Error(body.error ?? `command ${res.status}`);
  return body.reply ?? "";
}

// confirmProposal accepts a pending SL/TP adjustment, executing it server-side.
export async function confirmProposal(id: string): Promise<void> {
  const res = await fetch(`/api/proposals/${encodeURIComponent(id)}/confirm`, {
    method: "POST",
    headers: authHeaders(),
  });
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(body.error ?? `confirm ${res.status}`);
  }
}

// rejectProposal discards a pending SL/TP adjustment without executing it.
export async function rejectProposal(id: string): Promise<void> {
  const res = await fetch(`/api/proposals/${encodeURIComponent(id)}/reject`, {
    method: "POST",
    headers: authHeaders(),
  });
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(body.error ?? `reject ${res.status}`);
  }
}

// getRawStrategies fetches the pre-scaling editable strategy values used to
// pre-fill the config edit form.
export async function getRawStrategies(): Promise<RawStrategiesView> {
  const res = await fetch("/api/strategies/raw", { headers: authHeaders() });
  if (!res.ok) throw new Error(`strategies ${res.status}`);
  return res.json();
}

// postStrategies submits edited strategy config. On a validation failure the
// backend leaves files + engine unchanged and returns the validator message.
export async function postStrategies(req: EditRequest): Promise<void> {
  const res = await fetch("/api/strategies", {
    method: "POST",
    headers: { ...authHeaders(), "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(body.error ?? `save ${res.status}`);
  }
}

// ─── Config file editing ─────────────────────────────────────────────────────

import type { ConfigFileDTO, ConfigFileSaveRequest } from "./types";

// getConfigFiles returns the list of editable config file names.
export async function getConfigFiles(): Promise<string[]> {
  const res = await fetch("/api/configs", { headers: authHeaders() });
  if (!res.ok) throw new Error(`configs ${res.status}`);
  const body = (await res.json()) as { files: string[] };
  return body.files;
}

// getConfigFile returns the raw content of one config file.
export async function getConfigFile(name: string): Promise<ConfigFileDTO> {
  const res = await fetch(`/api/configs/${encodeURIComponent(name)}`, {
    headers: authHeaders(),
  });
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(body.error ?? `get ${name} ${res.status}`);
  }
  return res.json();
}

// saveConfigFile writes the raw content to a config file and hot-reloads.
// On validation failure the backend leaves the file + engine unchanged.
export async function saveConfigFile(
  name: string,
  content: string
): Promise<void> {
  const req: ConfigFileSaveRequest = { content };
  const res = await fetch(`/api/configs/${encodeURIComponent(name)}`, {
    method: "PUT",
    headers: { ...authHeaders(), "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(body.error ?? `save ${name} ${res.status}`);
  }
}

// getConfigFormSchema returns the form schema for a config file.
export async function getConfigFormSchema(
  name: string
): Promise<ConfigFormSchema> {
  const res = await fetch(
    `/api/configs/${encodeURIComponent(name)}/form/schema`,
    { headers: authHeaders() }
  );
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(body.error ?? `get form schema ${name} ${res.status}`);
  }
  return res.json();
}

// getConfigFormValues returns the current field values for a config file.
export async function getConfigFormValues(
  name: string
): Promise<Record<string, unknown>> {
  const res = await fetch(`/api/configs/${encodeURIComponent(name)}/form`, {
    headers: authHeaders(),
  });
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(body.error ?? `get form values ${name} ${res.status}`);
  }
  return res.json();
}

// saveConfigFormValues applies field changes and hot-reloads the engine.
export async function saveConfigFormValues(
  name: string,
  fields: Record<string, unknown>
): Promise<void> {
  const res = await fetch(`/api/configs/${encodeURIComponent(name)}/form`, {
    method: "PUT",
    headers: { ...authHeaders(), "Content-Type": "application/json" },
    body: JSON.stringify(fields),
  });
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(body.error ?? `save form ${name} ${res.status}`);
  }
}

// ─── Backtest ────────────────────────────────────────────────────────────────

export interface BacktestRequest {
  symbol: string;
  strategy: string;
  timeframe: string;
  from: string;
  to: string;
  agent_mode?: string;
  max_positions?: number;
}

export interface BacktestCandle {
  time: number; // unix timestamp in seconds
  open: number;
  high: number;
  low: number;
  close: number;
  volume: number;
}

export interface BacktestTrade {
  id: string;
  symbol: string;
  side: string;
  quantity: number;
  fill_price: number;
  pnl?: number;
  created_at: string;
  closed_at?: string;
}

export interface AgentDecision {
  timestamp: string;
  symbol: string;
  strategy: string;
  signal_side: string;
  price: number;
  signal_reason: string;
  action: string;
  rationale: string;
  stop_loss?: number;
  take_profit?: number;
  outcome_pnl?: number;
  verdict: string;
}

export interface BacktestReport {
  strategy: string;
  symbol: string;
  from: string;
  to: string;
  total_trades: number;
  win_rate_pct: number;
  max_drawdown_pct: number;
  sharpe_ratio: number;
  profit_factor: number;
  net_pnl_usdt: number;
  avg_win_usdt: number;
  avg_loss_usdt: number;
  generated_at: string;

  // MT5 Strategy Tester Results
  history_quality?: string;
  bars?: number;
  ticks?: number;
  symbols_count?: number;
  gross_profit?: number;
  gross_loss?: number;
  balance_dd_absolute?: number;
  balance_dd_maximal?: number;
  balance_dd_maximal_pct?: number;
  balance_dd_relative_pct?: number;
  balance_dd_relative?: number;
  equity_dd_absolute?: number;
  equity_dd_maximal?: number;
  equity_dd_maximal_pct?: number;
  equity_dd_relative_pct?: number;
  equity_dd_relative?: number;
  expected_payoff?: number;
  margin_level_pct?: number;
  recovery_factor?: number;
  z_score?: number;
  z_score_prob_pct?: number;
  ahpr?: number;
  ahpr_pct?: number;
  ghpr?: number;
  ghpr_pct?: number;
  lr_correlation?: number;
  lr_standard_error?: number;
  on_tester_result?: number;
  total_deals?: number;
  short_trades?: number;
  short_trades_won_pct?: number;
  long_trades?: number;
  long_trades_won_pct?: number;
  profit_trades?: number;
  profit_trades_pct?: number;
  loss_trades?: number;
  loss_trades_pct?: number;
  largest_profit_trade?: number;
  largest_loss_trade?: number;
  average_profit_trade?: number;
  average_loss_trade?: number;
  max_consecutive_wins?: number;
  max_consecutive_wins_usd?: number;
  max_consecutive_losses?: number;
  max_consecutive_losses_usd?: number;
  maximal_consecutive_profit?: number;
  maximal_consecutive_profit_count?: number;
  maximal_consecutive_loss?: number;
  maximal_consecutive_loss_count?: number;
  avg_consecutive_wins?: number;
  avg_consecutive_losses?: number;
  balance_curve?: number[];
}

export interface BacktestResult {
  report: BacktestReport;
  candles: BacktestCandle[];
  trades: BacktestTrade[];
  decisions: AgentDecision[];
}

export async function runBacktest(req: BacktestRequest): Promise<BacktestResult> {
  const res = await fetch("/api/backtest/run", {
    method: "POST",
    headers: { ...authHeaders(), "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(err.error || `Backtest failed (${res.status})`);
  }
  return res.json();
}
