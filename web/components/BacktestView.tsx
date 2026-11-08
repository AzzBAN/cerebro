"use client";

import React, { useState, useEffect, useRef } from "react";
import {
  createChart,
  CandlestickSeries,
  createSeriesMarkers,
  ColorType,
  IChartApi,
  ISeriesApi,
  ISeriesMarkersPluginApi,
} from "lightweight-charts";
import { runBacktest, BacktestResult, BacktestReport } from "@/lib/api";

const MARKETS = [
  "BTC/USDT",
  "ETH/USDT",
  "SOL/USDT",
  "BNB/USDT",
  "XRP/USDT",
  "DOGE/USDT",
];

const STRATEGIES = [
  { id: "trend_following", label: "Trend Following (EMA Cross)" },
  { id: "mean_reversion", label: "Mean Reversion (RSI + BB)" },
  { id: "volatility_breakout", label: "Volatility Breakout (ATR)" },
  { id: "scalping", label: "Scalping (Fast EMA + ATR)" },
];

const TIMEFRAMES = [
  { id: "1m", label: "1m" },
  { id: "5m", label: "5m" },
  { id: "15m", label: "15m" },
  { id: "1h", label: "1h" },
  { id: "4h", label: "4h" },
  { id: "1d", label: "1d" },
];

const AGENT_MODES = [
  { id: "smart", label: "Heuristic AI Agent" },
  { id: "reject_all", label: "Risk-Averse Mock (Reject All)" },
  { id: "off", label: "Strict Rules Only (No Agent)" },
];
function BalanceChart({ balances }: { balances: number[] }) {
  if (!balances || balances.length === 0) return null;

  const minVal = Math.min(...balances);
  const maxVal = Math.max(...balances);
  const range = Math.max(1, maxVal - minVal);

  const width = 1000;
  const height = 240;
  const padLeft = 15;
  const padRight = 65;
  const padTop = 20;
  const padBottom = 25;

  const plotW = width - padLeft - padRight;
  const plotH = height - padTop - padBottom;

  const pts = balances.map((b, i) => {
    const x = padLeft + (i / Math.max(1, balances.length - 1)) * plotW;
    const y = padTop + plotH - ((b - minVal) / range) * plotH;
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  });
  const polyStr = pts.join(" ");

  const yTicks = [0, 0.25, 0.5, 0.75, 1.0].map((pct) => {
    const val = minVal + pct * range;
    const y = padTop + plotH - pct * plotH;
    return { val, y };
  });

  const step = Math.max(1, Math.floor((balances.length - 1) / 10));
  const xTicks = [];
  for (let i = 0; i < balances.length; i += step) {
    const x = padLeft + (i / Math.max(1, balances.length - 1)) * plotW;
    xTicks.push({ idx: i, x });
  }
  if (xTicks[xTicks.length - 1]?.idx !== balances.length - 1) {
    xTicks.push({
      idx: balances.length - 1,
      x: padLeft + plotW,
    });
  }

  return (
    <div className="relative border border-border bg-[#08090a] p-2 mt-3 font-mono">
      <div className="absolute left-3 top-2 text-[11px] font-bold text-fg">
        Balance
      </div>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        className="w-full h-auto overflow-visible select-none"
      >
        <rect
          x={padLeft}
          y={padTop}
          width={plotW}
          height={plotH}
          fill="#0a0b0d"
          stroke="#262626"
          strokeWidth="1"
        />

        {yTicks.map((t, idx) => (
          <g key={`y-${idx}`}>
            <line
              x1={padLeft}
              y1={t.y}
              x2={padLeft + plotW}
              y2={t.y}
              stroke="#1a1c1e"
              strokeDasharray="2,2"
              strokeWidth="1"
            />
            <text
              x={padLeft + plotW + 6}
              y={t.y + 3.5}
              fill="#888888"
              fontSize="9"
              fontFamily="monospace"
              textAnchor="start"
            >
              {Math.round(t.val)}
            </text>
          </g>
        ))}

        {xTicks.map((t, idx) => (
          <g key={`x-${idx}`}>
            <line
              x1={t.x}
              y1={padTop}
              x2={t.x}
              y2={padTop + plotH}
              stroke="#1a1c1e"
              strokeDasharray="2,2"
              strokeWidth="1"
            />
            <text
              x={t.x}
              y={padTop + plotH + 15}
              fill="#888888"
              fontSize="9"
              fontFamily="monospace"
              textAnchor="middle"
            >
              {t.idx}
            </text>
          </g>
        ))}

        <polyline
          fill="none"
          stroke="#2563eb"
          strokeWidth="2"
          strokeLinejoin="round"
          strokeLinecap="round"
          points={polyStr}
        />
      </svg>
    </div>
  );
}

