"use client";

import { useState } from "react";
import { useStore } from "@/lib/store";
import { fmtPrice, fmtUsdCompact } from "@/lib/format";

export function TradingAccount() {
  const account = useStore((s) => s.account);
  const positions = useStore((s) => s.positions);
  const [marginMode, setMarginMode] = useState<"Cross" | "Isolated">("Cross");

  // Derive from active positions if not explicitly supplied by broker
  const derivedInitialMargin = positions.reduce((acc, p) => acc + (Number(p.margin) || 0), 0);
  const totalNotional = positions.reduce((acc, p) => {
    const qty = Number(p.quantity) || 0;
    const price = Number(p.currentPrice) || Number(p.entryPrice) || 0;
    return acc + Math.abs(qty * price);
  }, 0);
  const derivedMaintMargin = totalNotional * 0.005;

  // Prefer real broker account metrics pushed from backend, fallback to paper/positions
  const initialMarginUsd = account && Number(account.initialMargin) > 0
    ? Number(account.initialMargin)
    : derivedInitialMargin;

  const maintenanceMarginUsd = account && Number(account.maintenanceMargin) > 0
    ? Number(account.maintenanceMargin)
    : derivedMaintMargin;

  const marginBalance = account && Number(account.marginBalance) > 0
    ? Number(account.marginBalance)
    : 10000;

  const availableBalance = account && Number(account.availableBalance) >= 0
    ? Number(account.availableBalance)
    : Math.max(0, marginBalance - initialMarginUsd);

  const currentMarginMode = account?.marginMode || marginMode;

  const imRatioPct = marginBalance > 0 ? (initialMarginUsd / marginBalance) * 100 : 0;
  const mmRatioPct = marginBalance > 0 ? (maintenanceMarginUsd / marginBalance) * 100 : 0;


  const fmtCurrency = (val: number) => {
    if (!isFinite(val)) return "0.00";
    return val.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
  };
  return (
    <div className="flex flex-col border border-border bg-panel text-xs">
      {/* Header */}
      <div className="flex items-center justify-between border-b border-border px-3 py-2">
        <div className="flex items-center gap-1.5 font-bold uppercase tracking-wider text-fg">
          <span>Unified Trading Account</span>
          <span className="text-2xs text-fg-dim" title="Privacy enabled">👁</span>
        </div>
        <div className="flex items-center gap-2 text-2xs">
          <span className="cursor-pointer font-bold text-accent hover:underline">
            📊 P&amp;L
          </span>
        </div>
      </div>

      <div className="space-y-2.5 p-3">
        {/* 1. Margin Mode */}
        <div className="flex items-center justify-between">
          <span className="text-fg-dim">Margin Mode</span>
          <button
            onClick={() => setMarginMode((m) => (m === "Cross" ? "Isolated" : "Cross"))}
            className="flex items-center gap-1 border border-border bg-bg-alt px-2 py-0.5 text-2xs font-bold text-fg hover:border-accent"
            title="Toggle Margin Mode"
          >
            <span>{currentMarginMode} Margin</span>
            <span className="text-fg-dim">›</span>
          </button>
        </div>

        {/* 2. Initial Margin */}
        <div>
          <div className="flex items-center justify-between text-2xs">
            <span className="text-fg-dim">Initial Margin</span>
            <span className="font-mono font-bold text-bull">{imRatioPct.toFixed(2)}%</span>
          </div>
          <div className="mt-1 flex items-center gap-2">
            <div className="h-1.5 flex-1 overflow-hidden bg-bg-alt">
              <div
                className="h-full bg-accent transition-all duration-300"
                style={{ width: `${Math.min(100, Math.max(0, imRatioPct))}%` }}
              />
            </div>
            <span className="w-20 text-right font-mono text-2xs text-fg-dim">
              ${fmtCurrency(initialMarginUsd)}
            </span>
          </div>
        </div>

        {/* 3. Maintenance Margin */}
        <div>
          <div className="flex items-center justify-between text-2xs">
            <span className="text-fg-dim">Maintenance Margin</span>
            <span className="font-mono font-bold text-bull">{mmRatioPct.toFixed(2)}%</span>
          </div>
          <div className="mt-1 flex items-center gap-2">
            <div className="h-1.5 flex-1 overflow-hidden bg-bg-alt">
              <div
                className="h-full bg-accent transition-all duration-300"
                style={{ width: `${Math.min(100, Math.max(0, mmRatioPct))}%` }}
              />
            </div>
            <span className="w-20 text-right font-mono text-2xs text-fg-dim">
              ${fmtCurrency(maintenanceMarginUsd)}
            </span>
          </div>
        </div>

        {/* 4. Margin Balance */}
        <div className="flex items-center justify-between border-t border-border/40 pt-2">
          <span className="text-fg-dim">Margin Balance</span>
          <span className="font-mono font-bold text-fg">
            ${fmtCurrency(marginBalance)} <span className="text-2xs text-fg-dim">USDT</span>
          </span>
        </div>

        {/* 5. Available Balance */}
        <div className="flex items-center justify-between">
          <span className="text-fg-dim">Available Balance</span>
          <span className="font-mono font-bold text-accent">
            ${fmtCurrency(availableBalance)} <span className="text-2xs text-fg-dim">USDT</span>
          </span>
        </div>
      </div>
    </div>
  );
}
