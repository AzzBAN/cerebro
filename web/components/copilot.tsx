"use client";

import { useState, useRef, useEffect } from "react";
import { Panel } from "./shared";
import { postCommand } from "@/lib/api";

interface ChatMsg {
  role: "user" | "assistant";
  text: string;
}

/** Copilot is the chat transcript + command input that calls /api/command. */
export function Copilot() {
  const [msgs, setMsgs] = useState<ChatMsg[]>([]);
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(false);
  const endRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [msgs, loading]);

  const send = async () => {
    const q = input.trim();
    if (!q || loading) return;
    setMsgs((m) => [...m, { role: "user", text: q }]);
    setInput("");
    setLoading(true);
    try {
      // Plain questions go through the /ask ChatOps command; slash-commands
      // pass through verbatim.
      const cmd = q.startsWith("/") ? q : `/ask ${q}`;
      const reply = await postCommand(cmd);
      setMsgs((m) => [...m, { role: "assistant", text: reply }]);
    } catch (e) {
      setMsgs((m) => [...m, { role: "assistant", text: `Error: ${(e as Error).message}` }]);
    } finally {
      setLoading(false);
    }
  };

  return (
    <Panel title="Copilot" color="accent" className="min-h-0">
      <div className="flex h-full min-h-0 flex-col">
        <div className="scroll-y min-h-0 flex-1 space-y-2">
          {msgs.map((m, i) => (
            <div key={i} className={m.role === "user" ? "text-right" : ""}>
              <div
                className={
                  m.role === "user"
                    ? "inline-block border border-accent/40 px-2 py-1 text-fg"
                    : "whitespace-pre-wrap break-words text-fg"
                }
              >
                {m.text}
              </div>
            </div>
          ))}
          {loading && <div className="text-agent">▸ thinking…</div>}
          {msgs.length === 0 && !loading && (
            <div className="text-fg-dim">Ask the copilot about the market, positions, or strategy.</div>
          )}
          <div ref={endRef} />
        </div>
        <div className="mt-2 flex shrink-0 items-center gap-2 border-t border-border pt-2">
          <span className="text-accent">❯</span>
          <input
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && send()}
            placeholder="Ask the copilot..."
            className="flex-1 bg-transparent text-fg outline-none placeholder:text-fg-dim"
          />
        </div>
      </div>
    </Panel>
  );
}

const COMMANDS: { cmd: string; color: string; label: string }[] = [
  { cmd: "/status", color: "text-fg", label: "/status" },
  { cmd: "/positions", color: "text-fg", label: "/positions" },
  { cmd: "/pause", color: "text-warn", label: "/pause" },
  { cmd: "/resume", color: "text-bull", label: "/resume" },
  { cmd: "/flatten", color: "text-bear", label: "/flatten" },
];

/** ChatOpsControls renders the command buttons + reply line. /flatten asks for
 *  confirmation first since it closes every open position. */
export function ChatOpsControls() {
  const [reply, setReply] = useState("");
  const [busy, setBusy] = useState(false);

  const run = async (cmd: string) => {
    if (busy) return;
    if (cmd === "/flatten" && !window.confirm("Flatten ALL open positions? This cannot be undone.")) {
      return;
    }
    setBusy(true);
    setReply(`running ${cmd}…`);
    try {
      setReply(await postCommand(cmd));
    } catch (e) {
      setReply(`Error: ${(e as Error).message}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Panel title="ChatOps Controls" color="bear">
      <div className="flex flex-wrap gap-2">
        {COMMANDS.map((c) => (
          <button
            key={c.cmd}
            onClick={() => run(c.cmd)}
            disabled={busy}
            className={`border border-border px-2 py-1 ${c.color} hover:bg-bg-alt disabled:opacity-50`}
          >
            {c.label}
          </button>
        ))}
      </div>
      {reply && (
        <pre className="mt-2 max-h-32 overflow-y-auto whitespace-pre-wrap break-words border-t border-border pt-2 text-2xs text-fg-dim">
          {reply}
        </pre>
      )}
    </Panel>
  );
}
