import type { ReactNode } from "react";

const titleColors: Record<string, string> = {
  accent: "text-accent",
  bull: "text-bull",
  bear: "text-bear",
  warn: "text-warn",
  agent: "text-agent",
  teal: "text-teal",
};

/**
 * Panel is the bordered, titled container used for every dashboard widget —
 * the web equivalent of a lipgloss bordered panel. 1px border, uppercase
 * colored title, dense body. Pass scroll to make the body independently
 * scrollable.
 */
export function Panel({
  title,
  color = "accent",
  right,
  children,
  className = "",
  scroll = false,
}: {
  title: string;
  color?: keyof typeof titleColors | string;
  right?: ReactNode;
  children: ReactNode;
  className?: string;
  scroll?: boolean;
}) {
  return (
    <div className={`flex min-h-0 flex-col border border-border bg-panel ${className}`}>
      <div className="flex shrink-0 items-center justify-between border-b border-border px-2 py-1">
        <span className={`font-bold uppercase tracking-wide ${titleColors[color] ?? "text-accent"}`}>
          {title}
        </span>
        {right && <span className="text-2xs text-fg-dim">{right}</span>}
      </div>
      <div className={`min-h-0 flex-1 p-2 ${scroll ? "scroll-y" : ""}`}>{children}</div>
    </div>
  );
}

/** BiasPill renders a colored directional badge: Bullish / Neutral / Bearish. */
export function BiasPill({ score, label }: { score: number; label: string }) {
  const cls =
    score > 0
      ? "bg-bull/20 text-bull"
      : score < 0
        ? "bg-bear/20 text-bear"
        : "bg-fg-dim/20 text-fg-dim";
  return <span className={`px-1.5 py-0.5 text-2xs font-bold uppercase ${cls}`}>{label}</span>;
}

/** BarGauge renders a horizontal filled bar (Fear & Greed, L/S, etc.). */
export function BarGauge({
  value,
  max = 100,
  color = "#5FAFD7",
  label,
}: {
  value: number;
  max?: number;
  color?: string;
  label?: string;
}) {
  const pct = Math.max(0, Math.min(100, (value / max) * 100));
  return (
    <div className="flex items-center gap-2">
      <div className="h-2 flex-1 bg-bg-alt">
        <div className="h-full" style={{ width: `${pct}%`, backgroundColor: color }} />
      </div>
      {label && <span className="w-20 text-right text-2xs text-fg-dim">{label}</span>}
    </div>
  );
}

const levelColors: Record<string, string> = {
  ERROR: "bg-bear/20 text-bear",
  WARN: "bg-warn/20 text-warn",
  INFO: "bg-accent/20 text-accent",
  DEBUG: "bg-fg-dim/20 text-fg-dim",
  AGENT: "bg-agent/20 text-agent",
  ORDER: "bg-bull/20 text-bull",
};

/** LevelBadge renders a colored log-level tag. */
export function LevelBadge({ level }: { level: string }) {
  const cls = levelColors[level] ?? "bg-fg-dim/20 text-fg-dim";
  return (
    <span className={`inline-block w-12 shrink-0 text-center text-2xs font-bold ${cls}`}>
      {level}
    </span>
  );
}
