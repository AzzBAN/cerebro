"use client";

import { useEffect, useState } from "react";
import { useStore } from "@/lib/store";
import { fmtClock } from "@/lib/format";

export type Tab = "dashboard" | "market" | "logs" | "agents";

const TABS: { id: Tab; icon: string; label: string }[] = [
  { id: "dashboard", icon: "◈", label: "Dashboard" },
  { id: "market", icon: "◉", label: "Market" },
  { id: "logs", icon: "▤", label: "Logs" },
  { id: "agents", icon: "⚙", label: "Agents" },
];

/** Header is the top bar: brand, live clock, budget chip, connection dot. */
export function Header() {
  const connected = useStore((s) => s.connected);
  const budget = useStore((s) => s.budget);
  const [now, setNow] = useState(() => new Date());

  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), 1000);
    return () => clearInterval(t);
  }, []);

  return (
    <header className="flex h-8 shrink-0 items-center justify-between border-b border-border bg-bg-alt px-3">
      <div className="flex items-center gap-2">
        <span className="font-bold text-accent">◈ CEREBRO</span>
        <span className="bg-warn/20 px-1.5 text-2xs font-bold uppercase text-warn">paper</span>
      </div>
      <div className="text-fg-dim">{fmtClock(now)}</div>
      <div className="flex items-center gap-3">
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
