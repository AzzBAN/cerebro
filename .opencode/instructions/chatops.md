# ChatOps (Telegram & Discord)

## Bot Integrations

- **Telegram**: `go-telegram-bot-api/telegram-bot-api/v5`
- **Discord**: `bvsv/dis discordgo`

Both bots share command handlers in `internal/chatops/`.

## 12 Commands

| Command | Permission | Purpose |
|---------|-----------|---------|
| `/status` | `viewer` | System status (uptime, mode, kill switch) |
| `/balance` | `viewer` | Account balances |
| `/positions` | `viewer` | Open positions |
| `/trades` | `viewer` | Recent trade history |
| `/screening` | `viewer` | Latest screening results |
| `/agents` | `operator` | Agent status + costs |
| `/propose` | `operator` | Trigger position proposal |
| `/approve` | `operator` | Approve pending proposal |
| `/reject` | `operator` | Reject pending proposal |
| `/halt` | `admin` | Activate kill switch |
| `/resume` | `admin` | Deactivate kill switch |
| `/config` | `admin` | View current config |

## Permission Levels

| Level | Commands | Default Role |
|-------|----------|--------------|
| `viewer` | Read-only (status, balance, positions, trades, screening) | All users |
| `operator` | Viewer + trading actions (agents, propose, approve, reject) | Trusted users |
| `admin` | Operator + system control (halt, resume, config) | Owner only |

Permission checked via `internal/chatops/auth.go`:

```go
type Permission int

const (
    PermViewer Permission = iota
    PermOperator
    PermAdmin
)

func (a *Auth) HasPermission(userID string, perm Permission) bool {
    userPerm, ok := a.userPerms[userID]
    if !ok {
        return perm == PermViewer // default to viewer
    }
    return userPerm >= perm
}
```

User permissions configured in `secrets.env`:

```bash
TELEGRAM_ADMIN_IDS=123456789,987654321
TELEGRAM_OPERATOR_IDS=111111111
DISCORD_ADMIN_ROLES=admin
DISCORD_OPERATOR_ROLES=trader
```

## Command Handlers

All handlers in `internal/chatops/commands/`:

```go
// internal/chatops/commands/status.go
func HandleStatus(ctx context.Context, deps Dependencies) (string, error) {
    status := deps.App.GetStatus()
    
    msg := fmt.Sprintf(
        "Status: %s\n"+
        "Mode: %s\n"+
        "Kill Switch: %v\n"+
        "Uptime: %s",
        status.Health,
        status.Environment,
        status.KillSwitch,
        status.Uptime,
    )
    
    return msg, nil
}
```

## Alert Types

Bots send alerts for 10 event types:

| Alert | Trigger | Permission |
|-------|---------|-----------|
| `trade_opened` | New position opened | `viewer` |
| `trade_closed` | Position closed | `viewer` |
| `screening_published` | Screening results ready | `viewer` |
| `proposal_pending` | New proposal awaiting approval | `operator` |
| `proposal_approved` | Proposal approved | `operator` |
| `proposal_rejected` | Proposal rejected | `operator` |
| `risk_warning` | Risk threshold breached | `operator` |
| `agent_error` | Agent execution failed | `operator` |
| `kill_switch_activated` | Kill switch turned on | `admin` |
| `system_error` | Critical system error | `admin` |

Alert routing:

```go
func (n *Notifier) Send(ctx context.Context, alert domain.Alert) error {
    // Route based on permission level
    switch alert.Permission {
    case domain.PermViewer:
        n.sendToAll(ctx, alert)
    case domain.PermOperator:
        n.sendToOperators(ctx, alert)
    case domain.PermAdmin:
        n.sendToAdmins(ctx, alert)
    }
    return nil
}
```

## Message Formatting

Use Markdown for Telegram, embeds for Discord:

```go
// Telegram
msg := tgbotapi.NewMessage(chatID, text)
msg.ParseMode = tgbotapi.ModeMarkdown

// Discord
embed := &discordgo.MessageEmbed{
    Title:       title,
    Description: description,
    Color:       color,
    Fields:      fields,
}
session.ChannelMessageSendEmbed(channelID, embed)
```

## Rate Limiting

Bots respect platform rate limits:
- Telegram: 30 messages/second to same chat
- Discord: 5 messages/5 seconds per channel

Use `internal/resilience/ratelimit.go` for throttling.

## Testing ChatOps

Mock bot APIs in tests:

```go
func TestHandleStatus(t *testing.T) {
    mockBot := &MockBot{}
    deps := Dependencies{
        App: mockApp,
        Bot: mockBot,
    }
    
    msg, err := HandleStatus(context.Background(), deps)
    if err != nil {
        t.Fatal(err)
    }
    
    if !strings.Contains(msg, "Mode: paper") {
        t.Errorf("expected mode in message, got %s", msg)
    }
}
```

## Security

- **Never** log bot tokens
- **Never** expose admin IDs in logs
- Validate user IDs against allowlist
- Sanitize user input (no HTML injection)
- Use HTTPS webhooks (not polling in production)
