package bot

import (
	"github.com/bwmarrin/discordgo"
)

// Channel-scoped permission model, ported from src/bot/commands.ts: a user may
// configure the feeds of a channel if they can WRITE in that channel (view +
// send). Server administrators bypass the check.

// isAdmin reports whether the interaction member has Manage Server.
func isAdmin(i *discordgo.Interaction) bool {
	if i.Member == nil {
		return false
	}
	return i.Member.Permissions&discordgo.PermissionManageGuild != 0
}

// canManageChannel reports whether the interaction member may write in the
// given channel (view + send, role and overwrites included).
func (ctx *BotContext) canManageChannel(i *discordgo.Interaction, channelID string) bool {
	if i.GuildID == "" || i.Member == nil {
		return false
	}
	if isAdmin(i) {
		return true
	}
	perms, err := ctx.Session.UserChannelPermissions(i.Member.User.ID, channelID)
	if err != nil {
		return false
	}
	return perms&discordgo.PermissionViewChannel != 0 && perms&discordgo.PermissionSendMessages != 0
}

// channelExists reports whether a channel belongs to the given guild.
func (ctx *BotContext) channelExists(guildID, channelID string) bool {
	if guildID == "" {
		return false
	}
	guild, err := ctx.Session.State.Guild(guildID)
	if err != nil || guild == nil {
		return false
	}
	for _, ch := range guild.Channels {
		if ch.ID == channelID {
			return true
		}
	}
	return false
}
