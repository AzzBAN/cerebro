"use client";

import { useState, useEffect, useCallback, useMemo, useRef } from "react";
import {
  getConfigFiles,
  getConfigFile,
  saveConfigFile,
  getConfigFormSchema,
  getConfigFormValues,
  saveConfigFormValues,
} from "@/lib/api";
import type {
  ConfigFormSchema,
  ConfigFormSection,
  ConfigFormField,
} from "@/lib/types";
import {
  POPULAR_FUTURES_MARKETS,
  POPULAR_SPOT_MARKETS,
  AVAILABLE_STRATEGIES,
  parseMarketsYaml,
  addSymbolToDoc,
  removeSymbolFromDoc,
  updateSymbolInDoc,
  type VenueData,
  type MarketSymbolConfig,
  type CryptoPreset,
} from "@/lib/marketConfig";
import YAML from "yaml";

const FILE_METADATA: Record<string, { label: string; icon: string; desc: string }> = {
  "markets.yaml": {
    label: "markets.yaml",
    icon: "◈",
    desc: "Trading pairs, venues, leverage & per-symbol strategies",
  },
  "app.yaml": {
    label: "app.yaml",
    icon: "⚙",
    desc: "Engine, risk gates, screening agent & system parameters",
  },
  "strategies.yaml": {
    label: "strategies.yaml",
    icon: "⚡",
    desc: "Strategy presets, indicators, stop-loss & take-profit rules",
  },
  "secrets.env": {
    label: "secrets.env",
    icon: "🔒",
    desc: "API credentials, database, redis & chatops bot tokens",
  },
};

type ViewMode = "markets" | "form" | "raw";

// ─── Markets Manager (Interactive view for markets.yaml) ─────────────────────

