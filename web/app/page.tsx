"use client";

import { useEffect, useState } from "react";
import { Header, TabBar, StatusBar, type Tab } from "@/components/chrome";
import { MarketWatch, Positions, BiasSignals, SystemLog } from "@/components/panels";
import { Macro, News, AgentRuns, AgentReasoning } from "@/components/agentpanels";
import { Copilot, ChatOpsControls } from "@/components/copilot";
import { useStore } from "@/lib/store";
import { getState, getToken, setToken } from "@/lib/api";
import { connectWS } from "@/lib/ws";

export default function Page() {
  const [tab, setTab] = useState<Tab>("dashboard");
  const [authed, setAuthed] = useState(false);
  const applySnapshot = useStore((s) => s.applySnapshot);
  const applyEvent = useStore((s) => s.applyEvent);
  const setConnected = useStore((s) => s.setConnected);

  useEffect(() => {
    setAuthed(getToken() !== "");
  }, []);

  useEffect(() => {
    if (!authed) return;
    let disconnect: (() => void) | undefined;
    getState()
      .then((snap) => {
        applySnapshot(snap);
        disconnect = connectWS(applyEvent, setConnected);
      })
      .catch(() => setAuthed(false)); // bad token → back to gate
    return () => disconnect?.();
  }, [authed, applySnapshot, applyEvent, setConnected]);

  if (!authed) return <TokenGate onSet={() => setAuthed(true)} />;

  return (
    <div className="flex h-screen flex-col bg-bg text-fg">
      <Header />
      <TabBar active={tab} onChange={setTab} />
      <main className="min-h-0 flex-1 overflow-hidden p-1">
        {tab === "dashboard" && <DashboardView />}
        {tab === "market" && <MarketView />}
        {tab === "logs" && (
          <div className="grid h-full">
            <SystemLog />
          </div>
        )}
        {tab === "agents" && <AgentsView />}
      </main>
      <StatusBar />
    </div>
  );
}

/** DashboardView: market watch on top, four columns below, agent strip. */
function DashboardView() {
  return (
    <div className="grid h-full grid-rows-[minmax(0,1.2fr)_minmax(0,1.6fr)_minmax(0,0.8fr)] gap-1">
      <MarketWatch />
      <div className="grid min-h-0 grid-cols-4 gap-1">
        <Positions />
        <BiasSignals />
        <SystemLog />
        <div className="grid min-h-0 grid-rows-2 gap-1">
          <Macro />
          <News />
        </div>
      </div>
      <div className="grid min-h-0 grid-cols-2 gap-1">
        <AgentRuns />
        <AgentReasoning />
      </div>
    </div>
  );
}

/** MarketView: full watchlist with a narrow macro/news rail. */
function MarketView() {
  return (
    <div className="grid h-full grid-cols-[minmax(0,5fr)_minmax(0,1fr)] gap-1">
      <MarketWatch />
      <div className="grid min-h-0 grid-rows-2 gap-1">
        <Macro />
        <News />
      </div>
    </div>
  );
}

/** AgentsView: runs + reasoning on the left, copilot + controls on the right. */
function AgentsView() {
  return (
    <div className="grid h-full grid-cols-[minmax(0,3fr)_minmax(0,2fr)] gap-1">
      <div className="grid min-h-0 grid-rows-2 gap-1">
        <AgentRuns />
        <AgentReasoning />
      </div>
      <div className="grid min-h-0 grid-rows-[minmax(0,3fr)_minmax(0,1fr)] gap-1">
        <Copilot />
        <ChatOpsControls />
      </div>
    </div>
  );
}

/** TokenGate prompts for the bearer token and stores it in localStorage. */
function TokenGate({ onSet }: { onSet: () => void }) {
  const [value, setValue] = useState("");
  return (
    <div className="flex h-screen items-center justify-center bg-bg">
      <div className="w-80 border border-border bg-panel p-4">
        <div className="mb-3 font-bold text-accent">◈ CEREBRO — Auth</div>
        <p className="mb-2 text-2xs text-fg-dim">
          Enter the dashboard token (WEB_AUTH_TOKEN from the server).
        </p>
        <input
          type="password"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && value.trim()) {
              setToken(value.trim());
              onSet();
            }
          }}
          placeholder="token"
          className="mb-2 w-full border border-border bg-bg px-2 py-1 text-fg outline-none focus:border-accent"
        />
        <button
          onClick={() => {
            if (value.trim()) {
              setToken(value.trim());
              onSet();
            }
          }}
          className="w-full border border-accent py-1 text-accent hover:bg-accent/10"
        >
          Connect
        </button>
      </div>
    </div>
  );
}