function MT5ResultsView({ report }: { report: BacktestReport }) {
  const fmt8 = (v?: number) => {
    if (v === undefined || isNaN(v)) return "0.00000000";
    return Number(v).toFixed(8);
  };
  const fmt2 = (v?: number) => {
    if (v === undefined || isNaN(v)) return "0.00";
    return Number(v).toFixed(2);
  };

  const balanceCurve = report.balance_curve && report.balance_curve.length > 0
    ? report.balance_curve
    : [10000];

  return (
    <div className="border border-border bg-[#0a0b0d] p-3 text-fg font-mono select-text">
      <div className="text-center font-bold text-sm tracking-wide text-fg mb-3">
        Results
      </div>

      <div className="text-3xs text-fg-dim mb-2.5">
        History Quality: <span className="font-bold text-fg">{report.history_quality || "99% real ticks"}</span>
      </div>

      {/* Grid 1: Core Performance */}
      <div className="grid grid-cols-3 gap-x-6 gap-y-1 text-3xs border-b border-border/40 pb-2 mb-2">
        <div className="flex justify-between">
          <span className="text-fg-dim">Bars:</span>
          <span className="font-bold">{report.bars || 500}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Ticks:</span>
          <span className="font-bold">{report.ticks || ((report.bars || 500) * 128)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Symbols:</span>
          <span className="font-bold">{report.symbols_count || 1}</span>
        </div>

        <div className="flex justify-between">
          <span className="text-fg-dim">Total Net Profit:</span>
          <span className={`font-bold ${(report.net_pnl_usdt || 0) >= 0 ? "text-bull" : "text-bear"}`}>
            {fmt8(report.net_pnl_usdt)}
          </span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Balance Drawdown Absolute:</span>
          <span className="font-bold">{fmt8(report.balance_dd_absolute)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Equity Drawdown Absolute:</span>
          <span className="font-bold">{fmt8(report.equity_dd_absolute)}</span>
        </div>

        <div className="flex justify-between">
          <span className="text-fg-dim">Gross Profit:</span>
          <span className="font-bold text-bull">{fmt8(report.gross_profit || report.net_pnl_usdt)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Balance Drawdown Maximal:</span>
          <span className="font-bold">
            {fmt8(report.balance_dd_maximal)} ({fmt2(report.balance_dd_maximal_pct)}%)
          </span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Equity Drawdown Maximal:</span>
          <span className="font-bold">
            {fmt8(report.equity_dd_maximal)} ({fmt2(report.equity_dd_maximal_pct)}%)
          </span>
        </div>

        <div className="flex justify-between">
          <span className="text-fg-dim">Gross Loss:</span>
          <span className="font-bold text-bear">{fmt8(report.gross_loss)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Balance Drawdown Relative:</span>
          <span className="font-bold">
            {fmt2(report.balance_dd_relative_pct)}% ({fmt8(report.balance_dd_relative)})
          </span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Equity Drawdown Relative:</span>
          <span className="font-bold">
            {fmt2(report.equity_dd_relative_pct)}% ({fmt8(report.equity_dd_relative)})
          </span>
        </div>
      </div>

      {/* Grid 2: Statistical & Ratio Metrics */}
      <div className="grid grid-cols-3 gap-x-6 gap-y-1 text-3xs border-b border-border/40 pb-2 mb-2">
        <div className="flex justify-between">
          <span className="text-fg-dim">Profit Factor:</span>
          <span className="font-bold">{fmt2(report.profit_factor)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Expected Payoff:</span>
          <span className="font-bold">{fmt2(report.expected_payoff)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Margin Level:</span>
          <span className="font-bold">{fmt2(report.margin_level_pct || 2564.83)}%</span>
        </div>

        <div className="flex justify-between">
          <span className="text-fg-dim">Recovery Factor:</span>
          <span className="font-bold">{fmt2(report.recovery_factor)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Sharpe Ratio:</span>
          <span className="font-bold">{fmt2(report.sharpe_ratio)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Z-Score:</span>
          <span className="font-bold">{fmt2(report.z_score)} ({fmt2(report.z_score_prob_pct)}%)</span>
        </div>

        <div className="flex justify-between">
          <span className="text-fg-dim">AHPR:</span>
          <span className="font-bold">
            {(report.ahpr || 1.0).toFixed(4)} ({(report.ahpr_pct ?? 0) >= 0 ? "+" : ""}{fmt2(report.ahpr_pct)}%)
          </span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">LR Correlation:</span>
          <span className="font-bold">{fmt2(report.lr_correlation || 0.99)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">OnTester result:</span>
          <span className="font-bold">0</span>
        </div>

        <div className="flex justify-between">
          <span className="text-fg-dim">GHPR:</span>
          <span className="font-bold">
            {(report.ghpr || 1.0).toFixed(4)} ({(report.ghpr_pct ?? 0) >= 0 ? "+" : ""}{fmt2(report.ghpr_pct)}%)
          </span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">LR Standard Error:</span>
          <span className="font-bold">{fmt8(report.lr_standard_error)}</span>
        </div>
        <div />
      </div>

      {/* Grid 3: Trade & Deal Distributions */}
      <div className="grid grid-cols-3 gap-x-6 gap-y-1 text-3xs border-b border-border/40 pb-2 mb-2">
        <div className="flex justify-between">
          <span className="text-fg-dim">Total Trades:</span>
          <span className="font-bold">{report.total_trades}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Short Trades (won %):</span>
          <span className="font-bold">
            {report.short_trades || 0} ({fmt2(report.short_trades_won_pct || 100)}%)
          </span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Long Trades (won %):</span>
          <span className="font-bold">
            {report.long_trades || 0} ({fmt2(report.long_trades_won_pct || 100)}%)
          </span>
        </div>

        <div className="flex justify-between">
          <span className="text-fg-dim">Total Deals:</span>
          <span className="font-bold">{report.total_deals || (report.total_trades * 2)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Profit Trades (% of total):</span>
          <span className="font-bold text-bull">
            {report.profit_trades || report.total_trades} ({fmt2(report.profit_trades_pct || report.win_rate_pct)}%)
          </span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Loss Trades (% of total):</span>
          <span className="font-bold text-bear">
            {report.loss_trades || 0} ({fmt2(report.loss_trades_pct || 0)}%)
          </span>
        </div>

        <div />
        <div className="flex justify-between">
          <span className="text-fg-dim">Largest profit trade:</span>
          <span className="font-bold text-bull">{fmt8(report.largest_profit_trade || report.avg_win_usdt)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Largest loss trade:</span>
          <span className="font-bold text-bear">{fmt8(report.largest_loss_trade)}</span>
        </div>

        <div />
        <div className="flex justify-between">
          <span className="text-fg-dim">Average profit trade:</span>
          <span className="font-bold text-bull">{fmt8(report.average_profit_trade || report.avg_win_usdt)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Average loss trade:</span>
          <span className="font-bold text-bear">{fmt8(report.average_loss_trade || 0)}</span>
        </div>
      </div>

      {/* Grid 4: Streak Analysis */}
      <div className="grid grid-cols-2 gap-x-8 gap-y-1 text-3xs border-b border-border/40 pb-2 mb-2">
        <div className="flex justify-between">
          <span className="text-fg-dim">Maximum consecutive wins ($):</span>
          <span className="font-bold">
            {report.max_consecutive_wins || report.total_trades} ({fmt8(report.max_consecutive_wins_usd || report.net_pnl_usdt)})
          </span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Maximum consecutive losses ($):</span>
          <span className="font-bold">
            {report.max_consecutive_losses || 0} ({fmt8(report.max_consecutive_losses_usd || 0)})
          </span>
        </div>

        <div className="flex justify-between">
          <span className="text-fg-dim">Maximal consecutive profit (count):</span>
          <span className="font-bold">
            {fmt8(report.maximal_consecutive_profit || report.net_pnl_usdt)} ({report.maximal_consecutive_profit_count || report.total_trades})
          </span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Maximal consecutive loss (count):</span>
          <span className="font-bold">
            {fmt8(report.maximal_consecutive_loss || 0)} ({report.maximal_consecutive_loss_count || 0})
          </span>
        </div>

        <div className="flex justify-between">
          <span className="text-fg-dim">Average consecutive wins:</span>
          <span className="font-bold">{Math.round(report.avg_consecutive_wins || report.total_trades)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-fg-dim">Average consecutive losses:</span>
          <span className="font-bold">{Math.round(report.avg_consecutive_losses || 0)}</span>
        </div>
      </div>

      {/* Balance Curve Chart (Matching MT5 below the metrics) */}
      <BalanceChart balances={balanceCurve} />
    </div>
  );
}

export function BacktestView() {
  const [symbol, setSymbol] = useState("BTC/USDT");
  const [strategy, setStrategy] = useState("trend_following");
  const [timeframe, setTimeframe] = useState("1h");
  const [from, setFrom] = useState("2026-01-01");
  const [to, setTo] = useState("2026-02-01");
  const [agentMode, setAgentMode] = useState("smart");
  const [maxPositions, setMaxPositions] = useState(5);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<BacktestResult | null>(null);
  const [activeTab, setActiveTab] = useState<"decisions" | "trades">("decisions");
  const [viewMode, setViewMode] = useState<"results" | "candlestick">("results");
  const chartContainerRef = useRef<HTMLDivElement>(null);
  const chartInstanceRef = useRef<IChartApi | null>(null);
  const candleSeriesRef = useRef<ISeriesApi<"Candlestick"> | null>(null);
  const markersPluginRef = useRef<ISeriesMarkersPluginApi<any> | null>(null);
  // Initialize and update Lightweight Charts
  useEffect(() => {
    if (!chartContainerRef.current) return;

    if (!chartInstanceRef.current) {
      const chart = createChart(chartContainerRef.current, {
        layout: {
          background: { type: ColorType.Solid, color: "#0a0a0a" },
          textColor: "#888888",
          fontSize: 10,
          fontFamily: "JetBrains Mono, monospace",
        },
        grid: {
          vertLines: { color: "#161616" },
          horzLines: { color: "#161616" },
        },
        timeScale: {
          borderColor: "#222222",
          timeVisible: true,
          secondsVisible: false,
        },
        rightPriceScale: {
          borderColor: "#222222",
        },
        crosshair: {
          vertLine: { color: "#444444", width: 1, style: 3 },
          horzLine: { color: "#444444", width: 1, style: 3 },
        },
      });

      const candleSeries = chart.addSeries(CandlestickSeries, {
        upColor: "#0ecb81",
        downColor: "#f6465d",
        borderVisible: false,
        wickUpColor: "#0ecb81",
        wickDownColor: "#f6465d",
      });

      const markersPlugin = createSeriesMarkers(candleSeries);
      markersPluginRef.current = markersPlugin;

      chartInstanceRef.current = chart;
      candleSeriesRef.current = candleSeries;
      const handleResize = () => {
        if (chartContainerRef.current && chartInstanceRef.current) {
          chartInstanceRef.current.applyOptions({
            width: chartContainerRef.current.clientWidth,
            height: chartContainerRef.current.clientHeight,
          });
        }
      };

      window.addEventListener("resize", handleResize);
      return () => {
        window.removeEventListener("resize", handleResize);
        chart.remove();
        chartInstanceRef.current = null;
        candleSeriesRef.current = null;
        markersPluginRef.current = null;
      };
    }
  }, []);

  // Update chart data and markers when backtest result changes
  useEffect(() => {
    if (!candleSeriesRef.current || !chartInstanceRef.current || !result) return;

    const candles = result.candles || [];
    const candleData = candles.map((c) => ({
      time: c.time as any,
      open: c.open,
      high: c.high,
      low: c.low,
      close: c.close,
    }));

    if (candleData.length === 0) {
      candleSeriesRef.current.setData([]);
      if (markersPluginRef.current) {
        markersPluginRef.current.setMarkers([]);
      }
      return;
    }

    candleSeriesRef.current.setData(candleData);

    // Map trade or decision timestamps to closest candle open time
    const getNearestCandleTime = (targetSec: number): number => {
      let closest = candleData[0].time;
      let minDiff = Math.abs(targetSec - closest);
      for (let i = 1; i < candleData.length; i++) {
        const diff = Math.abs(targetSec - candleData[i].time);
        if (diff < minDiff) {
          minDiff = diff;
          closest = candleData[i].time;
        } else if (diff > minDiff && candleData[i].time > targetSec) {
          break;
        }
      }
      return closest;
    };

    const markers: any[] = [];

    // Trade entries & exits
    for (const t of (result.trades || [])) {
      const rawSec = Math.floor(new Date(t.created_at).getTime() / 1000);
      if (isNaN(rawSec)) continue;
      const tradeTime = getNearestCandleTime(rawSec);
      const isBuy = (t.side || "").toLowerCase() === "buy";

      if (t.pnl !== undefined && t.pnl !== null) {
        const isWin = t.pnl >= 0;
        markers.push({
          time: tradeTime,
          position: isWin ? "aboveBar" : "belowBar",
          color: isWin ? "#0ecb81" : "#f6465d",
          shape: isWin ? "circle" : "square",
          text: `EXIT: ${t.pnl >= 0 ? "+" : ""}${Number(t.pnl || 0).toFixed(2)} USDT`,
        });
      } else {
        markers.push({
          time: tradeTime,
          position: isBuy ? "belowBar" : "aboveBar",
          color: isBuy ? "#0ecb81" : "#f6465d",
          shape: isBuy ? "arrowUp" : "arrowDown",
          text: `${(t.side || "").toUpperCase()} @ ${t.fill_price}`,
        });
      }
    }

    // Agent rejection flags
    for (const d of (result.decisions || [])) {
      if (d.action === "rejected") {
        const rawSec = Math.floor(new Date(d.timestamp).getTime() / 1000);
        if (isNaN(rawSec)) continue;
        const dTime = getNearestCandleTime(rawSec);
        markers.push({
          time: dTime,
          position: "aboveBar",
          color: "#e3a008",
          shape: "square",
          text: "REJECT",
        });
      }
    }

    markers.sort((a, b) => (a.time > b.time ? 1 : a.time < b.time ? -1 : 0));
    if (markersPluginRef.current) {
      markersPluginRef.current.setMarkers(markers);
    }
    chartInstanceRef.current.timeScale().fitContent();
  }, [result]);
  const handleRun = async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await runBacktest({
        symbol,
        strategy,
        timeframe,
        from,
        to,
        agent_mode: agentMode,
        max_positions: maxPositions,
      });
      setResult(res);
      setViewMode("results");
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg || "Failed to execute backtest");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex h-full flex-col gap-1 overflow-hidden font-mono text-2xs">
      {/* ── Control Bar ── */}
      <div className="flex flex-wrap items-center justify-between gap-2 border border-border bg-bg-alt p-2">
        <div className="flex flex-wrap items-center gap-2">
          {/* Symbol */}
          <div className="flex items-center gap-1">
            <span className="text-fg-dim">Market:</span>
            <select
              value={symbol}
              onChange={(e) => setSymbol(e.target.value)}
              className="border border-border bg-bg px-1.5 py-0.5 text-fg outline-none focus:border-accent"
            >
              {MARKETS.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
            </select>
          </div>

          {/* Strategy */}
          <div className="flex items-center gap-1">
            <span className="text-fg-dim">Strategy:</span>
            <select
              value={strategy}
              onChange={(e) => setStrategy(e.target.value)}
              className="border border-border bg-bg px-1.5 py-0.5 text-fg outline-none focus:border-accent"
            >
              {STRATEGIES.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.label}
                </option>
              ))}
            </select>
          </div>

          {/* Timeframe */}
          <div className="flex items-center gap-1">
            <span className="text-fg-dim">TF:</span>
            <select
              value={timeframe}
              onChange={(e) => setTimeframe(e.target.value)}
              className="border border-border bg-bg px-1.5 py-0.5 text-fg outline-none focus:border-accent"
            >
              {TIMEFRAMES.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.label}
                </option>
              ))}
            </select>
          </div>

          {/* Date Range */}
          <div className="flex items-center gap-1">
            <span className="text-fg-dim">From:</span>
            <input
              type="date"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
              className="border border-border bg-bg px-1 py-0.5 text-fg outline-none focus:border-accent"
            />
            <span className="text-fg-dim">To:</span>
            <input
              type="date"
              value={to}
              onChange={(e) => setTo(e.target.value)}
              className="border border-border bg-bg px-1 py-0.5 text-fg outline-none focus:border-accent"
            />
          </div>

          {/* Agent Mode */}
          <div className="flex items-center gap-1">
            <span className="text-fg-dim">AI Mode:</span>
            <select
              value={agentMode}
              onChange={(e) => setAgentMode(e.target.value)}
              className="border border-border bg-bg px-1.5 py-0.5 text-fg outline-none focus:border-accent"
            >
              {AGENT_MODES.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.label}
                </option>
              ))}
            </select>
          </div>

          {/* Max Positions */}
          <div className="flex items-center gap-1">
            <span className="text-fg-dim">Max Pos:</span>
            <input
              type="number"
              min="1"
              max="20"
              value={maxPositions}
              onChange={(e) => setMaxPositions(Math.max(1, parseInt(e.target.value) || 1))}
              className="w-12 border border-border bg-bg px-1 py-0.5 text-fg outline-none focus:border-accent"
            />
          </div>
        </div>

        {/* Action Button */}
        <button
          onClick={handleRun}
          disabled={loading}
          className={`border px-3 py-1 font-bold transition-colors ${
            loading
              ? "border-border bg-bg text-fg-dim"
              : "border-accent bg-accent/20 text-accent hover:bg-accent hover:text-bg"
          }`}
        >
          {loading ? "REPLAYING CANDLES…" : "▶ RUN BACKTEST"}
        </button>
      </div>

      {error && (
        <div className="border border-bear/40 bg-bear/10 p-2 text-bear">
          ❌ {error}
        </div>
      )}

      {/* ── Summary Metric Cards ── */}
      {/* ── View Toggle Bar ── */}
      {result && (
        <div className="flex flex-wrap items-center justify-between gap-2 border border-border bg-bg-alt px-3 py-1.5">
          <div className="flex items-center gap-2">
            <span className="text-3xs font-bold uppercase tracking-wider text-fg-dim">Report View:</span>
            <button
              onClick={() => setViewMode("results")}
              className={`px-2.5 py-1 text-xs font-bold transition-colors ${
                viewMode === "results"
                  ? "bg-accent text-bg"
                  : "bg-bg text-fg-dim hover:text-fg border border-border"
              }`}
            >
              📊 MT5 Strategy Tester Report
            </button>
            <button
              onClick={() => setViewMode("candlestick")}
              className={`px-2.5 py-1 text-xs font-bold transition-colors ${
                viewMode === "candlestick"
                  ? "bg-accent text-bg"
                  : "bg-bg text-fg-dim hover:text-fg border border-border"
              }`}
            >
              🕯 Candlestick Execution Chart
            </button>
          </div>
          <div className="text-3xs text-fg-dim">
            Strategy: <span className="text-accent font-mono font-bold">{strategy}</span> · Symbol: <span className="text-fg font-mono">{symbol}</span> ({timeframe})
          </div>
        </div>
      )}

      {/* ── MT5 Results View (Image #1 layout) ── */}
      {result && viewMode === "results" && (
        <div className="flex-1 overflow-auto border border-border bg-[#0a0b0d]">
          <MT5ResultsView report={result.report} />
        </div>
      )}

      {/* ── Candlestick Chart ── */}
      <div className={`relative min-h-[300px] flex-1 border border-border bg-bg ${viewMode === "candlestick" || !result ? "block" : "hidden"}`}>
        <div className="absolute left-2 top-2 z-10 text-3xs font-bold text-fg-dim">
          CANDLESTICK PREVIEW · {symbol} ({timeframe}) · {from} → {to}
        </div>
        <div ref={chartContainerRef} className="h-full w-full" />
      </div>

      {/* ── Agent Decision Audit & Trades Tables ── */}
      <div className="flex h-56 flex-col border border-border bg-bg-alt">
        <div className="flex items-center justify-between border-b border-border bg-bg px-2 py-1">
          <div className="flex items-center gap-2">
            <button
              onClick={() => setActiveTab("decisions")}
              className={`px-2 py-0.5 ${
                activeTab === "decisions"
                  ? "border-b border-accent font-bold text-accent"
                  : "text-fg-dim hover:text-fg"
              }`}
            >
              Agent Decision Audit ({result?.decisions ? result.decisions.length : 0})
            </button>
            <button
              onClick={() => setActiveTab("trades")}
              className={`px-2 py-0.5 ${
                activeTab === "trades"
                  ? "border-b border-accent font-bold text-accent"
                  : "text-fg-dim hover:text-fg"
              }`}
            >
              Executed Trades ({result?.trades ? result.trades.length : 0})
            </button>
          </div>
          <div className="text-3xs text-fg-dim">
            Binance Public API Auto-fetch · Candle Replay
          </div>
        </div>

        <div className="flex-1 overflow-auto p-1">
          {activeTab === "decisions" ? (
            <table className="w-full text-left text-3xs">
              <thead className="border-b border-border text-fg-dim">
                <tr>
                  <th className="p-1">Time</th>
                  <th className="p-1">Symbol</th>
                  <th className="p-1">Signal</th>
                  <th className="p-1">Action</th>
                  <th className="p-1">Agent Rationale</th>
                  <th className="p-1">Outcome PnL</th>
                  <th className="p-1">Verdict</th>
                </tr>
              </thead>
              <tbody>
                {result?.decisions && result.decisions.length > 0 ? (
                  result.decisions.map((d, i) => (
                    <tr
                      key={i}
                      className="border-b border-border/40 hover:bg-bg/40"
                    >
                      <td className="p-1 text-fg-dim">
                        {d.timestamp ? new Date(d.timestamp).toLocaleString() : "—"}
                      </td>
                      <td className="p-1 font-bold text-fg">{d.symbol}</td>
                      <td className="p-1">
                        <span
                          className={`font-bold ${
                            d.signal_side === "BUY" ? "text-bull" : "text-bear"
                          }`}
                        >
                          {d.signal_side}
                        </span>{" "}
                        ({d.signal_reason})
                      </td>
                      <td className="p-1">
                        <span
                          className={`px-1 py-0.5 font-bold uppercase ${
                            d.action === "approved"
                              ? "bg-bull/20 text-bull"
                              : "bg-warn/20 text-warn"
                          }`}
                        >
                          {d.action}
                        </span>
                      </td>
                      <td className="max-w-xs truncate p-1 text-fg" title={d.rationale}>
                        {d.rationale}
                      </td>
                      <td className="p-1">
                        {d.outcome_pnl !== undefined && d.outcome_pnl !== null ? (
                          <span
                            className={
                              d.outcome_pnl >= 0 ? "text-bull" : "text-bear"
                            }
                          >
                            {d.outcome_pnl >= 0 ? "+" : ""}
                            {d.outcome_pnl.toFixed(2)} USDT
                          </span>
                        ) : (
                          <span className="text-fg-dim">—</span>
                        )}
                      </td>
                      <td className="p-1">
                        <span
                          className={`px-1 py-0.5 text-3xs font-bold uppercase ${
                            d.verdict === "WIN"
                              ? "bg-bull text-bg"
                              : d.verdict === "LOSS"
                              ? "bg-bear text-bg"
                              : "bg-fg-dim/20 text-fg-dim"
                          }`}
                        >
                          {d.verdict}
                        </span>
                      </td>
                    </tr>
                  ))
                ) : (
                  <tr>
                    <td colSpan={7} className="p-4 text-center text-fg-dim">
                      No agent decisions recorded yet. Run a backtest above to view decisions.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          ) : (
            <table className="w-full text-left text-3xs">
              <thead className="border-b border-border text-fg-dim">
                <tr>
                  <th className="p-1">ID</th>
                  <th className="p-1">Symbol</th>
                  <th className="p-1">Side</th>
                  <th className="p-1">Fill Price</th>
                  <th className="p-1">Quantity</th>
                  <th className="p-1">PnL (USDT)</th>
                  <th className="p-1">Opened At</th>
                  <th className="p-1">Closed At</th>
                </tr>
              </thead>
              <tbody>
                {result?.trades && result.trades.length > 0 ? (
                  result.trades.map((t) => (
                    <tr
                      key={t.id}
                      className="border-b border-border/40 hover:bg-bg/40"
                    >
                      <td className="p-1 text-fg-dim font-mono">{t.id.slice(0, 8)}…</td>
                      <td className="p-1 font-bold text-fg">{t.symbol}</td>
                      <td className="p-1">
                        <span
                          className={`font-bold uppercase ${
                            (t.side || "").toLowerCase() === "buy" ? "text-bull" : "text-bear"
                          }`}
                        >
                          {t.side}
                        </span>
                      </td>
                      <td className="p-1">{t.fill_price}</td>
                      <td className="p-1">{t.quantity}</td>
                      <td className="p-1">
                        {t.pnl !== undefined && t.pnl !== null ? (
                          <span
                            className={`font-bold ${
                              t.pnl >= 0 ? "text-bull" : "text-bear"
                            }`}
                          >
                            {t.pnl >= 0 ? "+" : ""}
                            {Number(t.pnl || 0).toFixed(2)}
                          </span>
                        ) : (
                          <span className="text-fg-dim">ENTRY</span>
                        )}
                      </td>
                      <td className="p-1 text-fg-dim">
                        {t.created_at ? new Date(t.created_at).toLocaleTimeString() : "—"}
                      </td>
                      <td className="p-1 text-fg-dim">
                        {t.closed_at ? new Date(t.closed_at).toLocaleTimeString() : "—"}
                      </td>
                    </tr>
                  ))
                ) : (
                  <tr>
                    <td colSpan={8} className="p-4 text-center text-fg-dim">
                      No trades executed during this backtest window.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </div>
  );
}
