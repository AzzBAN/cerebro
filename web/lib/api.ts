// REST client for the Go web adapter. The bearer token is read from
// localStorage (set on the login/token screen) and sent on every request.

import type { Snapshot } from "./types";

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
