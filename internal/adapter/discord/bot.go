package discord

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/azhar/cerebro/internal/port"
	"github.com/bwmarrin/discordgo"
)

// Bot is the Discord operator interface.
type Bot struct {
	session     *discordgo.Session
	guildID     string
	channelIDs  map[string]string // NotifyChannel → Discord channel ID
	allowlistFn func(actorID string) bool
	dispatchFn  func(ctx context.Context, actorID, message string) string
}

// New creates and opens a Discord Bot session.
func New(token string) (*Bot, error) {
	s, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("discord: create session: %w", err)
	}
	return &Bot{session: s, channelIDs: make(map[string]string)}, nil
}

// SetGuildID restricts command listening to a specific server.
func (b *Bot) SetGuildID(id string) { b.guildID = id }

// SetChannel maps a NotifyChannel name to a Discord channel ID.
func (b *Bot) SetChannel(name, channelID string) { b.channelIDs[name] = channelID }

// SetAllowlist wires the actor allowlist function.
func (b *Bot) SetAllowlist(fn func(actorID string) bool) { b.allowlistFn = fn }

// SetDispatcher wires the ChatOps dispatcher.
func (b *Bot) SetDispatcher(fn func(ctx context.Context, actorID, message string) string) {
	b.dispatchFn = fn
}

// Run opens the Discord gateway and begins listening for messages.
// Blocks until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	b.session.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if m.Author.ID == s.State.User.ID {
			return
		}
		// Only process messages starting with /
		if !strings.HasPrefix(m.Content, "/") {
			return
		}

		actorID := fmt.Sprintf("discord:%s", m.Author.ID)
		if b.allowlistFn != nil && !b.allowlistFn(actorID) {
			slog.Warn("discord: unauthorised actor", "actor", actorID)
			_, _ = s.ChannelMessageSend(m.ChannelID, "Access denied.")
			return
		}

		var reply string
		if b.dispatchFn != nil {
			reply = b.dispatchFn(ctx, actorID, m.Content)
		} else {
			reply = "Dispatcher not configured."
		}
		_, _ = s.ChannelMessageSend(m.ChannelID, reply)
	})

	if err := b.session.Open(); err != nil {
		return fmt.Errorf("discord: open session: %w", err)
	}
	slog.Info("discord bot connected")

	<-ctx.Done()
	slog.Info("discord bot disconnecting")
	return b.session.Close()
}

// Send implements port.Notifier for system alerts.
func (b *Bot) Send(_ context.Context, channel port.NotifyChannel, message string) error {
	channelID, ok := b.channelIDs[string(channel)]
	if !ok {
		slog.Warn("discord: no channel configured", "channel", channel)
		return nil
	}
	_, err := b.session.ChannelMessageSend(channelID, message)
	return err
}

// SendEmbed implements port.Notifier with a rich Discord embed.
func (b *Bot) SendEmbed(_ context.Context, channel port.NotifyChannel, title, body string, fields map[string]string) error {
	channelID, ok := b.channelIDs[string(channel)]
	if !ok {
		return nil
	}

	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: body,
	}
	for name, val := range fields {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   name,
			Value:  val,
			Inline: true,
		})
	}

	_, err := b.session.ChannelMessageSendEmbed(channelID, embed)
	return err
}
