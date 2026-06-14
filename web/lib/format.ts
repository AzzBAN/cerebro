// Display formatters. Money/quantity values arrive as decimal strings; we
// format for readability without doing float math on them.

export function fmtNum(s: string, maxFrac = 2): string {
  const n = Number(s);
  if (!isFinite(n)) return "—";
  return n.toLocaleString("en-US", {
    minimumFractionDigits: 0,
    maximumFractionDigits: maxFrac,
  });
}

export function fmtPrice(s: string): string {
  const n = Number(s);
  if (!isFinite(n) || n === 0) return "—";
  // More decimals for sub-dollar assets.
  const frac = n >= 100 ? 2 : n >= 1 ? 4 : 6;
  return n.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: frac });
}

export function fmtPct(s: string): string {
  const n = Number(s);
  if (!isFinite(n)) return "—";
  const sign = n > 0 ? "+" : "";
  return `${sign}${n.toFixed(2)}%`;
}

export function fmtSignedNum(s: string, maxFrac = 2): string {
  const n = Number(s);
  if (!isFinite(n)) return "—";
  const sign = n > 0 ? "+" : "";
  return sign + fmtNum(s, maxFrac);
}

export function fmtUsdCompact(s: string): string {
  const n = Number(s);
  if (!isFinite(n) || n === 0) return "—";
  if (n >= 1e9) return `$${(n / 1e9).toFixed(1)}B`;
  if (n >= 1e6) return `$${(n / 1e6).toFixed(1)}M`;
  if (n >= 1e3) return `$${(n / 1e3).toFixed(1)}K`;
  return `$${n.toFixed(0)}`;
}

// signClass returns a tailwind text color class based on the numeric sign.
export function signClass(s: string): string {
  const n = Number(s);
  if (!isFinite(n) || n === 0) return "text-fg";
  return n > 0 ? "text-bull" : "text-bear";
}

export function fmtAge(ms: number): string {
  if (!ms) return "—";
  const secs = Math.floor((Date.now() - ms) / 1000);
  if (secs < 60) return `${secs}s`;
  if (secs < 3600) return `${Math.floor(secs / 60)}m`;
  if (secs < 86400) return `${Math.floor(secs / 3600)}h`;
  return `${Math.floor(secs / 86400)}d`;
}

export function fmtClock(d: Date): string {
  return d.toISOString().replace("T", " ").slice(0, 19) + " UTC";
}
