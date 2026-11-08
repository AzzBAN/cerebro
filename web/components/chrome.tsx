"use client";

import { useEffect, useState, useRef } from "react";
import { useStore } from "@/lib/store";
import { fmtClock, signClass } from "@/lib/format";
import { postCommand } from "@/lib/api";

export type Tab = "dashboard" | "market" | "logs" | "agents" | "configs" | "history" | "backtest";

const TABS: { id: Tab; icon: string; label: string }[] = [
  { id: "dashboard", icon: "◈", label: "Dashboard" },
  { id: "market", icon: "◉", label: "Market" },
  { id: "logs", icon: "▤", label: "Logs" },
  { id: "agents", icon: "⚙", label: "Agents" },
  { id: "history", icon: "⟨⟩", label: "Orders" },
  { id: "configs", icon: "☰", label: "Config" },
  { id: "backtest", icon: "∿", label: "Backtest" },
];

/** Header is the top bar: brand, live clock, budget chip, connection dot. */
export function Header() {
  const connected = useStore((s) => s.connected);
  const budget = useStore((s) => s.budget);
  const config = useStore((s) => s.config);
  const positions = useStore((s) => s.positions);
  const [now, setNow] = useState(() => new Date());
  const [flattenConfirm, setFlattenConfirm] = useState(false);
  const [flattenStatus, setFlattenStatus] = useState<string | null>(null);
  const flattenTimer = useRef<NodeJS.Timeout>();

  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), 1000);
    return () => clearInterval(t);
  }, []);

  const env = config?.environment ?? "paper";
  const aggr = config?.aggressiveness?.toLowerCase() ?? "balanced";

  const totalUnrealizedPnl = positions.reduce(
    (acc, p) => acc + (Number(p.unrealizedPnl) || 0),
    0
  );

  const handleFlatten = async () => {
    if (!flattenConfirm) {
      setFlattenConfirm(true);
      flattenTimer.current = setTimeout(() => setFlattenConfirm(false), 8000);
      return;
    }
    clearTimeout(flattenTimer.current);
    setFlattenConfirm(false);
    setFlattenStatus("flattening…");
    try {
      await postCommand("/flatten");
      const res = await postCommand("/flatten");
      setFlattenStatus(res.includes("❌") ? "err" : "sent");
      setTimeout(() => setFlattenStatus(null), 3000);
    } catch {
      setFlattenStatus("err");
      setTimeout(() => setFlattenStatus(null), 3000);
    }
  };

  return (
    <header className="flex h-8 shrink-0 items-center justify-between border-b border-border bg-bg-alt px-3">
      <div className="flex items-center gap-2">
        <span className="font-bold text-accent">◈ CEREBRO</span>
        <span className={`${ENV_BADGE[env] ?? ENV_BADGE.paper} px-1.5 text-2xs font-bold uppercase`}>
          {env}
        </span>
        <span className={`${AGGR_BADGE[aggr] ?? AGGR_BADGE.balanced} px-1.5 text-2xs font-bold uppercase`}>
          {aggr}
        </span>
        {positions.length > 0 && (
          <span className={`px-1.5 text-2xs font-bold ${signClass(totalUnrealizedPnl)}`}>
            uPnL: {totalUnrealizedPnl >= 0 ? "+" : ""}${totalUnrealizedPnl.toFixed(2)} ({positions.length} pos)
          </span>
        )}
      </div>
      <div className="text-fg-dim">{fmtClock(now)}</div>
      <div className="flex items-center gap-3">
        {positions.length > 0 && (
          <button
            onClick={handleFlatten}
            className={`border px-1.5 py-0.5 text-2xs font-bold transition-colors ${
              flattenConfirm
                ? "border-bear bg-bear text-bg font-extrabold"
                : "border-bear/60 text-bear hover:bg-bear/20"
            }`}
            title="Emergency flatten all open positions"
          >
            {flattenStatus ? flattenStatus : flattenConfirm ? "CONFIRM FLATTEN?" : "FLATTEN"}
          </button>
        )}
        {budget && budget.costBudgetUsd > 0 && (
          <span className="text-2xs text-fg-dim">
            ${budget.costUsd.toFixed(2)} / ${budget.costBudgetUsd.toFixed(2)} ·{" "}
            {(budget.tokensUsed / 1000).toFixed(1)}k tok
          </span>
        )}
        <span className={connected ? "text-bull" : "text-bear"}>
          ● {connected ? "LIVE" : "OFFLINE"}
        </span>
      </div>
    </header>
  );
}

const ENV_BADGE: Record<string, string> = {
  paper: "bg-warn/20 text-warn",
  demo: "bg-accent/20 text-accent",
  live: "bg-bear/20 text-bear",
};

const AGGR_BADGE: Record<string, string> = {
  conservative: "bg-bull/20 text-bull",
  balanced: "bg-fg-dim/20 text-fg",
  aggressive: "bg-bear/20 text-bear",
};

/** TabBar switches between the four top-level views. */
export function TabBar({ active, onChange }: { active: Tab; onChange: (t: Tab) => void }) {
  return (
    <nav className="flex h-7 shrink-0 items-center border-b border-border bg-bg">
      {TABS.map((t) => (
        <button
          key={t.id}
          onClick={() => onChange(t.id)}
          className={`h-full px-3 ${
            active === t.id
              ? "border-b-2 border-accent font-bold text-fg"
              : "text-fg-dim hover:text-fg"
          }`}
        >
          {t.icon} {t.label}
        </button>
      ))}
    </nav>
  );
}

/** StatusBar shows the engine heartbeat string. */
export function StatusBar() {
  const heartbeat = useStore((s) => s.heartbeat);
  return (
    <footer className="flex h-6 shrink-0 items-center border-t border-border bg-bg-alt px-3 text-2xs text-fg-dim">
      {heartbeat || "awaiting heartbeat…"}
    </footer>
  );
}
