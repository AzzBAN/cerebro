import React from "react";
import { useStore } from "../lib/store";
import type { Trade } from "../lib/types";

function fmt(n?: string | null): string {
  if (n == null) return "—";
  try {
    const f = parseFloat(n);
    if (!isFinite(f)) return n;
    return f.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 6 });
  } catch {
    return n;
  }
}

function fmtTime(ms: number): string {
  return new Date(ms).toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

function PnLBadge({ pnl }: { pnl?: string }) {
  if (!pnl) return null;
  const v = parseFloat(pnl);
  const color = v >= 0 ? "#34d399" : "#f87171";
  return (
    <span style={{
      color,
      fontSize: 11,
      fontWeight: 700,
      marginLeft: 6,
      background: `${color}22`,
      padding: "1px 5px",
      borderRadius: 4,
    }}>
      {v >= 0 ? "+" : ""}{fmt(pnl)}
    </span>
  );
}

interface TradeChipProps { t: Trade }

function TradeChip({ t }: TradeChipProps) {
  const sideColor = t.side === "buy" ? "#34d399" : "#f87171";
  return (
    <div style={{
      display: "flex",
      alignItems: "center",
      gap: 8,
      padding: "6px 10px",
      background: "#1e293b",
      borderRadius: 8,
      border: "1px solid #334155",
      minWidth: 200,
      flexShrink: 0,
    }}>
      <span style={{
        width: 8, height: 8, borderRadius: "50%",
        background: sideColor,
        flexShrink: 0,
      }} />
      <span style={{ color: sideColor, fontWeight: 700, fontSize: 12 }}>
        {t.side?.toUpperCase()}
      </span>
      <span style={{ color: "#e2e8f0", fontWeight: 600, fontSize: 13 }}>
        {t.symbol}
      </span>
      <span style={{ color: "#94a3b8", fontSize: 11 }}>
        ×{fmt(t.quantity)}
      </span>
      <span style={{ color: "#e2e8f0", fontSize: 12 }}>
        @ {fmt(t.fillPrice)}
      </span>
      <span style={{ color: "#64748b", fontSize: 10 }}>
        {fmtTime(t.createdAt)}
      </span>
      <span style={{ color: "#475569", fontSize: 10 }}>
        {t.venue}
      </span>
      <PnLBadge pnl={t.pnl} />
    </div>
  );
}

export function RecentTrades() {
  const recentTrades = useStore((s) => s.recentTrades);

  // Show newest-last so the strip scrolls right as fills arrive (newest right).
  const chips = [...recentTrades].reverse().slice(-8);

  if (chips.length === 0) {
    return (
      <div style={{
        display: "flex",
        alignItems: "center",
        gap: 8,
        padding: "8px 12px",
        color: "#475569",
        fontSize: 12,
        fontStyle: "italic",
      }}>
        <span style={{ width: 8, height: 8, borderRadius: "50%", background: "#334155", display: "inline-block" }} />
        No fills yet — orders will appear here when they execute
      </div>
    );
  }

  return (
    <div style={{
      display: "flex",
      gap: 8,
      overflowX: "auto",
      padding: "4px 0",
      scrollbarWidth: "thin",
      scrollbarColor: "#334155 transparent",
    }}>
      {chips.map((t) => (
        <TradeChip key={t.id} t={t} />
      ))}
    </div>
  );
}
