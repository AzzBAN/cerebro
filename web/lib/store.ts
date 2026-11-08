// Live dashboard store. The WebSocket layer pushes typed events here; React
// panels subscribe to slices. Snapshot replaces everything; deltas merge.

import { create } from "zustand";
import type {
  AgentRun,
  Bias,
  Budget,
  Config,
  LogLine,
  Macro,
  NewsItem,
  Opportunity,
  OrderIntent,
  Position,
  Proposal,
  Quote,
  Snapshot,
  Trade,
} from "./types";

const MAX_LOGS = 500;
const MAX_HISTORY = 500;

interface ChatMsg {
  role: "user" | "assistant";
  text: string;
}

interface State {
  connected: boolean;
  quotes: Record<string, Quote>;
  positions: Position[];
  proposals: Proposal[];
  logs: LogLine[];
  agentRuns: Record<string, AgentRun>;
  agentOrder: string[];
  bias: Record<string, Bias>;
  biasOrder: string[];
  opportunities: Opportunity[];
  macro: Macro | null;
  news: NewsItem[];
  budget: Budget | null;
  heartbeat: string;
  config: Config | null;
  chatMsgs: ChatMsg[];
  // recentIntents and recentTrades back the order-history panel. They are
  // seeded from the initial snapshot and grow via WebSocket "trade" frames
  // (and "intent" frames when the engine starts emitting them). The slice
  // is bounded so a long-running engine never blows up React re-render
  // cost.
  recentIntents: OrderIntent[];
  recentTrades: Trade[];

  setConnected: (up: boolean) => void;
  applySnapshot: (s: Snapshot) => void;
  applyEvent: (type: string, data: unknown) => void;
  addChatMsg: (msg: ChatMsg) => void;
}

export const useStore = create<State>((set) => ({
  connected: false,
  quotes: {},
  positions: [],
  proposals: [],
  logs: [],
  agentRuns: {},
  agentOrder: [],
  bias: {},
  biasOrder: [],
  opportunities: [],
  macro: null,
  news: [],
  budget: null,
  heartbeat: "",
  config: null,
  chatMsgs: [],
  recentIntents: [],
  recentTrades: [],

  setConnected: (up) => set({ connected: up }),

  addChatMsg: (msg) =>
    set((s) => ({ chatMsgs: [...s.chatMsgs, msg] })),

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
        proposals: s.proposals ?? [],
        logs: s.logs ?? [],
        agentRuns,
        agentOrder,
        bias,
        biasOrder,
        opportunities: s.opportunities ?? [],
        macro: s.macro,
        news: s.news ?? [],
        budget: s.budget,
        heartbeat: s.heartbeat ?? "",
        config: s.config ?? null,
        recentIntents: s.recentIntents ?? [],
        recentTrades: s.recentTrades ?? [],
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
        case "proposals":
          return { proposals: data as Proposal[] };
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
        case "opportunities":
          return { opportunities: data as Opportunity[] };
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
        case "config":
          return { config: data as Config };
        case "trade": {
          // Push a single fill into the history buffer. De-duplicate by
          // trade id so an at-least-once WebSocket re-broadcast can't
          // double-count a fill.
          const t = data as Trade;
          if (st.recentTrades.some((x) => x.id === t.id)) return {};
          const next = [...st.recentTrades, t];
          if (next.length > MAX_HISTORY) next.splice(0, next.length - MAX_HISTORY);
          return { recentTrades: next };
        }
        case "intent": {
          const i = data as OrderIntent;
          if (st.recentIntents.some((x) => x.id === i.id)) return {};
          const next = [...st.recentIntents, i];
          if (next.length > MAX_HISTORY) next.splice(0, next.length - MAX_HISTORY);
          return { recentIntents: next };
        }
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
    proposals: s.proposals ?? [],
    logs: s.logs ?? [],
    agentRuns,
    agentOrder,
    bias,
    biasOrder,
    opportunities: s.opportunities ?? [],
    macro: s.macro,
    news: s.news ?? [],
    budget: s.budget,
    heartbeat: s.heartbeat ?? "",
    config: s.config ?? null,
    recentIntents: s.recentIntents ?? [],
    recentTrades: s.recentTrades ?? [],
  };
}
