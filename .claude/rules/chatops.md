# ChatOps (Telegram & Discord)

## Overview

Telegram and Discord bots provide operator control and monitoring of the trading system. Bots support 12 commands with permission levels, real-time alerts, and agent interaction.

## Bot Adapters

| Platform | Package | Token Config |
|----------|---------|--------------|
| Telegram | `internal/adapter/telegram/` | `TELEGRAM_BOT_TOKEN` in `secrets.env` |
| Discord | `internal/adapter/discord/` | `DISCORD_BOT_TOKEN` in `secrets.env` |

Both implement the same command interface with platform-specific message formatting.

## Commands (12 total)

| Command | Permission | Purpose |
|---------|-----------|---------|
| `/status` | Operator | Current engine status, positions, P&L |
| `/positions` | Operator | List all open positions with details |
| `/close <symbol>` | Operator | Close specific position |
| `/halt` | Admin | Activate kill switch, halt all trading |
| `/resume` | Admin | Deactivate kill switch, resume trading |
| `/screen` | Operator | Trigger screening pipeline manually |
| `/proposals` | Operator | List pending trade proposals |
| `/approve <id>` | Operator | Approve a pending proposal |
| `/reject <id>` | Operator | Reject a pending proposal |
| `/ask <question>` | Operator | Ask copilot agent a question |
| `/logs [level]` | Operator | Recent log entries filtered by level |
| `/config` | Admin | Show current configuration summary |

## Permission Levels

| Level | Who | Actions |
|-------|-----|---------|
| **Operator** | Allowlisted users | All trading operations |
| **Admin** | System owner | Kill switch, config changes |
| **Read-only** | Other users | Status viewing only |

### Allowlist

Operator allowlist configured in `app.yaml`:

```yaml
chatops:
  telegram:
    bot_token: ${TELEGRAM_BOT_TOKEN}
    operator_ids: [123456789, 987654321]
    admin_ids: [123456789]
  discord:
    bot_token: ${DISCORD_BOT_TOKEN}
    operator_role: "Trader"
    admin_role: "Admin"
```

## Alert Types

Bots send proactive alerts for:

| Alert | Trigger | Severity |
|-------|---------|----------|
| Position opened | New trade executed | INFO |
| Position closed | Trade closed (any reason) | INFO |
| Risk rejection | Signal rejected by risk gate | WARN |
| Kill switch | Trading halted | ERROR |
| LLM failure | Provider unavailable | WARN |
| Drawdown limit | Daily drawdown threshold hit | ERROR |
| Budget warning | 80% of daily LLM budget used | WARN |
| Screening result | New screening summary published | INFO |
| WebSocket down | Exchange connection lost | ERROR |
| Agent error | Agent execution failed | WARN |

## Copilot Interaction

The `/ask` command routes questions to the Copilot agent:

```
User: /ask What's the current BTC outlook?
Bot: 🤖 Analyzing BTC/USDT...

Based on current market data:
- RSI: 42 (neutral)
- MACD: bearish crossover forming
- Support: $42,000 | Resistance: $44,500
- Bias: Neutral-to-bearish short term

Recommendation: Wait for clearer setup at support level.
```

## Message Formatting

### Telegram

Uses Markdown formatting:
- **Bold** for symbols and key values
- `Monospace` for numbers and IDs
- Emoji for status indicators (🟢/🔴/⚠️)

### Discord

Uses embeds:
- Color-coded embeds (green/red/yellow)
- Fields for structured data
- Thumbnails for status indicators

## Error Handling

- **Rate limiting**: Respect platform rate limits, queue messages
- **Connection loss**: Reconnect with exponential backoff + jitter
- **Command errors**: Reply with error message, log to system logs
- **Permission denied**: Silent reject or "Access denied" message
