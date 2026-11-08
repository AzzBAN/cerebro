# Web Frontend (Next.js Dashboard)

## Tech Stack

- **Framework**: Next.js 14 (App Router)
- **React**: 18
- **Language**: TypeScript
- **Styling**: Tailwind CSS
- **State**: React Query (TanStack Query)
- **Real-time**: Native WebSocket API

## Directory Structure

```
web/
├── app/
│   ├── layout.tsx          # Root layout with providers
│   ├── page.tsx            # Dashboard (redirect)
│   ├── dashboard/
│   │   └── page.tsx        # Portfolio overview
│   ├── market/
│   │   └── page.tsx        # Market data + screening
│   ├── logs/
│   │   └── page.tsx        # Agent runs + audit log
│   ├── agents/
│   │   └── page.tsx        # Agent status + costs
│   └── config/
│       └── page.tsx        # Config viewer
├── components/
│   ├── ui/                 # Reusable UI components
│   ├── dashboard/          # Dashboard-specific
│   ├── market/             # Market-specific
│   └── shared/             # Cross-tab components
├── lib/
│   ├── api.ts              # API client (fetch wrapper)
│   ├── websocket.ts        # WebSocket connection manager
│   └── types.ts            # TypeScript type definitions
└── tailwind.config.ts
```

## 5 Tabs

| Tab | Route | Purpose |
|-----|-------|---------|
| Dashboard | `/dashboard` | Portfolio value, open positions, PnL chart |
| Market | `/market` | Real-time prices, screening results, news feed |
| Logs | `/logs` | Agent runs, audit events, error log |
| Agents | `/agents` | Agent status, cost tracking, tool usage |
| Config | `/config` | View current config (read-only) |

## API Communication

### REST Endpoints

All endpoints prefixed with `/api`:

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/api/positions` | GET | Fetch open positions |
| `/api/trades` | GET | Fetch trade history |
| `/api/agent-runs` | GET | Fetch agent run logs |
| `/api/audit-events` | GET | Fetch audit events |
| `/api/screening` | GET | Fetch screening results |
| `/api/config` | GET | Fetch current config |
| `/api/market-data` | GET | Fetch market snapshot |

### WebSocket Events

Connect to `ws://localhost:8080/ws`:

```typescript
// Event types
type WSMessage = 
  | { type: 'position_update'; data: Position }
  | { type: 'trade_closed'; data: Trade }
  | { type: 'agent_run'; data: AgentRun }
  | { type: 'market_data'; data: MarketSnapshot }
  | { type: 'alert'; data: Alert };
```

Client reconnects automatically with exponential backoff.

## Component Hierarchy

```
<RootLayout>
  <QueryClientProvider>
    <WebSocketProvider>
      <Sidebar />
      <main>
        <DashboardPage>
          <PortfolioChart />
          <PositionTable />
          <PnLSummary />
        </DashboardPage>
      </main>
    </WebSocketProvider>
  </QueryClientProvider>
</RootLayout>
```

## UI Components

Reusable components in `components/ui/`:

| Component | Purpose |
|-----------|---------|
| `Card` | Container with shadow + padding |
| `Button` | Action button (primary/secondary/danger) |
| `Table` | Data table with sorting |
| `Badge` | Status indicators (success/warning/error) |
| `Chart` | Recharts wrapper (line/bar/area) |
| `Modal` | Dialog overlay |
| `Toast` | Notification popup |

## Styling

Tailwind CSS with custom theme:

```typescript
// tailwind.config.ts
export default {
  theme: {
    extend: {
      colors: {
        primary: '#3b82f6',
        success: '#10b981',
        warning: '#f59e0b',
        danger: '#ef4444',
      },
    },
  },
};
```

Dark mode: Use `dark:` prefix (not implemented yet).

## Type Definitions

All types in `lib/types.ts`:

```typescript
export interface Position {
  id: string;
  symbol: string;
  side: 'BUY' | 'SELL';
  qty: string;
  entryPrice: string;
  currentPrice: string;
  pnl: string;
  openedAt: string;
}

export interface Trade {
  id: string;
  symbol: string;
  side: 'BUY' | 'SELL';
  qty: string;
  entryPrice: string;
  exitPrice: string;
  pnl: string;
  fees: string;
  openedAt: string;
  closedAt: string;
}

export interface AgentRun {
  id: string;
  agentName: string;
  startedAt: string;
  completedAt: string;
  status: 'running' | 'completed' | 'failed';
  inputTokens: number;
  outputTokens: number;
  costUSD: string;
}
```

All monetary values are `string` (decimal serialization).

## Development

```bash
# Install dependencies
cd web && npm install

# Dev server (hot reload)
npm run dev

# Build production
npm run build

# Lint
npm run lint

# Type check
npm run type-check
```

## Conventions

- Use **server components** by default (no `use client` unless needed)
- Use **React Query** for data fetching (not `useEffect`)
- Use **TypeScript strict mode** (no `any`)
- Use **named exports** (not default exports)
- Use **PascalCase** for components, **camelCase** for functions
- Use **semantic HTML** (`<button>`, not `<div onClick>`)
