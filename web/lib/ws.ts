// Reconnecting WebSocket client. On (re)connect it relies on the server's
// initial "snapshot" frame to resync, so dropped connections self-heal without
// losing state. The token is passed via query param because browsers cannot
// set Authorization headers on the WebSocket handshake.

import { getToken } from "./api";

export type Listener = (type: string, data: unknown) => void;

export function connectWS(onEvent: Listener, onStatus: (up: boolean) => void): () => void {
  let ws: WebSocket | null = null;
  let closed = false;
  let retry = 0;

  const open = () => {
    if (closed) return;
    const proto = window.location.protocol === "https:" ? "wss" : "ws";
    const token = encodeURIComponent(getToken());
    ws = new WebSocket(`${proto}://${window.location.host}/ws?token=${token}`);

    ws.onopen = () => {
      retry = 0;
      onStatus(true);
    };
    ws.onmessage = (ev) => {
      try {
        const frame = JSON.parse(ev.data) as { type: string; data: unknown };
        onEvent(frame.type, frame.data);
      } catch {
        // Ignore malformed frames.
      }
    };
    ws.onclose = () => {
      onStatus(false);
      if (closed) return;
      // Exponential backoff capped at 10s.
      retry = Math.min(retry + 1, 10);
      setTimeout(open, retry * 1000);
    };
    ws.onerror = () => ws?.close();
  };

  open();

  return () => {
    closed = true;
    ws?.close();
  };
}
