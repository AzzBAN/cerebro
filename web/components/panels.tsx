"use client";

import { useState } from "react";
import { useStore } from "@/lib/store";
import { Panel, BiasPill, LevelBadge } from "./shared";
import { fmtPrice, fmtPct, fmtUsdCompact, signClass, fmtAge } from "@/lib/format";
import { confirmProposal, rejectProposal } from "@/lib/api";
import type { Proposal } from "@/lib/types";

/** MarketWatch renders the dense quote table with an optional bias column. */
export function MarketWatch({ withBias = true }: { withBias?: boolean }) {
  const quotes = useStore((s) => s.quotes);
  const bias = useStore((s) => s.bias);
  const rows = Object.values(quotes).sort((a, b) => a.symbol.localeCompare(b.symbol));

  return (
    <Panel title="Market Watch" color="accent" scroll right={`${rows.length} symbols`}>
      <table className="w-full text-xs">
        <thead className="sticky top-0 bg-panel text-2xs uppercase text-fg-dim">
          <tr className="text-right">
            <th className="py-1 text-left">Symbol</th>
            <th>Last</th>
            <th>24h Chg%</th>
            <th>Bid / Ask</th>
            <th>Vol(24h)</th>
            {withBias && <th className="text-center">Bias</th>}
          </tr>
        </thead>
        <tbody>
          {rows.map((q) => {
            const b = bias[q.symbol];
            return (
              <tr key={q.symbol} className="border-t border-border/40 text-right hover:bg-bg-alt">
                <td className="py-1 text-left font-bold text-fg">{q.symbol}</td>
                <td>{fmtPrice(q.last)}</td>
                <td className={signClass(q.priceChangePercent)}>{fmtPct(q.priceChangePercent)}</td>
                <td className="text-fg-dim">
                  {fmtPrice(q.bid)} / {fmtPrice(q.ask)}
                </td>
                <td className="text-fg-dim">{fmtUsdCompact(q.volume24h)}</td>
                {withBias && (
                  <td className="text-center">
                    {b ? <BiasPill score={b.score} label={b.label} /> : <span className="text-fg-dim">—</span>}
                  </td>
                )}
              </tr>
            );
          })}
          {rows.length === 0 && (
            <tr>
              <td colSpan={withBias ? 6 : 5} className="py-3 text-center text-fg-dim">
                waiting for market data…
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </Panel>
  );
}

/** ProposalBlock renders one pending SL/TP adjustment with confirm/reject. */
function ProposalBlock({ p }: { p: Proposal }) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  async function act(fn: (id: string) => Promise<void>) {
    setBusy(true);
    setErr("");
    try {
      await fn(p.id);
      // On success the backend pushes a fresh "proposals" frame that removes
      // this block; no local removal needed.
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  }

  return (
    <div className="border-l-2 border-warn/60 bg-warn/5 pl-2 pr-1 py-1">
      <div className="flex items-center justify-between">
        <span className="font-bold text-warn">
          {p.symbol} · SL/TP ADJUSTMENT
        </span>
        <span className="text-2xs text-fg-dim">{fmtAge(p.createdAt)}</span>
      </div>
      <div className="text-2xs text-fg-dim">
        SL {fmtPrice(p.currentStop)} → <span className="text-fg">{fmtPrice(p.proposedStop)}</span>
        {" · "}TP {fmtPrice(p.currentTp)} → <span className="text-fg">{fmtPrice(p.proposedTp)}</span>
      </div>
      {p.reasoning && <div className="mt-0.5 text-2xs text-fg-dim">{p.reasoning}</div>}
      {err && <div className="mt-0.5 text-2xs text-bear">{err}</div>}
      <div className="mt-1 flex gap-1.5">
        <button
          disabled={busy}
          onClick={() => act(confirmProposal)}
          className="px-2 py-0.5 text-2xs font-bold uppercase bg-bull/20 text-bull hover:bg-bull/30 disabled:opacity-40"
        >
          Confirm
        </button>
        <button
          disabled={busy}
          onClick={() => act(rejectProposal)}
          className="px-2 py-0.5 text-2xs font-bold uppercase bg-bear/20 text-bear hover:bg-bear/30 disabled:opacity-40"
        >
          Reject
        </button>
      </div>
    </div>
  );
}

/** Positions renders open positions with PnL/ROI coloring. */
export function Positions() {
  const positions = useStore((s) => s.positions);
  const proposals = useStore((s) => s.proposals);
  return (
    <Panel title="Active Positions" color="bull" scroll right={proposals.length > 0 ? `${positions.length} open · ${proposals.length} pending` : `${positions.length} open`}>
      <div className="space-y-2">
        {proposals.map((p) => (
          <ProposalBlock key={p.id} p={p} />
        ))}
        {positions.map((p, i) => (
          <div key={`${p.symbol}-${i}`} className="border-l-2 border-bull/40 pl-2">
            <div className="flex items-center justify-between">
              <span className="font-bold text-fg">
                {p.symbol}{" "}
                <span className={p.side === "BUY" ? "text-bull" : "text-bear"}>
                  {p.side === "BUY" ? "LONG" : "SHORT"}
                </span>
                {p.leverage > 1 && <span className="ml-1 text-fg-dim">{p.leverage}x</span>}
              </span>
              <span className={signClass(p.unrealizedPnl)}>
                {Number(p.unrealizedPnl) > 0 ? "+" : ""}
                {fmtPrice(p.unrealizedPnl)} ({fmtPct(p.pnlRoi)})
              </span>
            </div>
            <div className="text-2xs text-fg-dim">
              entry {fmtPrice(p.entryPrice)} · mark {fmtPrice(p.currentPrice)} · qty {p.quantity}
            </div>
            <div className="text-2xs text-fg-dim">
              SL {fmtPrice(p.stopLoss)} · TP {fmtPrice(p.takeProfit1)}
            </div>
          </div>
        ))}
        {positions.length === 0 && <div className="text-fg-dim">no open positions</div>}
      </div>
    </Panel>
  );
}

/** BiasSignals lists the screening agent's directional reads with reasoning. */
export function BiasSignals() {
  const bias = useStore((s) => s.bias);
  const order = useStore((s) => s.biasOrder);
  return (
    <Panel title="Bias / Signals" color="agent" scroll>
      <div className="space-y-1.5">
        {order.map((sym) => {
          const b = bias[sym];
          if (!b) return null;
          return (
            <div key={sym} className="flex items-start gap-2">
              <span className="w-20 shrink-0 font-bold text-fg">{sym}</span>
              <BiasPill score={b.score} label={b.label} />
              <span className="line-clamp-2 text-2xs text-fg-dim">{b.reasoning}</span>
            </div>
          );
        })}
        {order.length === 0 && <div className="text-fg-dim">no bias data yet</div>}
      </div>
    </Panel>
  );
}

/** SystemLog renders the combined system/agent/order log with level badges. */
export function SystemLog() {
  const logs = useStore((s) => s.logs);
  return (
    <Panel title="System Log" color="warn" scroll right={`${logs.length} lines`}>
      <div className="space-y-0.5 font-mono text-2xs">
        {logs.map((l, i) => (
          <div key={i} className="flex gap-2">
            <span className="shrink-0 text-fg-dim">{fmtAge(l.at)}</span>
            <LevelBadge level={l.level} />
            <span className="break-all text-fg">{l.text}</span>
          </div>
        ))}
        {logs.length === 0 && <div className="text-fg-dim">no log output yet</div>}
      </div>
    </Panel>
  );
}
