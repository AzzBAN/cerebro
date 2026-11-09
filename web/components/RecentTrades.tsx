"use client";

import React from "react";
import { useStore } from "@/lib/store";
import { Panel } from "./shared";
import { fmtPrice, fmtNum, signClass } from "@/lib/format";

function fmtTime(ms?: number | null): string {
  if (!ms) return "—";
  return new Date(ms).toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

export function RecentTrades() {
  const recentTrades = useStore((s) => s.recentTrades);
  const trades = [...recentTrades].sort((a, b) => (b.createdAt ?? 0) - (a.createdAt ?? 0)).slice(0, 20);

  return (
    <Panel
      title="Recent Trades"
      color="teal"
      scroll
      right={recentTrades.length > 0 ? `${recentTrades.length} fills` : undefined}
    >
      {trades.length === 0 ? (
        <div className="flex h-full min-h-[60px] items-center justify-center text-xs italic text-fg-dim">
          No fills yet — orders will appear here when they execute
        </div>
      ) : (
        <table className="w-full text-xs">
          <thead className="sticky top-0 bg-panel text-2xs uppercase text-fg-dim">
            <tr className="text-right">
              <th className="py-1 text-left">Time</th>
              <th className="text-left">Symbol</th>
              <th className="text-center">Side</th>
              <th>Price</th>
              <th>Qty</th>
              <th>PnL</th>
            </tr>
          </thead>
          <tbody>
            {trades.map((t) => {
              const isBuy = t.side?.toLowerCase() === "buy";
              const pnlVal = t.pnl != null && t.pnl !== "" ? Number(t.pnl) : null;
              return (
                <tr key={t.id} className="border-t border-border/40 text-right hover:bg-bg-alt">
                  <td className="py-1 text-left font-mono text-2xs text-fg-dim">{fmtTime(t.createdAt)}</td>
                  <td className="text-left font-bold text-fg">{t.symbol}</td>
                  <td className="text-center">
                    <span
                      className={`px-1 text-2xs font-bold uppercase ${
                        isBuy ? "bg-bull/20 text-bull" : "bg-bear/20 text-bear"
                      }`}
                    >
                      {t.side?.toUpperCase()}
                    </span>
                  </td>
                  <td className="font-mono text-fg">{fmtPrice(t.fillPrice)}</td>
                  <td className="font-mono text-fg-dim">{fmtNum(t.quantity, 4)}</td>
                  <td className={`font-mono font-bold ${pnlVal == null ? "text-fg-dim" : signClass(pnlVal)}`}>
                    {pnlVal != null ? `${pnlVal >= 0 ? "+" : ""}${pnlVal.toFixed(2)}` : "—"}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </Panel>
  );
}
