"use client";

import { useStore } from "@/lib/store";
import { Panel, BarGauge } from "./shared";
import { fmtUsdCompact, fmtAge } from "@/lib/format";

function fearGreedColor(v: number): string {
  if (v >= 75) return "#87D787";
  if (v >= 55) return "#5FD7D7";
  if (v >= 45) return "#FFD75F";
  if (v >= 25) return "#FFAF5F";
  return "#FF5F5F";
}

/** Macro renders Fear & Greed, funding, OI, and L/S with bar gauges. */
export function Macro() {
  const macro = useStore((s) => s.macro);
  return (
    <Panel title="Macro" color="teal" scroll right={macro ? fmtAge(macro.updatedAt) : undefined}>
      {!macro ? (
        <div className="text-fg-dim">no macro data yet</div>
      ) : (
        <div className="space-y-3">
          <div>
            <div className="mb-1 flex justify-between text-2xs text-fg-dim">
              <span>Fear &amp; Greed</span>
              <span className="text-fg">
                {macro.fearGreedValue} {macro.fearGreedCategory}
              </span>
            </div>
            <BarGauge value={macro.fearGreedValue} color={fearGreedColor(macro.fearGreedValue)} />
          </div>
          <Row label="BTC Funding" value={`${(macro.fundingRate * 100).toFixed(4)}%`} color={macro.fundingRate >= 0 ? "text-bull" : "text-bear"} />
          <Row label="Open Interest" value={fmtUsdCompact(macro.openInterestUsd)} />
          <div>
            <div className="mb-1 flex justify-between text-2xs text-fg-dim">
              <span>Long / Short</span>
              <span className="text-fg">{macro.longShortRatio.toFixed(2)}</span>
            </div>
            <BarGauge value={macro.longShortRatio} max={3} color="#5FAFD7" />
          </div>
        </div>
      )}
    </Panel>
  );
}

function Row({ label, value, color = "text-fg" }: { label: string; value: string; color?: string }) {
  return (
    <div className="flex justify-between text-2xs">
      <span className="text-fg-dim">{label}</span>
      <span className={color}>{value}</span>
    </div>
  );
}

/** News renders the latest headlines, newest-first. */
export function News() {
  const news = useStore((s) => s.news);
  return (
    <Panel title="News" color="teal" scroll right={`${news.length}`}>
      <div className="space-y-1.5">
        {news.map((n, i) => (
          <a
            key={i}
            href={n.url}
            target="_blank"
            rel="noreferrer"
            className="block hover:bg-bg-alt"
          >
            <div className="text-2xs text-fg-dim">{n.source || n.domain}</div>
            <div className="line-clamp-2 text-2xs text-fg">{n.title}</div>
          </a>
        ))}
        {news.length === 0 && <div className="text-fg-dim">no headlines yet</div>}
      </div>
    </Panel>
  );
}

const stepColors: Record<string, string> = {
  THINKING: "text-agent",
  TOOL: "text-accent",
  OBSERVING: "text-teal",
  STREAMING: "text-warn",
  COMPLETE: "text-bull",
  ERROR: "text-bear",
};

/** AgentRuns renders the table of live + completed agent invocations. */
export function AgentRuns() {
  const runs = useStore((s) => s.agentRuns);
  const order = useStore((s) => s.agentOrder);
  const rows = [...order].reverse().map((id) => runs[id]).filter(Boolean);
  return (
    <Panel title="Agent Runs" color="agent" scroll right={`${rows.length}`}>
      <table className="w-full text-2xs">
        <thead className="sticky top-0 bg-panel uppercase text-fg-dim">
          <tr className="text-left">
            <th className="py-1">Agent</th>
            <th>Step</th>
            <th>Symbol</th>
            <th>Model</th>
            <th className="text-right">Step#</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.runId} className="border-t border-border/40 hover:bg-bg-alt">
              <td className="py-1 font-bold text-fg">{r.agent}</td>
              <td className={stepColors[r.step] ?? "text-fg"}>
                {(r.step === "THINKING" || r.step === "TOOL" || r.step === "STREAMING") && "▸ "}
                {r.step}
              </td>
              <td className="text-fg-dim">{r.symbol || "—"}</td>
              <td className="text-fg-dim">{r.model || "—"}</td>
              <td className="text-right text-fg-dim">
                {r.stepNum}/{r.maxSteps}
              </td>
            </tr>
          ))}
          {rows.length === 0 && (
            <tr>
              <td colSpan={5} className="py-3 text-center text-fg-dim">
                no agent activity yet
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </Panel>
  );
}

/** AgentReasoning streams the most recent run's description + content. */
export function AgentReasoning() {
  const runs = useStore((s) => s.agentRuns);
  const order = useStore((s) => s.agentOrder);
  const latest = order.length ? runs[order[order.length - 1]] : null;
  return (
    <Panel
      title={latest ? `Reasoning — ${latest.agent}${latest.symbol ? ` · ${latest.symbol}` : ""}` : "Reasoning"}
      color="accent"
      scroll
    >
      {!latest ? (
        <div className="text-fg-dim">no active agent</div>
      ) : (
        <div className="space-y-1 text-2xs">
          {latest.toolName && <div className="text-accent">▸ TOOL {latest.toolName}</div>}
          {latest.description && <div className="text-agent">{latest.description}</div>}
          {latest.content && <pre className="whitespace-pre-wrap break-words text-fg">{latest.content}</pre>}
        </div>
      )}
    </Panel>
  );
}
