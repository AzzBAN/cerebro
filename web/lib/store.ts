// Live dashboard store. The WebSocket layer pushes typed events here; React
// panels subscribe to slices. Snapshot replaces everything; deltas merge.

import { create } from "zustand";
import type {
  AgentRun,
  Bias,
  Budget,
  LogLine,
  Macro,
  NewsItem,
  Position,
  Quote,
  Snapshot,
} from "./types";

const MAX_LOGS = 500;

interface State {
  connected: boolean;
  quotes: Record<string, Quote>;
  positions: Position[];
  logs: LogLine[];
  agentRuns: Record<string, AgentRun>;
  agentOrder: string[];
  bias: Record<string, Bias>;
  biasOrder: string[];
  macro: Macro | null;
  news: NewsItem[];
  budget: Budget | null;
  heartbeat: string;

  setConnected: (up: boolean) => void;
  applySnapshot: (s: Snapshot) => void;
  applyEvent: (type: string, data: unknown) => void;
}

export const useStore = create<State>((set) => ({
  connected: false,
  quotes: {},
  positions: [],
  logs: [],
  agentRuns: {},
  agentOrder: [],
  bias: {},
  biasOrder: [],
  macro: null,
  news: [],
  budget: null,
  heartbeat: "",

  setConnected: (up) => set({ connected: up }),

  applySnapshot: (s) =>
    set(() => {
      const quotes: Record<string, Quote> = {};
      for (const q of s.quotes ?? []) quotes[q.symbol] = q;
      const agentRuns: Record<string, AgentRun> = {};
      const agentOrder: string[] = [];
      for (const r of s.agentRuns ?? []) {
        agentRuns[r.runId] = r;
        agentOrder.push(r.runId);
      }
      const bias: Record<string, Bias> = {};
      const biasOrder: string[] = [];
      for (const b of s.bias ?? []) {
        bias[b.symbol] = b;
        biasOrder.push(b.symbol);
      }
      return {
        quotes,
        positions: s.positions ?? [],
        logs: s.logs ?? [],
        agentRuns,
        agentOrder,
        bias,
        biasOrder,
        macro: s.macro,
        news: s.news ?? [],
        budget: s.budget,
        heartbeat: s.heartbeat ?? "",
      };
    }),

  applyEvent: (type, data) =>
    set((st) => {
      switch (type) {
        case "snapshot":
          // Re-entrant call into applySnapshot's reducer would be awkward;
          // handle inline by returning the same shape.
          return reduceSnapshot(data as Snapshot);
        case "quote": {
          const q = data as Quote;
          return { quotes: { ...st.quotes, [q.symbol]: q } };
        }
        case "positions":
          return { positions: data as Position[] };
        case "log": {
          const next = [...st.logs, data as LogLine];
          if (next.length > MAX_LOGS) next.splice(0, next.length - MAX_LOGS);
          return { logs: next };
        }
        case "bias": {
          const b = data as Bias;
          const order = st.bias[b.symbol] ? st.biasOrder : [...st.biasOrder, b.symbol];
          return { bias: { ...st.bias, [b.symbol]: b }, biasOrder: order };
        }
        case "agent": {
          const r = data as AgentRun;
          const order = st.agentRuns[r.runId] ? st.agentOrder : [...st.agentOrder, r.runId];
          return { agentRuns: { ...st.agentRuns, [r.runId]: r }, agentOrder: order };
        }
        case "macro":
          return { macro: data as Macro };
        case "news":
          return { news: data as NewsItem[] };
        case "budget":
          return { budget: data as Budget };
        case "heartbeat":
          return { heartbeat: data as string };
        default:
          return {};
      }
    }),
}));

// reduceSnapshot mirrors applySnapshot for the WS "snapshot" frame.
function reduceSnapshot(s: Snapshot): Partial<State> {
  const quotes: Record<string, Quote> = {};
  for (const q of s.quotes ?? []) quotes[q.symbol] = q;
  const agentRuns: Record<string, AgentRun> = {};
  const agentOrder: string[] = [];
  for (const r of s.agentRuns ?? []) {
    agentRuns[r.runId] = r;
    agentOrder.push(r.runId);
  }
  const bias: Record<string, Bias> = {};
  const biasOrder: string[] = [];
  for (const b of s.bias ?? []) {
    bias[b.symbol] = b;
    biasOrder.push(b.symbol);
  }
  return {
    quotes,
    positions: s.positions ?? [],
    logs: s.logs ?? [],
    agentRuns,
    agentOrder,
    bias,
    biasOrder,
    macro: s.macro,
    news: s.news ?? [],
    budget: s.budget,
    heartbeat: s.heartbeat ?? "",
  };
}
