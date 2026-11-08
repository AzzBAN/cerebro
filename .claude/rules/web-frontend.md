# Web Frontend

## Overview

High-density trading terminal dashboard built with Next.js 14 (App Router), React 18, TypeScript. Designed for static export embedded in the Go backend binary. Dark terminal aesthetic with monospace typography and 1px borders.

## Tech Stack

| Concern | Choice |
|---------|--------|
| Framework | Next.js 14 (App Router) |
| UI | React 18 + TypeScript |
| Styling | Tailwind CSS (dark theme) |
| Fonts | JetBrains Mono |
| Build | Static export (`output: 'export'`) embedded in Go binary |

## Page Structure

**Single Page Application** with one route (`/`) and 5 tab-based views:

| Tab | Component | Purpose |
|-----|-----------|---------|
| **Dashboard** | `DashboardView` | Overview: positions, proposals, P&L |
| **Market** | `MarketView` | Market watch, opportunities, bias signals |
| **Logs** | `LogsView` | System logs and agent execution logs |
| **Agents** | `AgentsView` | Agent runs, reasoning, macro data, news |
| **Config** | `ConfigView` | Strategy files editor, configuration |

## Directory Structure

```
web/
├── app/
│   ├── page.tsx           # Main terminal dashboard (client component)
│   ├── layout.tsx         # Root layout: JetBrains Mono font, metadata
│   └── globals.css        # Global styles: dark theme, thin scrollbars
├── components/
│   ├── Header.tsx         # Top bar: title, env badge, kill switch, heartbeat
│   ├── StatusBar.tsx      # Bottom bar: engine status, connection state
│   ├── DashboardView.tsx  # Positions table, P&L summary, proposals
│   ├── MarketView.tsx     # Market data, screening results, bias
│   ├── LogsView.tsx       # System log viewer with level filtering
│   ├── AgentsView.tsx     # Agent run history, reasoning, tools
│   ├── ConfigView.tsx     # YAML config editor, strategy params
│   ├── PositionTable.tsx  # Open positions with close/tighten actions
│   ├── ProposalList.tsx   # Pending proposals with approve/reject
│   ├── SystemLog.tsx      # Scrollable log viewer with level badges
│   └── LevelBadge.tsx     # Colored tag (ERROR/WARN/INFO/DEBUG/AGENT/ORDER)
├── lib/
│   ├── api.ts             # REST API client (fetch wrapper, error handling)
│   ├── websocket.ts       # WebSocket client for live updates
│   ├── types.ts           # TypeScript type definitions
│   ├── hooks.ts           # Custom React hooks
│   └── utils.ts           # Formatting, date, decimal helpers
└── package.json
```

## API Communication

### REST API (Commands)

```typescript
// web/lib/api.ts
const BASE_URL = process.env.NEXT_PUBLIC_API_URL || '';

async function fetchJSON<T>(path: string): Promise<T> { ... }
async function postJSON<T>(path: string, body: unknown): Promise<T> { ... }
```

### WebSocket (Live Updates)

```typescript
// web/lib/websocket.ts
class CerebroSocket {
    connect(): void;
    subscribe(channel: string, handler: (data: any) => void): void;
    disconnect(): void;
}
```

Real-time channels:
- `positions` — Open position updates
- `proposals` — New/rejected/executed proposals
- `logs` — System and agent log entries
- `market` — Price updates, screening results
- `agents` — Agent run status changes

## Component Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│ Header: Title | Env Badge | Kill Switch | Heartbeat | Tabs      │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  Dashboard Tab                                                   │
│  ┌─────────────┐ ┌─────────────┐ ┌─────────────┐              │
│  │ Positions    │ │ P&L Summary │ │ Proposals   │              │
│  └─────────────┘ └─────────────┘ └─────────────┘              │
│                                                                 │
│  Market Tab                                                      │
│  ┌─────────────┐ ┌─────────────┐ ┌─────────────┐              │
│  │ Market Data  │ │ Screening   │ │ Bias Signal │              │
│  └─────────────┘ └─────────────┘ └─────────────┘              │
│                                                                 │
│  Logs Tab                                                        │
│  ┌─────────────┐ ┌─────────────┐ ┌─────────────┐              │
│  │ System Log   │ │ Agent Logs  │ │ Chat Logs   │              │
│  └─────────────┘ └─────────────┘ └─────────────┘              │
│                                                                 │
│  Agents Tab                                                      │
│  ┌─────────────┐ ┌─────────────┐ ┌─────────────┐              │
│  │ Agent Runs   │ │ Macro Data  │ │ News Feed   │              │
│  └─────────────┘ └─────────────┘ └─────────────┘              │
│                                                                 │
│  Config Tab                                                      │
│  ┌─────────────┐ ┌─────────────┐                                │
│  │ Strategy    │ │ Config      │                                │
│  │ Files Editor│ │ Viewer      │                                │
│  └─────────────┘ └─────────────┘                                │
│                                                                 │
├─────────────────────────────────────────────────────────────────┤
│ StatusBar: Engine Heartbeat | Connection State | Last Update    │
└─────────────────────────────────────────────────────────────────┘
```

## Styling Conventions

- **Dark theme**: Background `#0a0a0a`, panels `#111111`, borders `#222222`
- **Monospace**: JetBrains Mono throughout
- **Semantic colors**: Green for profit/bullish, red for loss/bearish, yellow for warnings
- **1px borders**: Thin borders for terminal aesthetic
- **Dense layout**: Maximize information density, minimal padding
- **Level badges**: ERROR (red), WARN (yellow), INFO (blue), DEBUG (gray), AGENT (purple), ORDER (green)

## Build & Embed

Frontend is built as static export and embedded in Go binary:

```go
// internal/adapter/web/server.go
//go:embed dist/*
var staticFS embed.FS
```

```bash
# Build frontend
cd web && npm run build

# Output goes to web/dist/
# Go binary embeds dist/ at compile time
```

## Environment Variables

```bash
NEXT_PUBLIC_API_URL=http://localhost:8080  # Backend API URL
NEXT_PUBLIC_WS_URL=ws://localhost:8080/ws  # WebSocket URL
```

## Key Commands

```bash
npm run dev      # Development server
npm run build    # Production build (static export)
npm run lint     # ESLint check
```
