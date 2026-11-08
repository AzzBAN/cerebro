import React, { useState, useEffect, useCallback } from "react";
import { useStore } from "../lib/store";
import type { OrderIntent, Trade, HistoryResponse } from "../lib/types";
import { getHistory } from "../lib/api";

// ── helpers ──────────────────────────────────────────────────────────────────

function fmt(n?: number | string | null): string {
  if (n == null) return "—";
  const s = typeof n === "string" ? n : String(n);
  try {
    const f = parseFloat(s);
    if (!isFinite(f)) return s;
    return f.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 6 });
  } catch {
    return s;
  }
}

function fmtMs(ms: number): string {
  return new Date(ms).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

const STATUS_COLOR: Record<string, string> = {
  pending:   "#94a3b8",
  submitted: "#60a5fa",
  filled:    "#34d399",
  rejected:  "#f87171",
  cancelled: "#fbbf24",
};

// ── intent row ────────────────────────────────────────────────────────────────

interface IntentRowProps { i: OrderIntent }

function IntentRow({ i }: IntentRowProps) {
  const color = STATUS_COLOR[i.status] ?? "#94a3b8";
  return (
    <tr>
      <td style={{ color }}>{i.status}</td>
      <td>{fmtMs(i.createdAt)}</td>
      <td>{i.symbol}</td>
      <td>{i.side}</td>
      <td>{i.orderType}</td>
      <td>{fmt(i.quantity)}</td>
      <td>{fmt(i.limitPrice)}</td>
      <td>{fmt(i.stopPrice)}</td>
      <td>{fmt(i.stopLoss)}</td>
      <td>{fmt(i.takeProfit1)}</td>
      <td>{i.timeInForce ?? "—"}</td>
      <td>{i.reduceOnly ? "✓" : ""}</td>
      <td>{i.positionSide ?? "—"}</td>
      <td>{i.leverage > 0 ? `${i.leverage}×` : "—"}</td>
      <td>{i.strategy}</td>
      <td>{i.venue}</td>
      <td>{i.id.slice(0, 8)}</td>
    </tr>
  );
}

// ── trade row ─────────────────────────────────────────────────────────────────

interface TradeRowProps { t: Trade }

function TradeRow({ t }: TradeRowProps) {
  const pnlColor = t.pnl
    ? parseFloat(t.pnl) >= 0 ? "#34d399" : "#f87171"
    : "#94a3b8";
  return (
    <tr>
      <td style={{ color: "#60a5fa" }}>trade</td>
      <td>{t.createdAt ? fmtMs(t.createdAt) : "—"}</td>
      <td>{t.symbol}</td>
      <td>{t.side}</td>
      <td>market</td>
      <td>{fmt(t.quantity)}</td>
      <td>{fmt(t.fillPrice)}</td>
      <td>—</td>
      <td>—</td>
      <td>—</td>
      <td>—</td>
      <td>—</td>
      <td>—</td>
      <td>—</td>
      <td>{t.strategy}</td>
      <td>{t.venue}</td>
      <td>
        <span title={t.id}>{t.id.slice(0, 8)}</span>
        {t.pnl && (
          <span style={{ marginLeft: 6, color: pnlColor, fontWeight: 600 }}>
            {parseFloat(t.pnl) >= 0 ? "+" : ""}{fmt(t.pnl)}
          </span>
        )}
      </td>
    </tr>
  );
}

// ── tab state ─────────────────────────────────────────────────────────────────

type Tab = "all" | "intents" | "trades";

// ── main component ─────────────────────────────────────────────────────────────

export function History() {
  // Seed from snapshot / live push
  const recentIntents = useStore((s) => s.recentIntents);
  const recentTrades  = useStore((s) => s.recentTrades);

  // REST-loaded history (user picked a different window)
  const [apiData, setApiData] = useState<HistoryResponse | null>(null);
  const [tab, setTab] = useState<Tab>("all");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Time window picker
  const [windowHours, setWindowHours] = useState(24);

  const loadHistory = useCallback(async (hours: number) => {
    setLoading(true);
    setError(null);
    try {
      const from = Date.now() - hours * 3600 * 1000;
      const data = await getHistory(from, Date.now());
      setApiData(data);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "fetch failed");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { loadHistory(windowHours); }, [windowHours, loadHistory]);

  // Resolve the displayed lists: if the user has a live push, prefer the
  // store (always fresh); fall back to the REST load.
  const intents: OrderIntent[] = apiData?.intents ?? recentIntents;
  const trades:  Trade[]       = apiData?.trades  ?? recentTrades;

  // "all" tab: merge by createdAt, newest first
  type MergedRow = { kind: "intent"; t: number; data: OrderIntent } | { kind: "trade"; t: number; data: Trade };
  const merged: MergedRow[] = React.useMemo(() => {
    const rows: MergedRow[] = [
      ...intents.map((i): MergedRow => ({ kind: "intent", t: i.createdAt, data: i })),
      ...trades.map((t): MergedRow  => ({ kind: "trade",  t: t.createdAt ?? 0, data: t })),
    ];
    return rows.sort((a, b) => b.t - a.t);
  }, [intents, trades]);

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 12, height: "100%" }}>
      {/* ── header ── */}
      <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
        <h2 style={{ margin: 0, fontSize: 18 }}>Order History</h2>
        <select
          value={windowHours}
          onChange={(e) => setWindowHours(Number(e.target.value))}
          style={{ marginLeft: "auto", padding: "4px 8px", fontSize: 13 }}
        >
          <option value={1}>Last 1h</option>
          <option value={6}>Last 6h</option>
          <option value={24}>Last 24h</option>
          <option value={168}>Last 7d</option>
          <option value={720}>Last 30d</option>
        </select>
        <button onClick={() => loadHistory(windowHours)} disabled={loading}
          style={{ padding: "4px 12px", fontSize: 13, cursor: "pointer" }}>
          {loading ? "…" : "↻"}
        </button>
      </div>

      {/* ── tabs ── */}
      <div style={{ display: "flex", gap: 4 }}>
        {(["all", "intents", "trades"] as Tab[]).map((t) => (
          <button key={t} onClick={() => setTab(t)}
            style={{
              padding: "4px 12px",
              fontSize: 13,
              cursor: "pointer",
              borderBottom: tab === t ? "2px solid #60a5fa" : "2px solid transparent",
              background: "none",
              color: tab === t ? "#60a5fa" : "#94a3b8",
            }}>
            {t === "all" ? "All" : t === "intents" ? `Intents (${intents.length})` : `Trades (${trades.length})`}
          </button>
        ))}
      </div>

      {/* ── live indicator ── */}
      <div style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 12, color: "#64748b" }}>
        <span style={{
          width: 8, height: 8, borderRadius: "50%",
          background: recentTrades.length > 0 ? "#34d399" : "#475569",
          display: "inline-block",
        }} />
        {recentTrades.length > 0 ? "Live — watching for new fills" : "No live fills yet"}
        {apiData && (
          <span style={{ marginLeft: 8 }}>
            ↕ REST snapshot loaded ({windowHours}h window)
          </span>
        )}
      </div>

      {error && (
        <div style={{ color: "#f87171", fontSize: 13 }}>Error: {error}</div>
      )}

      {/* ── table ── */}
      <div style={{ flex: 1, overflowY: "auto" }}>
        <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 12 }}>
          <thead>
            <tr style={{ position: "sticky", top: 0, background: "#0f172a", zIndex: 1 }}>
              <th>Type / Status</th>
              <th>Time</th>
              <th>Symbol</th>
              <th>Side</th>
              <th>Order Type</th>
              <th>Quantity</th>
              <th>Limit</th>
              <th>Stop</th>
              <th>SL</th>
              <th>TP1</th>
              <th>TIF</th>
              <th>Reduce</th>
              <th>Pos Side</th>
              <th>Leverage</th>
              <th>Strategy</th>
              <th>Venue</th>
              <th>ID / PnL</th>
            </tr>
          </thead>
          <tbody>
            {(tab === "all"
              ? merged
              : tab === "intents"
                ? intents.sort((a, b) => b.createdAt - a.createdAt)
                : trades.sort((a, b) => (b.createdAt ?? 0) - (a.createdAt ?? 0))
            ).map((item) => {
              const row = item as MergedRow;
              return row.kind === "intent"
                ? <IntentRow key={row.data.id} i={row.data} />
                : <TradeRow  key={row.data.id}      t={row.data} />;
            })}
            {(tab === "intents" ? intents : tab === "trades" ? trades : merged).length === 0 && (
              <tr>
                <td colSpan={17} style={{ textAlign: "center", color: "#64748b", padding: "24px 0" }}>
                  No {tab === "all" ? "orders" : tab === "intents" ? "intents" : "trades"} in this window
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
