// Package bot wires the Discord client: slash commands, modals, autocomplete
// and the failure-alert delivery. It is the port of src/bot/*.
package bot

import (
	"github.com/bwmarrin/discordgo"

	"github.com/jonthekiller/rss-tracker/internal/config"
	"github.com/jonthekiller/rss-tracker/internal/scheduler"
	"github.com/jonthekiller/rss-tracker/internal/sender"
	"github.com/jonthekiller/rss-tracker/internal/store"
)

// BotContext is shared by all interaction handlers.
type BotContext struct {
	Config    config.Config
	Version   string
	Store     *store.FeedStore
	Sender    *sender.SenderQueue
	Pipeline  *scheduler.Pipeline
	Scheduler *scheduler.Scheduler
	Session   *discordgo.Session
}

// StartBotContext is reserved for post-login wiring (kept as a hook so
// StartBot stays linear).
func StartBotContext(_ *BotContext) {}
