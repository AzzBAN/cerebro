"use client";

import { useState, useEffect, useRef } from "react";
import { useStore } from "@/lib/store";
import { Panel, BiasPill, LevelBadge } from "./shared";
import { fmtPrice, fmtPct, fmtUsdCompact, signClass, fmtAge } from "@/lib/format";
import { confirmProposal, rejectProposal, getRawStrategies, postStrategies, postCommand } from "@/lib/api";
import type { Proposal, RawStrategy, RawStrategiesView, EditRequest, Position } from "@/lib/types";

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
          <PositionCard key={`${p.symbol}-${i}`} position={p} />
        ))}
        {positions.length === 0 && <div className="text-fg-dim">no open positions</div>}
      </div>
    </Panel>
  );
}

function PositionCard({ position: p }: { position: Position }) {
  const [confirming, setConfirming] = useState(false);
  const [closing, setClosing] = useState(false);
  const timer = useRef<NodeJS.Timeout>();

  const handleClose = async () => {
    if (!confirming) {
      setConfirming(true);
      timer.current = setTimeout(() => setConfirming(false), 8000);
      return;
    }
    clearTimeout(timer.current);
    setConfirming(false);
    setClosing(true);
    try {
      await postCommand(`/close ${p.symbol}`);
      await postCommand(`/close ${p.symbol}`);
    } catch {
      setClosing(false);
    }
  };

  const isLong = p.side?.toUpperCase() === "BUY";

  return (
    <div className={`border-l-2 ${isLong ? "border-bull" : "border-bear"} bg-bg/40 p-1.5 pl-2`}>
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-1.5">
          <span className="font-bold text-fg">{p.symbol}</span>
          <span className={`px-1 text-2xs font-bold uppercase ${isLong ? "bg-bull/20 text-bull" : "bg-bear/20 text-bear"}`}>
            {isLong ? "LONG" : "SHORT"}
          </span>
          {p.leverage > 1 && <span className="bg-fg-dim/20 px-1 text-2xs text-fg-dim">{p.leverage}x</span>}
        </div>
        <div className="flex items-center gap-2">
          <span className={`font-mono text-xs font-bold ${signClass(p.unrealizedPnl)}`}>
            {Number(p.unrealizedPnl) > 0 ? "+" : ""}
            {fmtPrice(p.unrealizedPnl)} ({fmtPct(p.pnlRoi)})
          </span>
          <button
            onClick={handleClose}
            disabled={closing}
            className={`border px-1.5 py-0.5 text-2xs font-bold transition-colors ${
              confirming
                ? "border-bear bg-bear text-bg font-extrabold"
                : "border-bear/60 text-bear hover:bg-bear/20"
            }`}
            title="Close position via ChatOps"
          >
            {closing ? "closing…" : confirming ? "CONFIRM CLOSE?" : "CLOSE"}
          </button>
        </div>
      </div>
      <div className="mt-1 flex items-center justify-between text-2xs text-fg-dim">
        <span>entry <span className="text-fg">{fmtPrice(p.entryPrice)}</span></span>
        <span>mark <span className="text-fg">{fmtPrice(p.currentPrice)}</span></span>
        <span>qty <span className="text-fg">{p.quantity}</span></span>
      </div>
      <div className="mt-0.5 flex items-center justify-between text-2xs text-fg-dim">
        <span>SL: <span className="text-bear">{fmtPrice(p.stopLoss)}</span></span>
        <span>TP: <span className="text-bull">{fmtPrice(p.takeProfit1)}</span></span>
      </div>
    </div>
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

/** Opportunities lists the screening agent's ranked cross-symbol entry setups. */
export function Opportunities() {
  const opps = useStore((s) => s.opportunities);
  return (
    <Panel title="Top Opportunities" color="accent" scroll right={opps.length > 0 ? `${opps.length} ranked` : undefined}>
      <div className="space-y-1.5">
        {opps.map((o) => {
          const buy = o.side?.toUpperCase() === "BUY";
          const label = o.avoided ? `AVOIDED ${o.side?.toUpperCase()}` : o.side?.toUpperCase();
          return (
            <div
              key={o.id}
              className={`border-l-2 pl-2 ${o.avoided ? "border-fg-dim/40" : buy ? "border-bull/50" : "border-bear/50"}`}
            >
              <div className="flex items-center justify-between">
                <span className="font-bold text-fg">
                  {o.symbol}{" "}
                  <span className={o.avoided ? "text-fg-dim" : buy ? "text-bull" : "text-bear"}>{label}</span>
                </span>
                <span className="text-2xs text-fg-dim">{Math.round(o.confidence * 100)}%</span>
              </div>
              {o.reasoning && <div className="line-clamp-2 text-2xs text-fg-dim">{o.reasoning}</div>}
            </div>
          );
        })}
        {opps.length === 0 && <div className="text-fg-dim">no opportunities yet</div>}
      </div>
    </Panel>
  );
}

/** SystemLog renders the combined system/agent/order log with level badges.
 *  Auto-scrolls to latest entry; pauses when the operator scrolls up to read
 *  history and resumes the moment they return to the bottom. */
export function SystemLog() {
  const logs = useStore((s) => s.logs);
  const scrollRef = useRef<HTMLDivElement>(null);
  const stickRef = useRef(true);

  // Track whether the operator is near the bottom. We re-arm auto-scroll the
  // instant they scroll back down, so the latest line always reappears.
  const onScroll = () => {
    const el = scrollRef.current;
    if (!el) return;
    stickRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 32;
  };

  useEffect(() => {
    const el = scrollRef.current;
    if (!el || !stickRef.current) return;
    el.scrollTop = el.scrollHeight;
  }, [logs]);

  return (
    <Panel title="System Log" color="warn" right={`${logs.length} lines`}>
      <div ref={scrollRef} onScroll={onScroll} className="scroll-y h-full">
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
      </div>
    </Panel>
  );
}

const aggrColor: Record<string, string> = {
  conservative: "text-bull",
  balanced: "text-fg",
  aggressive: "text-bear",
};

const AGGR_LEVELS = ["conservative", "balanced", "aggressive"];

/** ConfigPanel shows the active aggressiveness dial and effective per-strategy
 *  params (read-only), and lets the operator edit the raw values and apply them
 *  live via POST /api/strategies. */
export function ConfigPanel() {
  const config = useStore((s) => s.config);
  const [editing, setEditing] = useState(false);
  if (!config) {
    return (
      <Panel title="Config" color="accent" scroll>
        <div className="text-fg-dim">no config data yet</div>
      </Panel>
    );
  }
  const aggr = config.aggressiveness?.toLowerCase() ?? "balanced";
  return (
    <Panel
      title="Config"
      color="accent"
      scroll
      right={
        <button
          onClick={() => setEditing((v) => !v)}
          className="border border-border px-1.5 text-accent hover:bg-accent/10"
        >
          {editing ? "cancel" : "edit"}
        </button>
      }
    >
      {editing ? (
        <ConfigEditor onDone={() => setEditing(false)} />
      ) : (
        <div className="space-y-2 text-2xs">
          <div className="flex items-center gap-2">
            <span className="text-fg-dim">aggressiveness</span>
            <span className={`font-bold uppercase ${aggrColor[aggr] ?? "text-fg"}`}>{aggr}</span>
            <span className="text-fg-dim">· {config.environment?.toUpperCase()}</span>
          </div>

          <div>
            <div className="mb-0.5 font-bold text-fg-dim">strategies (effective)</div>
            <table className="w-full font-mono">
              <thead>
                <tr className="text-fg-dim">
                  <th className="text-left font-normal">name</th>
                  <th className="text-right font-normal">tf</th>
                  <th className="text-right font-normal">risk%</th>
                  <th className="text-right font-normal">maxpos%</th>
                  <th className="text-right font-normal">dedup</th>
                  <th className="text-right font-normal">conf</th>
                  <th className="text-right font-normal">bias</th>
                  <th className="text-right font-normal">mkts</th>
                </tr>
              </thead>
              <tbody>
                {config.strategies.map((s) => (
                  <tr key={s.name} className="text-fg">
                    <td className="text-left">{s.name}</td>
                    <td className="text-right text-fg-dim">{s.primaryTimeframe}</td>
                    <td className="text-right">{s.riskPctPerTrade.toFixed(2)}</td>
                    <td className="text-right">{s.maxPositionSizePct.toFixed(1)}</td>
                    <td className="text-right">{s.dedupWindowSeconds}s</td>
                    <td className="text-right">{s.confirmationCandles}</td>
                    <td className="text-right">{s.requireBiasAlignment ? "✓" : "·"}</td>
                    <td className="text-right text-fg-dim">{s.markets}</td>
                  </tr>
                ))}
                {config.strategies.length === 0 && (
                  <tr>
                    <td colSpan={8} className="text-fg-dim">
                      no enabled strategies
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>

          <div>
            <div className="mb-0.5 font-bold text-fg-dim">risk caps (hard limits)</div>
            <div className="grid grid-cols-2 gap-x-3 gap-y-0.5 font-mono">
              <Stat label="max drawdown" value={`${config.risk.maxDrawdownPct}%`} />
              <Stat label="max daily loss" value={`${config.risk.maxDailyLossPct}%`} />
              <Stat label="max exposure" value={`${config.risk.maxExposurePct}%`} />
              <Stat label="min equity" value={`$${config.risk.minEquityToTrade}`} />
              <Stat label="max positions" value={`${config.risk.maxOpenPositions}`} />
              <Stat label="per symbol" value={`${config.risk.maxOpenPositionsPerSymbol}`} />
            </div>
          </div>
        </div>
      )}
    </Panel>
  );
}

/** ConfigEditor loads raw (pre-scaling) strategy values, lets the operator edit
 *  the safe subset, and applies them live. Only changed fields are sent. */
function ConfigEditor({ onDone }: { onDone: () => void }) {
  const [view, setView] = useState<RawStrategiesView | null>(null);
  const [draft, setDraft] = useState<RawStrategiesView | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");

  useEffect(() => {
    getRawStrategies()
      .then((v) => {
        setView(v);
        setDraft(structuredClone(v));
      })
      .catch((e) => setErr((e as Error).message));
  }, []);

  if (err && !view) return <div className="text-2xs text-bear">load failed: {err}</div>;
  if (!view || !draft) return <div className="text-2xs text-fg-dim">loading…</div>;

  const setStrat = (name: string, patch: Partial<RawStrategy>) => {
    setDraft((d) =>
      d
        ? { ...d, strategies: d.strategies.map((s) => (s.name === name ? { ...s, ...patch } : s)) }
        : d,
    );
  };

  // Build the request from only the fields that differ from the loaded view.
  const buildRequest = (): EditRequest => {
    const req: EditRequest = {};
    if (draft.aggressiveness !== view.aggressiveness) req.aggressiveness = draft.aggressiveness;
    const edits = [];
    for (const s of draft.strategies) {
      const orig = view.strategies.find((o) => o.name === s.name);
      if (!orig) continue;
      const e: Record<string, unknown> = { name: s.name };
      let changed = false;
      const keys: (keyof RawStrategy)[] = [
        "enabled", "riskPctPerTrade", "maxPositionSizePct", "dedupWindowSeconds",
        "confirmationCandles", "requireBiasAlignment", "stopLossFixedPct", "tp1RrRatio",
      ];
      for (const k of keys) {
        if (s[k] !== orig[k]) {
          e[k] = s[k];
          changed = true;
        }
      }
      if (changed) edits.push(e);
    }
    if (edits.length > 0) req.strategies = edits as unknown as EditRequest["strategies"];
    return req;
  };

  const save = async () => {
    if (busy) return;
    const req = buildRequest();
    if (!req.aggressiveness && !req.strategies) {
      setMsg("no changes");
      return;
    }
    setBusy(true);
    setErr("");
    setMsg("applying…");
    try {
      await postStrategies(req);
      setMsg("saved — applied live");
      setTimeout(onDone, 800);
    } catch (e) {
      setErr((e as Error).message);
      setMsg("");
    } finally {
      setBusy(false);
    }
  };

  const numInput = (val: number, onChange: (n: number) => void, step = "0.05") => (
    <input
      type="number"
      step={step}
      value={val}
      disabled={busy}
      onChange={(e) => onChange(parseFloat(e.target.value))}
      className="w-14 border border-border bg-bg px-1 text-right text-fg outline-none focus:border-accent disabled:opacity-50"
    />
  );

  return (
    <div className="space-y-2 text-2xs">
      <div className="flex items-center gap-2">
        <span className="text-fg-dim">aggressiveness</span>
        <select
          value={draft.aggressiveness}
          disabled={busy}
          onChange={(e) => setDraft({ ...draft, aggressiveness: e.target.value })}
          className="border border-border bg-bg px-1 text-fg outline-none focus:border-accent disabled:opacity-50"
        >
          {AGGR_LEVELS.map((lvl) => (
            <option key={lvl} value={lvl}>{lvl}</option>
          ))}
        </select>
      </div>

      <div className="space-y-1.5">
        {draft.strategies.map((s) => (
          <div key={s.name} className="border-l-2 border-border pl-2">
            <div className="flex items-center justify-between">
              <span className="font-bold text-fg">{s.name}</span>
              <label className="flex items-center gap-1 text-fg-dim">
                enabled
                <input
                  type="checkbox"
                  checked={s.enabled}
                  disabled={busy}
                  onChange={(e) => setStrat(s.name, { enabled: e.target.checked })}
                />
              </label>
            </div>
            <div className="mt-0.5 grid grid-cols-2 gap-x-3 gap-y-0.5 font-mono">
              <Field label="risk%">{numInput(s.riskPctPerTrade, (n) => setStrat(s.name, { riskPctPerTrade: n }))}</Field>
              <Field label="maxpos%">{numInput(s.maxPositionSizePct, (n) => setStrat(s.name, { maxPositionSizePct: n }), "0.5")}</Field>
              <Field label="dedup s">{numInput(s.dedupWindowSeconds, (n) => setStrat(s.name, { dedupWindowSeconds: Math.round(n) }), "1")}</Field>
              <Field label="confirm">{numInput(s.confirmationCandles, (n) => setStrat(s.name, { confirmationCandles: Math.round(n) }), "1")}</Field>
              <Field label="SL %">{numInput(s.stopLossFixedPct, (n) => setStrat(s.name, { stopLossFixedPct: n }))}</Field>
              <Field label="TP rr">{numInput(s.tp1RrRatio, (n) => setStrat(s.name, { tp1RrRatio: n }), "0.1")}</Field>
              <Field label="bias align">
                <input
                  type="checkbox"
                  checked={s.requireBiasAlignment}
                  disabled={busy}
                  onChange={(e) => setStrat(s.name, { requireBiasAlignment: e.target.checked })}
                />
              </Field>
            </div>
          </div>
        ))}
      </div>

      <div className="flex items-center gap-2 border-t border-border pt-2">
        <button
          onClick={save}
          disabled={busy}
          className="border border-accent px-2 py-0.5 text-accent hover:bg-accent/10 disabled:opacity-50"
        >
          save & apply
        </button>
        {msg && <span className="text-bull">{msg}</span>}
        {err && <span className="break-all text-bear">{err}</span>}
      </div>
      <p className="text-2xs text-fg-dim">
        Raw values are written to strategies.yaml; aggressiveness scales them. Invalid edits are
        rejected and nothing changes.
      </p>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between">
      <span className="text-fg-dim">{label}</span>
      {children}
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between">
      <span className="text-fg-dim">{label}</span>
      <span className="text-fg">{value}</span>
    </div>
  );
}