function MarketsManager({
  yamlContent,
  onYamlChange,
  isDirty,
}: {
  yamlContent: string;
  onYamlChange: (content: string) => void;
  isDirty: boolean;
}) {
  const [selectedVenue, setSelectedVenue] = useState<string>("binance_futures");
  const [searchFilter, setSearchFilter] = useState<string>("");
  const [categoryFilter, setCategoryFilter] = useState<string>("All");
  const [customSymbol, setCustomSymbol] = useState<string>("");
  const [selectedPreset, setSelectedPreset] = useState<string>("");
  const [newLeverage, setNewLeverage] = useState<number>(50);
  const [selectedStrategies, setSelectedStrategies] = useState<string[]>([
    "scalping",
    "mean_reversion",
    "trend_following",
  ]);
  const [showAddModal, setShowAddModal] = useState<boolean>(false);
  const [showDefaults, setShowDefaults] = useState<boolean>(false);
  const [localError, setLocalError] = useState<string>("");

  const parsed = useMemo(() => {
    try {
      return parseMarketsYaml(yamlContent);
    } catch (e: unknown) {
      return { venues: [] as VenueData[], doc: null, error: e instanceof Error ? e.message : String(e) };
    }
  }, [yamlContent]);

  const currentVenue = useMemo(() => {
    return parsed.venues.find((v) => v.venue === selectedVenue) ?? parsed.venues[0];
  }, [parsed.venues, selectedVenue]);

  // Adjust defaults when venue changes
  useEffect(() => {
    if (selectedVenue === "binance_spot") {
      setNewLeverage(1);
    } else {
      setNewLeverage(50);
    }
  }, [selectedVenue]);

  const activeSymbols = currentVenue?.symbols ?? [];

  const filteredSymbols = useMemo(() => {
    if (!searchFilter.trim()) return activeSymbols;
    const q = searchFilter.toLowerCase();
    return activeSymbols.filter((s) => s.symbol.toLowerCase().includes(q));
  }, [activeSymbols, searchFilter]);

  const existingSymbolSet = useMemo(() => {
    return new Set(activeSymbols.map((s) => s.symbol));
  }, [activeSymbols]);

  const popularPresets: CryptoPreset[] = useMemo(() => {
    return selectedVenue === "binance_spot" ? POPULAR_SPOT_MARKETS : POPULAR_FUTURES_MARKETS;
  }, [selectedVenue]);

  const filteredPresets = useMemo(() => {
    return popularPresets.filter((p) => {
      const matchesCat = categoryFilter === "All" || p.category === categoryFilter;
      const notAdded = !existingSymbolSet.has(p.symbol);
      return matchesCat && notAdded;
    });
  }, [popularPresets, categoryFilter, existingSymbolSet]);

  const handleAddSymbol = (sym: string, lev: number, strats: string[]) => {
    setLocalError("");
    if (!parsed.doc) {
      setLocalError("Cannot parse YAML document");
      return;
    }
    const cleanSym = sym.trim().toUpperCase();
    if (!cleanSym) {
      setLocalError("Symbol name cannot be empty");
      return;
    }

    try {
      const updated = addSymbolToDoc(parsed.doc, selectedVenue, {
        symbol: cleanSym,
        leverage: selectedVenue === "binance_spot" ? 1 : lev,
        strategies: strats,
      });
      onYamlChange(updated);
      setCustomSymbol("");
      setSelectedPreset("");
      setShowAddModal(false);
    } catch (e: unknown) {
      setLocalError(e instanceof Error ? e.message : String(e));
    }
  };

  const handleRemoveSymbol = (sym: string) => {
    setLocalError("");
    if (!parsed.doc) return;
    try {
      const updated = removeSymbolFromDoc(parsed.doc, selectedVenue, sym);
      onYamlChange(updated);
    } catch (e: unknown) {
      setLocalError(e instanceof Error ? e.message : String(e));
    }
  };

  const handleToggleStrategy = (sym: string, stratId: string) => {
    setLocalError("");
    if (!parsed.doc) return;
    const target = activeSymbols.find((s) => s.symbol === sym);
    if (!target) return;
    const currentStrats = target.strategies ? [...target.strategies] : [];
    const idx = currentStrats.indexOf(stratId);
    let nextStrats: string[];
    if (idx >= 0) {
      nextStrats = currentStrats.filter((s) => s !== stratId);
    } else {
      nextStrats = [...currentStrats, stratId];
    }
    try {
      const updated = updateSymbolInDoc(parsed.doc, selectedVenue, sym, {
        strategies: nextStrats,
      });
      onYamlChange(updated);
    } catch (e: unknown) {
      setLocalError(e instanceof Error ? e.message : String(e));
    }
  };

  const handleUpdateLeverage = (sym: string, delta: number) => {
    setLocalError("");
    if (!parsed.doc || selectedVenue === "binance_spot") return;
    const target = activeSymbols.find((s) => s.symbol === sym);
    if (!target) return;
    const cur = target.leverage ?? currentVenue?.defaults?.leverage ?? 5;
    const nextLev = Math.max(1, Math.min(125, cur + delta));
    try {
      const updated = updateSymbolInDoc(parsed.doc, selectedVenue, sym, {
        leverage: nextLev,
      });
      onYamlChange(updated);
    } catch (e: unknown) {
      setLocalError(e instanceof Error ? e.message : String(e));
    }
  };

  if ("error" in parsed && parsed.error) {
    return (
      <div className="p-4 text-sm text-bear bg-bear/10 border border-bear rounded">
        <p className="font-bold">Error parsing markets.yaml:</p>
        <p className="font-mono text-xs mt-1">{parsed.error}</p>
        <p className="text-xs text-fg-dim mt-2">
          Switch to Raw YAML mode to inspect or fix syntax errors.
        </p>
      </div>
    );
  }

  return (
    <div className="flex-1 min-h-0 overflow-y-auto px-4 py-3 space-y-4">
      {localError && (
        <div className="p-2.5 text-xs text-bear bg-bear/10 border border-bear/40 rounded flex items-center justify-between">
          <span>{localError}</span>
          <button
            onClick={() => setLocalError("")}
            className="text-fg-dim hover:text-fg text-sm ml-2"
          >
            ✕
          </button>
        </div>
      )}

      {/* Venue Switcher Bar */}
      <div className="flex flex-wrap items-center justify-between gap-3 bg-panel border border-border p-3 rounded">
        <div className="flex items-center gap-2">
          <span className="text-2xs uppercase tracking-wider text-fg-dim font-bold mr-1">
            Exchange Venue:
          </span>
          {parsed.venues.map((v) => {
            const isSelected = v.venue === selectedVenue;
            const count = v.symbols?.length ?? 0;
            const isFutures = v.venue.includes("futures");
            return (
              <button
                key={v.venue}
                onClick={() => setSelectedVenue(v.venue)}
                className={`flex items-center gap-2 px-3 py-1.5 text-xs font-mono font-medium rounded transition-colors ${
                  isSelected
                    ? "bg-accent text-bg font-bold shadow"
                    : "bg-bg-alt text-fg hover:text-white hover:bg-border"
                }`}
              >
                <span>{isFutures ? "⚡ Binance Futures (USDT-M)" : "◉ Binance Spot"}</span>
                <span
                  className={`px-1.5 py-0.2 text-2xs rounded-full ${
                    isSelected ? "bg-bg text-accent" : "bg-bg text-fg-dim"
                  }`}
                >
                  {count}
                </span>
              </button>
            );
          })}
        </div>

        <div className="flex items-center gap-2">
          <button
            onClick={() => setShowDefaults(!showDefaults)}
            className="px-2.5 py-1 text-xs border border-border rounded text-fg-dim hover:text-fg bg-bg-alt"
          >
            {showDefaults ? "▲ Hide Defaults" : "▼ Venue Defaults"}
          </button>
          <button
            onClick={() => setShowAddModal(true)}
            className="flex items-center gap-1.5 px-3 py-1 text-xs font-bold rounded bg-bull text-bg hover:opacity-90 transition-opacity"
          >
            <span>＋</span> Add Market
          </button>
        </div>
      </div>

      {/* Collapsible Venue Defaults Preview */}
      {showDefaults && currentVenue && (
        <div className="p-3 bg-bg-alt/70 border border-border rounded text-xs grid grid-cols-2 sm:grid-cols-4 md:grid-cols-6 gap-3">
          <div>
            <span className="text-2xs text-fg-dim block uppercase">Contract</span>
            <span className="font-mono text-fg">{currentVenue.defaults?.contract_type ?? "N/A"}</span>
          </div>
          <div>
            <span className="text-2xs text-fg-dim block uppercase">Default Leverage</span>
            <span className="font-mono text-accent">{currentVenue.defaults?.leverage ?? 1}x</span>
          </div>
          <div>
            <span className="text-2xs text-fg-dim block uppercase">Margin Type</span>
            <span className="font-mono text-fg">{currentVenue.defaults?.margin_type ?? "isolated"}</span>
          </div>
          <div>
            <span className="text-2xs text-fg-dim block uppercase">Primary TF</span>
            <span className="font-mono text-fg">{currentVenue.defaults?.primary_timeframe ?? "5m"}</span>
          </div>
          <div>
            <span className="text-2xs text-fg-dim block uppercase">Trend TF</span>
            <span className="font-mono text-fg">{currentVenue.defaults?.trend_timeframe ?? "1h"}</span>
          </div>
          <div>
            <span className="text-2xs text-fg-dim block uppercase">Max Spread</span>
            <span className="font-mono text-fg">{currentVenue.defaults?.max_spread_pct ?? 0.05}%</span>
          </div>
        </div>
      )}

      {/* Quick Add Market Modal / Section */}
      {showAddModal && (
        <div className="p-4 bg-panel border-2 border-accent/60 rounded space-y-4 shadow-xl">
          <div className="flex items-center justify-between border-b border-border pb-2">
            <div className="flex items-center gap-2">
              <span className="text-sm font-bold text-accent uppercase tracking-wide">
                Add Crypto Market to {selectedVenue === "binance_futures" ? "Futures" : "Spot"}
              </span>
              <span className="text-2xs px-2 py-0.5 rounded bg-bg text-fg-dim">
                Select from popular list or enter custom symbol
              </span>
            </div>
            <button
              onClick={() => setShowAddModal(false)}
              className="text-fg-dim hover:text-fg text-sm px-2 py-0.5"
            >
              ✕
            </button>
          </div>

          {/* Category Filter for Presets */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <span className="text-xs text-fg-dim font-medium">Quick Pick from Top Binance Markets:</span>
              <div className="flex gap-1 text-2xs">
                {["All", "Major", "L1/L2", "DeFi/AI", "Meme", "Commodity"].map((cat) => (
                  <button
                    key={cat}
                    onClick={() => setCategoryFilter(cat)}
                    className={`px-2 py-0.5 rounded ${
                      categoryFilter === cat
                        ? "bg-accent/20 text-accent font-bold"
                        : "text-fg-dim hover:text-fg"
                    }`}
                  >
                    {cat}
                  </button>
                ))}
              </div>
            </div>

            {/* Quick Chips */}
            <div className="flex flex-wrap gap-1.5 max-h-36 overflow-y-auto p-2 bg-bg border border-border rounded">
              {filteredPresets.map((p) => {
                const isSelected = selectedPreset === p.symbol;
                return (
                  <button
                    key={p.symbol}
                    onClick={() => {
                      setSelectedPreset(p.symbol);
                      setCustomSymbol("");
                      if (selectedVenue !== "binance_spot") {
                        setNewLeverage(p.defaultLeverage);
                      }
                    }}
                    className={`px-2 py-1 text-xs font-mono rounded flex items-center gap-1.5 transition-all ${
                      isSelected
                        ? "bg-accent text-bg font-bold ring-1 ring-white"
                        : "bg-bg-alt text-fg hover:bg-border border border-border/40"
                    }`}
                  >
                    <span>{p.symbol}</span>
                    <span className="text-2xs opacity-70">({p.base})</span>
                  </button>
                );
              })}
              {filteredPresets.length === 0 && (
                <span className="text-xs text-fg-dim italic py-1">
                  All preset markets in this category are already added!
                </span>
              )}
            </div>
          </div>

          {/* Or Custom Symbol Input */}
          <div className="grid grid-cols-1 md:grid-cols-3 gap-3 pt-1">
            <div className="space-y-1">
              <label className="text-xs text-fg-dim font-medium block">
                Symbol Name (or custom):
              </label>
              <input
                type="text"
                placeholder={selectedVenue === "binance_spot" ? "e.g. INJ/USDT" : "e.g. INJ/USDT-PERP"}
                value={customSymbol || selectedPreset}
                onChange={(e) => {
                  setCustomSymbol(e.target.value.toUpperCase());
                  setSelectedPreset("");
                }}
                className="w-full rounded bg-bg border border-border px-3 py-1.5 text-xs text-fg font-mono uppercase focus:border-accent focus:outline-none"
              />
            </div>

            {/* Leverage selector */}
            <div className="space-y-1">
              <label className="text-xs text-fg-dim font-medium block">
                Leverage ({selectedVenue === "binance_spot" ? "Spot = 1x" : `${newLeverage}x`}):
              </label>
              <div className="flex items-center gap-1">
                {selectedVenue === "binance_spot" ? (
                  <span className="text-xs text-fg-dim italic py-1">Spot mode: 1x leverage</span>
                ) : (
                  <>
                    {[5, 10, 20, 50, 75, 100].map((lev) => (
                      <button
                        key={lev}
                        type="button"
                        onClick={() => setNewLeverage(lev)}
                        className={`px-2 py-1 text-2xs font-mono rounded border ${
                          newLeverage === lev
                            ? "bg-accent/20 border-accent text-accent font-bold"
                            : "border-border text-fg-dim hover:text-fg"
                        }`}
                      >
                        {lev}x
                      </button>
                    ))}
                    <input
                      type="number"
                      min={1}
                      max={125}
                      value={newLeverage}
                      onChange={(e) => setNewLeverage(parseInt(e.target.value, 10) || 1)}
                      className="w-16 rounded bg-bg border border-border px-2 py-1 text-xs text-fg font-mono text-center ml-1"
                    />
                  </>
                )}
              </div>
            </div>

            {/* Strategy Selection */}
            <div className="space-y-1">
              <label className="text-xs text-fg-dim font-medium block">
                Active Trading Strategies:
              </label>
              <div className="flex flex-wrap gap-1">
                {AVAILABLE_STRATEGIES.map((strat) => {
                  const active = selectedStrategies.includes(strat.id);
                  return (
                    <button
                      key={strat.id}
                      type="button"
                      onClick={() => {
                        if (active) {
                          setSelectedStrategies(selectedStrategies.filter((s) => s !== strat.id));
                        } else {
                          setSelectedStrategies([...selectedStrategies, strat.id]);
                        }
                      }}
                      className={`px-2 py-0.5 text-2xs font-mono rounded transition-colors ${
                        active
                          ? "bg-bull/20 text-bull border border-bull/40 font-bold"
                          : "bg-bg text-fg-dim border border-border hover:text-fg"
                      }`}
                    >
                      {strat.label}
                    </button>
                  );
                })}
              </div>
            </div>
          </div>

          {/* Action Row */}
          <div className="flex items-center justify-end gap-2 pt-2 border-t border-border">
            <button
              onClick={() => setShowAddModal(false)}
              className="px-3 py-1.5 text-xs text-fg-dim hover:text-fg"
            >
              Cancel
            </button>
            <button
              onClick={() => {
                const targetSym = customSymbol || selectedPreset;
                if (!targetSym) {
                  setLocalError("Please select or enter a symbol name");
                  return;
                }
                handleAddSymbol(targetSym, newLeverage, selectedStrategies);
              }}
              className="px-4 py-1.5 text-xs font-bold rounded bg-bull text-bg hover:opacity-90 shadow"
            >
              ＋ Add to {selectedVenue === "binance_futures" ? "Futures" : "Spot"}
            </button>
          </div>
        </div>
      )}

      {/* Configured Markets List */}
      <div className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <span className="text-xs font-bold uppercase tracking-wider text-fg">
              Configured Markets in {selectedVenue === "binance_futures" ? "Futures" : "Spot"}:
            </span>
            <span className="text-2xs px-2 py-0.5 bg-bg-alt border border-border rounded text-accent font-mono font-bold">
              {activeSymbols.length} Total
            </span>
          </div>

          <div className="relative">
            <input
              type="text"
              placeholder="Search markets..."
              value={searchFilter}
              onChange={(e) => setSearchFilter(e.target.value)}
              className="w-48 rounded bg-bg border border-border px-2.5 py-1 text-xs text-fg font-mono placeholder-fg-dim/60 focus:border-accent focus:outline-none"
            />
            {searchFilter && (
              <button
                onClick={() => setSearchFilter("")}
                className="absolute right-2 top-1.5 text-xs text-fg-dim hover:text-fg"
              >
                ✕
              </button>
            )}
          </div>
        </div>

        {/* Markets Cards Grid */}
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
          {filteredSymbols.map((item) => {
            const isSpot = selectedVenue === "binance_spot";
            const effectiveLev = isSpot ? 1 : (item.leverage ?? currentVenue?.defaults?.leverage ?? 5);
            const activeStrats = item.strategies ?? [];

            return (
              <div
                key={item.symbol}
                className="p-3 bg-panel border border-border rounded hover:border-border-focus transition-all space-y-2.5 group relative"
              >
                {/* Header */}
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <span className="font-mono font-bold text-sm text-accent">
                      {item.symbol}
                    </span>
                    {!isSpot && (
                      <span className="px-1.5 py-0.2 text-2xs font-mono font-bold rounded bg-accent/20 text-accent">
                        {effectiveLev}x
                      </span>
                    )}
                  </div>

                  <button
                    onClick={() => handleRemoveSymbol(item.symbol)}
                    title="Remove market from config"
                    className="opacity-60 group-hover:opacity-100 text-fg-dim hover:text-bear p-1 text-xs transition-opacity"
                  >
                    🗑
                  </button>
                </div>

                {/* Leverage Adjuster (Futures only) */}
                {!isSpot && (
                  <div className="flex items-center justify-between text-xs py-1 px-2 bg-bg rounded border border-border/50">
                    <span className="text-2xs text-fg-dim uppercase">Leverage:</span>
                    <div className="flex items-center gap-1.5 font-mono">
                      <button
                        onClick={() => handleUpdateLeverage(item.symbol, -5)}
                        className="px-1.5 py-0.5 bg-bg-alt hover:bg-border rounded text-fg text-2xs font-bold"
                      >
                        -5
                      </button>
                      <span className="text-xs font-bold text-fg w-8 text-center">
                        {effectiveLev}x
                      </span>
                      <button
                        onClick={() => handleUpdateLeverage(item.symbol, 5)}
                        className="px-1.5 py-0.5 bg-bg-alt hover:bg-border rounded text-fg text-2xs font-bold"
                      >
                        +5
                      </button>
                    </div>
                  </div>
                )}

                {/* Strategies Chips */}
                <div className="space-y-1">
                  <span className="text-2xs text-fg-dim uppercase block">Strategies:</span>
                  <div className="flex flex-wrap gap-1">
                    {AVAILABLE_STRATEGIES.map((st) => {
                      const enabled = activeStrats.includes(st.id);
                      return (
                        <button
                          key={st.id}
                          onClick={() => handleToggleStrategy(item.symbol, st.id)}
                          title={`Toggle ${st.label}: ${st.desc}`}
                          className={`px-1.5 py-0.5 text-2xs font-mono rounded transition-colors ${
                            enabled
                              ? "bg-bull/20 text-bull border border-bull/40 font-bold"
                              : "bg-bg text-fg-dim/60 border border-border/40 hover:text-fg hover:border-border"
                          }`}
                        >
                          {enabled ? "✓ " : ""}{st.label}
                        </button>
                      );
                    })}
                    {activeStrats.length === 0 && (
                      <span className="text-2xs text-warn italic">
                        No strategies (Data-only feed)
                      </span>
                    )}
                  </div>
                </div>

                {/* Custom Specs if configured */}
                {(item.tick_size || item.lot_size || item.max_spread_pct) && (
                  <div className="pt-1.5 border-t border-border/40 flex items-center gap-3 text-2xs text-fg-dim font-mono">
                    {item.tick_size && <span>tick: {item.tick_size}</span>}
                    {item.lot_size && <span>lot: {item.lot_size}</span>}
                    {item.max_spread_pct && <span>spread: {item.max_spread_pct}%</span>}
                  </div>
                )}
              </div>
            );
          })}

          {filteredSymbols.length === 0 && (
            <div className="col-span-full p-8 text-center bg-panel border border-border border-dashed rounded text-fg-dim text-xs space-y-2">
              <p>No markets found matching current filter.</p>
              <button
                onClick={() => setShowAddModal(true)}
                className="px-3 py-1 bg-bull text-bg text-xs font-bold rounded inline-block"
              >
                ＋ Add Market to {selectedVenue === "binance_futures" ? "Futures" : "Spot"}
              </button>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// ─── Raw YAML Code Editor ────────────────────────────────────────────────────

function RawYamlEditor({
  content,
  onChange,
  onSave,
  onReset,
  saving,
  isDirty,
}: {
  content: string;
  onChange: (c: string) => void;
  onSave: () => void;
  onReset: () => void;
  saving: boolean;
  isDirty: boolean;
}) {
  const [copyFeedback, setCopyFeedback] = useState(false);
  const [syntaxStatus, setSyntaxStatus] = useState<{ valid: boolean; error?: string } | null>(null);
  const gutterRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const lines = useMemo(() => content.split("\n"), [content]);

  const handleCopy = () => {
    navigator.clipboard.writeText(content);
    setCopyFeedback(true);
    setTimeout(() => setCopyFeedback(false), 2000);
  };

  const handleValidate = () => {
    try {
      YAML.parse(content);
      setSyntaxStatus({ valid: true });
    } catch (e: unknown) {
      setSyntaxStatus({
        valid: false,
        error: e instanceof Error ? e.message : String(e),
      });
    }
  };

  return (
    <div className="flex-1 flex flex-col min-h-0 bg-bg">
      {/* Editor Toolbar */}
      <div className="shrink-0 flex items-center justify-between px-3 py-1.5 bg-panel border-b border-border text-xs">
        <div className="flex items-center gap-3 font-mono text-2xs text-fg-dim">
          <span>{lines.length} lines</span>
          <span>{content.length} bytes</span>
          {syntaxStatus && (
            <span
              className={`font-bold ${
                syntaxStatus.valid ? "text-bull" : "text-bear"
              }`}
            >
              {syntaxStatus.valid ? "✓ Valid YAML" : `✗ Error: ${syntaxStatus.error}`}
            </span>
          )}
        </div>

        <div className="flex items-center gap-2">
          <button
            onClick={handleValidate}
            className="px-2 py-0.5 rounded border border-border bg-bg-alt text-fg-dim hover:text-fg text-2xs"
          >
            Check Syntax
          </button>
          <button
            onClick={handleCopy}
            className="px-2 py-0.5 rounded border border-border bg-bg-alt text-fg-dim hover:text-fg text-2xs"
          >
            {copyFeedback ? "✓ Copied" : "Copy YAML"}
          </button>
        </div>
      </div>

      {/* Editor Textarea with line numbers */}
      <div className="flex-1 flex min-h-0 overflow-hidden font-mono text-xs">
        {/* Line numbers gutter */}
        <div
          ref={gutterRef}
          onWheel={(e) => {
            if (textareaRef.current) {
              textareaRef.current.scrollTop += e.deltaY;
            }
          }}
          className="w-12 bg-panel/70 border-r border-border text-fg-dim/40 select-none py-3 px-2 text-right overflow-hidden leading-5"
        >
          {lines.map((_, i) => (
            <div key={i}>{i + 1}</div>
          ))}
        </div>

        {/* Text body */}
        <textarea
          ref={textareaRef}
          value={content}
          onChange={(e) => {
            onChange(e.target.value);
            if (syntaxStatus) setSyntaxStatus(null);
          }}
          onScroll={(e) => {
            if (gutterRef.current) {
              gutterRef.current.scrollTop = e.currentTarget.scrollTop;
            }
          }}
          spellCheck={false}
          className="flex-1 min-h-0 bg-bg text-fg p-3 leading-5 resize-none border-none outline-none overflow-y-auto selection:bg-accent/30 font-mono whitespace-pre"
        />
      </div>
    </div>
  );
}

// ─── Form Field & Section View ───────────────────────────────────────────────

function FormField({
  field,
  value,
  onChange,
}: {
  field: ConfigFormField;
  value: string | number | boolean;
  onChange: (v: string | number | boolean) => void;
}) {
  const id = `field-${field.name.replace(/\./g, "-")}`;

  switch (field.type) {
    case "bool":
    case "boolean": {
      const isChecked = !!value;
      return (
        <div className="flex items-center justify-between p-2 rounded bg-bg border border-border/60 hover:border-border transition-colors">
          <div className="flex-1 pr-3">
            <label htmlFor={id} className="text-xs font-medium text-fg block cursor-pointer">
              {field.label}
              {field.required && <span className="text-bear ml-1">*</span>}
            </label>
            {field.description && (
              <p className="text-2xs text-fg-dim mt-0.5 leading-snug">{field.description}</p>
            )}
          </div>
          <button
            type="button"
            id={id}
            role="switch"
            aria-checked={isChecked}
            onClick={() => onChange(!isChecked)}
            className={`w-10 h-5 rounded-full p-0.5 transition-colors focus:outline-none focus:ring-1 focus:ring-accent ${
              isChecked ? "bg-bull" : "bg-bg-alt border border-border"
            }`}
          >
            <div
              className={`w-4 h-4 rounded-full bg-white transition-transform ${
                isChecked ? "translate-x-5" : "translate-x-0"
              }`}
            />
          </button>
        </div>
      );
    }

    case "select":
      return (
        <div className="p-2 rounded bg-bg border border-border/60 space-y-1">
          <div className="flex items-center justify-between">
            <label htmlFor={id} className="text-xs font-medium text-fg">
              {field.label}
              {field.required && <span className="text-bear ml-1">*</span>}
            </label>
            <select
              id={id}
              value={String(value ?? "")}
              onChange={(e) => onChange(e.target.value)}
              className="rounded bg-bg-alt border border-border px-2.5 py-1 text-xs text-fg font-mono focus:border-accent focus:outline-none"
            >
              {(field.options ?? []).map((opt) => (
                <option key={opt} value={opt}>
                  {opt}
                </option>
              ))}
            </select>
          </div>
          {field.description && (
            <p className="text-2xs text-fg-dim">{field.description}</p>
          )}
        </div>
      );

    case "number":
    case "int":
    case "float":
      return (
        <div className="p-2 rounded bg-bg border border-border/60 space-y-1">
          <div className="flex items-center justify-between gap-2">
            <label htmlFor={id} className="text-xs font-medium text-fg flex-1">
              {field.label}
              {field.required && <span className="text-bear ml-1">*</span>}
            </label>
            <input
              id={id}
              type="number"
              value={typeof value === "boolean" ? (value ? 1 : 0) : (value ?? "")}
              step={field.step ?? (field.type === "float" ? 0.01 : 1)}
              min={field.min}
              max={field.max}
              onChange={(e) => {
                const v = e.target.value;
                if (v === "") onChange(0);
                else if (field.type === "float" || field.step === 0.01)
                  onChange(parseFloat(v));
                else onChange(parseInt(v, 10));
              }}
              className="w-32 rounded bg-bg-alt border border-border px-2.5 py-1 text-xs text-fg font-mono text-right focus:border-accent focus:outline-none"
            />
          </div>
          {field.description && (
            <p className="text-2xs text-fg-dim">{field.description}</p>
          )}
        </div>
      );

    case "textarea":
      return (
        <div className="p-2.5 rounded bg-bg border border-border/60 space-y-1.5 col-span-full">
          <div className="flex items-center justify-between">
            <label htmlFor={id} className="text-xs font-medium text-fg">
              {field.label}
              {field.required && <span className="text-bear ml-1">*</span>}
            </label>
            <span className="text-2xs text-fg-dim font-mono">YAML Sequence / Block</span>
          </div>
          {field.description && (
            <p className="text-2xs text-fg-dim">{field.description}</p>
          )}
          <textarea
            id={id}
            value={String(value ?? "")}
            onChange={(e) => onChange(e.target.value)}
            rows={5}
            className="w-full rounded bg-panel border border-border px-3 py-2 text-xs text-fg font-mono focus:border-accent focus:outline-none"
            style={{ resize: "vertical" }}
          />
        </div>
      );

    case "text":
    default:
      return (
        <div className="p-2 rounded bg-bg border border-border/60 space-y-1">
          <div className="flex items-center justify-between gap-2">
            <label htmlFor={id} className="text-xs font-medium text-fg flex-1">
              {field.label}
              {field.required && <span className="text-bear ml-1">*</span>}
            </label>
            <input
              id={id}
              type="text"
              value={String(value ?? "")}
              onChange={(e) => onChange(e.target.value)}
              className="w-48 rounded bg-bg-alt border border-border px-2.5 py-1 text-xs text-fg font-mono focus:border-accent focus:outline-none"
            />
          </div>
          {field.description && (
            <p className="text-2xs text-fg-dim">{field.description}</p>
          )}
        </div>
      );
  }
}

function FormSectionComponent({
  section,
  values,
  onChange,
  searchQuery,
}: {
  section: ConfigFormSection;
  values: Record<string, unknown>;
  onChange: (name: string, value: string | number | boolean) => void;
  searchQuery: string;
}) {
  const [collapsed, setCollapsed] = useState(false);

  const matchesSearch = useCallback(
    (f: ConfigFormField) => {
      if (!searchQuery) return true;
      const q = searchQuery.toLowerCase();
      return (
        f.label.toLowerCase().includes(q) ||
        f.name.toLowerCase().includes(q) ||
        (f.description && f.description.toLowerCase().includes(q))
      );
    },
    [searchQuery]
  );

  const visibleFields = section.fields.filter(matchesSearch);
  const hasMatchingSubsections = section.sections?.some((sub) =>
    sub.fields.some(matchesSearch)
  );

  if (searchQuery && visibleFields.length === 0 && !hasMatchingSubsections) {
    return null;
  }

  return (
    <div className="border border-border bg-panel rounded mb-3 overflow-hidden shadow-sm">
      <button
        onClick={() => setCollapsed(!collapsed)}
        className="w-full flex items-center justify-between px-3 py-2 bg-bg-alt/90 text-xs font-bold text-fg hover:bg-bg-alt transition-colors"
      >
        <div className="flex items-center gap-2">
          <span className="text-accent uppercase tracking-wide">{section.title}</span>
          <span className="text-2xs px-1.5 py-0.2 rounded bg-bg text-fg-dim font-mono">
            {section.fields.length} fields
          </span>
        </div>
        <span className="text-xs text-fg-dim font-mono">{collapsed ? "▸ Expand" : "▾ Collapse"}</span>
      </button>

      {!collapsed && (
        <div className="p-3 space-y-3">
          {section.description && (
            <p className="text-2xs text-fg-dim bg-bg/50 p-2 rounded border border-border/40">
              {section.description}
            </p>
          )}

          {visibleFields.length > 0 && (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-2.5">
              {visibleFields.map((field) => (
                <FormField
                  key={field.name}
                  field={field}
                  value={
                    (values[field.name] as string | number | boolean) ??
                    field.value
                  }
                  onChange={(v) => onChange(field.name, v)}
                />
              ))}
            </div>
          )}

          {section.sections?.map((sub) => (
            <FormSectionComponent
              key={sub.key}
              section={sub}
              values={values}
              onChange={onChange}
              searchQuery={searchQuery}
            />
          ))}
        </div>
      )}
    </div>
  );
}

// ─── Main ConfigFilesView Component ──────────────────────────────────────────

export function ConfigFilesView() {
  const [files, setFiles] = useState<string[]>([]);
  const [activeFile, setActiveFile] = useState<string>("markets.yaml");
  const [viewMode, setViewMode] = useState<ViewMode>("markets");

  // Raw YAML state
  const [rawContent, setRawContent] = useState<string>("");
  const [savedRawContent, setSavedRawContent] = useState<string>("");
  const isRawDirty = rawContent !== savedRawContent;

  // Form schema state
  const [schema, setSchema] = useState<ConfigFormSchema | null>(null);
  const [formValues, setFormValues] = useState<Record<string, unknown>>({});
  const [formDirty, setFormDirty] = useState<Record<string, unknown>>({});
  const [formSearch, setFormSearch] = useState<string>("");
  const isFormDirty = Object.keys(formDirty).length > 0;

  // General loading & feedback
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");

  // Load file list
  useEffect(() => {
    setLoading(true);
    getConfigFiles()
      .then((names) => {
        setFiles(names);
        if (names.length > 0 && !names.includes(activeFile)) {
          setActiveFile(names[0]);
        }
      })
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false));
  }, []);

  // When activeFile changes, determine suitable default viewMode
  useEffect(() => {
    if (activeFile === "markets.yaml") {
      setViewMode("markets");
    } else {
      setViewMode("form");
    }
  }, [activeFile]);

  // Load file data (raw content + form schema/values)
  const loadFile = useCallback(async (name: string) => {
    setLoading(true);
    setError("");
    setSuccess("");
    try {
      const [raw, s, v] = await Promise.all([
        getConfigFile(name).catch(() => ({ name, content: "" })),
        getConfigFormSchema(name).catch(() => null),
        getConfigFormValues(name).catch(() => ({})),
      ]);
      setRawContent(raw.content);
      setSavedRawContent(raw.content);
      setSchema(s);
      setFormValues(v);
      setFormDirty({});
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (activeFile) {
      loadFile(activeFile);
    }
  }, [activeFile, loadFile]);

  const handleFormFieldChange = (name: string, value: string | number | boolean) => {
    setFormValues((prev) => ({ ...prev, [name]: value }));
    setFormDirty((prev) => ({ ...prev, [name]: value }));
  };

  const handleSave = async () => {
    if (saving || (!isRawDirty && !isFormDirty)) return;
    setSaving(true);
    setError("");
    setSuccess("");

    try {
      if (viewMode === "form" && isFormDirty) {
        // Save via form write API
        await saveConfigFormValues(activeFile, formDirty);
        setFormDirty({});
        setSuccess("Saved & hot-reloaded engine");
        // Refresh raw content
        const raw = await getConfigFile(activeFile);
        setRawContent(raw.content);
        setSavedRawContent(raw.content);
      } else {
        // Save raw content (used for Markets Manager & Raw YAML view)
        await saveConfigFile(activeFile, rawContent);
        setSavedRawContent(rawContent);
        setSuccess("Saved & hot-reloaded engine");
        // Refresh form schema & values
        const [s, v] = await Promise.all([
          getConfigFormSchema(activeFile).catch(() => null),
          getConfigFormValues(activeFile).catch(() => ({})),
        ]);
        setSchema(s);
        setFormValues(v);
        setFormDirty({});
      }
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  const handleReset = () => {
    loadFile(activeFile);
  };

  const isCurrentDirty = viewMode === "form" ? isFormDirty : isRawDirty;

  return (
    <div className="h-full flex flex-col min-h-0 overflow-hidden bg-bg text-fg">
      {/* Top Bar: File Tabs & View Mode */}
      <div className="shrink-0 flex flex-wrap items-center justify-between gap-2 px-3 py-2 bg-panel border-b border-border">
        {/* File Tabs */}
        <div className="flex items-center gap-1.5">
          {files.map((fileName) => {
            const meta = FILE_METADATA[fileName] ?? {
              label: fileName,
              icon: "📄",
              desc: fileName,
            };
            const isActive = activeFile === fileName;
            const fileDirty =
              isActive && isCurrentDirty;

            return (
              <button
                key={fileName}
                onClick={() => setActiveFile(fileName)}
                title={meta.desc}
                className={`flex items-center gap-1.5 px-3 py-1.5 text-xs font-mono rounded transition-colors ${
                  isActive
                    ? "bg-accent text-bg font-bold shadow-sm"
                    : "bg-bg-alt text-fg-dim hover:text-fg hover:bg-border/60"
                }`}
              >
                <span>{meta.icon}</span>
                <span>{meta.label}</span>
                {fileDirty && (
                  <span className="w-2 h-2 rounded-full bg-warn animate-pulse ml-0.5" />
                )}
              </button>
            );
          })}
        </div>

        {/* View Mode Switcher */}
        <div className="flex items-center gap-2">
          <div className="flex items-center bg-bg rounded p-0.5 border border-border text-2xs font-mono">
            {activeFile === "markets.yaml" && (
              <button
                onClick={() => setViewMode("markets")}
                className={`px-2.5 py-1 rounded transition-colors ${
                  viewMode === "markets"
                    ? "bg-accent/20 text-accent font-bold"
                    : "text-fg-dim hover:text-fg"
                }`}
              >
                ◈ Markets Manager
              </button>
            )}
            <button
              onClick={() => setViewMode("form")}
              className={`px-2.5 py-1 rounded transition-colors ${
                viewMode === "form"
                  ? "bg-accent/20 text-accent font-bold"
                  : "text-fg-dim hover:text-fg"
              }`}
            >
              🎛 Form Settings
            </button>
            <button
              onClick={() => setViewMode("raw")}
              className={`px-2.5 py-1 rounded transition-colors ${
                viewMode === "raw"
                  ? "bg-accent/20 text-accent font-bold"
                  : "text-fg-dim hover:text-fg"
              }`}
            >
              📄 Raw YAML
            </button>
          </div>
        </div>
      </div>

      {/* Error & Success Alert Banners */}
      {error && (
        <div className="shrink-0 mx-3 mt-2 px-3 py-2 bg-bear/15 border border-bear/60 rounded text-xs text-bear flex items-start justify-between">
          <div className="space-y-0.5">
            <span className="font-bold">Error saving config: </span>
            <span className="font-mono">{error}</span>
          </div>
          <button
            onClick={() => setError("")}
            className="text-fg-dim hover:text-bear ml-2 text-sm"
          >
            ✕
          </button>
        </div>
      )}
      {success && (
        <div className="shrink-0 mx-3 mt-2 px-3 py-2 bg-bull/15 border border-bull/60 rounded text-xs text-bull flex items-center justify-between">
          <span>✓ {success}</span>
          <button
            onClick={() => setSuccess("")}
            className="text-fg-dim hover:text-bull ml-2 text-sm"
          >
            ✕
          </button>
        </div>
      )}

      {/* Main Content Area */}
      {loading && !rawContent && !schema ? (
        <div className="flex-1 flex items-center justify-center text-fg-dim text-xs font-mono">
          Loading configuration...
        </div>
      ) : viewMode === "markets" && activeFile === "markets.yaml" ? (
        <MarketsManager
          yamlContent={rawContent}
          onYamlChange={(newText) => setRawContent(newText)}
          isDirty={isRawDirty}
        />
      ) : viewMode === "raw" ? (
        <RawYamlEditor
          content={rawContent}
          onChange={(newText) => setRawContent(newText)}
          onSave={handleSave}
          onReset={handleReset}
          saving={saving}
          isDirty={isRawDirty}
        />
      ) : (
        /* Form View */
        <div className="flex-1 min-h-0 overflow-y-auto px-4 py-3">
          {/* Search bar inside form */}
          <div className="mb-3 flex items-center justify-between gap-3 bg-panel p-2.5 rounded border border-border">
            <div className="flex items-center gap-2 text-xs text-fg-dim">
              <span>Filter Settings:</span>
              <input
                type="text"
                placeholder="Search parameter name or description..."
                value={formSearch}
                onChange={(e) => setFormSearch(e.target.value)}
                className="w-72 rounded bg-bg border border-border px-2.5 py-1 text-xs text-fg font-mono placeholder-fg-dim/60 focus:border-accent focus:outline-none"
              />
              {formSearch && (
                <button
                  onClick={() => setFormSearch("")}
                  className="text-xs text-fg-dim hover:text-fg"
                >
                  ✕ Clear
                </button>
              )}
            </div>
            <span className="text-2xs text-fg-dim font-mono">
              {schema?.sections?.length ?? 0} root sections
            </span>
          </div>

          {schema ? (
            schema.sections.map((section) => (
              <FormSectionComponent
                key={section.key}
                section={section}
                values={formValues}
                onChange={handleFormFieldChange}
                searchQuery={formSearch}
              />
            ))
          ) : (
            <div className="text-center p-8 text-xs text-fg-dim">
              No form schema available for this file. Switch to Raw YAML view.
            </div>
          )}
        </div>
      )}

      {/* Bottom Sticky Action Bar */}
      <div className="shrink-0 flex items-center justify-between px-4 py-2 bg-panel border-t border-border">
        <div className="flex items-center gap-3">
          <span className="text-xs font-mono font-bold text-fg">
            {FILE_METADATA[activeFile]?.label ?? activeFile}
          </span>
          {isCurrentDirty ? (
            <span className="text-2xs font-bold text-warn bg-warn/15 px-2 py-0.5 rounded border border-warn/30 animate-pulse">
              ● Unsaved Changes
            </span>
          ) : (
            <span className="text-2xs text-fg-dim">Synced with disk</span>
          )}
        </div>

        <div className="flex items-center gap-2">
          {isCurrentDirty && (
            <button
              onClick={handleReset}
              disabled={saving}
              className="px-3 py-1 text-xs rounded border border-border bg-bg-alt text-fg-dim hover:text-fg transition-colors"
            >
              Revert
            </button>
          )}
          <button
            onClick={handleSave}
            disabled={saving || !isCurrentDirty}
            className="flex items-center gap-1.5 px-4 py-1.5 text-xs font-bold rounded bg-accent text-bg disabled:opacity-40 disabled:cursor-not-allowed hover:opacity-90 shadow transition-all"
          >
            {saving ? (
              <>
                <span className="w-3 h-3 border-2 border-bg border-t-transparent rounded-full animate-spin" />
                <span>Saving & Hot-Reloading...</span>
              </>
            ) : (
              <span>Save & Hot-Reload</span>
            )}
          </button>
        </div>
      </div>
    </div>
  );
}
