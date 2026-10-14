package bot

import (
	"encoding/json"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/jonthekiller/rss-tracker/internal/store"
)

// handleAutocomplete responds to feed-id autocomplete requests with the feeds
// of channels the user can manage.
func handleAutocomplete(ctx *BotContext, s *discordgo.Session, i *discordgo.Interaction) {
	data := i.ApplicationCommandData()
	focused := ""
	for _, opts := range [][]*discordgo.ApplicationCommandInteractionDataOption{data.Options} {
		for _, opt := range opts {
			if opt.Type == discordgo.ApplicationCommandOptionString && opt.Focused {
				focused = opt.Name
			}
		}
	}
	choices := []*discordgo.ApplicationCommandOptionChoice{}
	if focused == "id" {
		feeds, _ := ctx.Store.GetFeedsForGuild(i.GuildID)
		member := i.Member
		// Only suggest feeds of channels the user can manage (write access or admin).
		for _, feedRow := range feeds {
			if feedRow.GuildID != i.GuildID {
				continue
			}
			if !isAdmin(i) && member != nil {
				perms, err := s.UserChannelPermissions(member.User.ID, feedRow.ChannelID)
				if err != nil ||
					perms&discordgo.PermissionViewChannel == 0 ||
					perms&discordgo.PermissionSendMessages == 0 {
					continue
				}
			}
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
				Name:  feedRow.Name + " (" + shortID(feedRow.ID) + ")",
				Value: feedRow.ID,
			})
		}
		query := strings.ToLower(focusedValue(i))
		filtered := choices[:0]
		for _, choice := range choices {
			name := strings.ToLower(choice.Name)
			id, _ := choice.Value.(string)
			if strings.Contains(name, query) || strings.HasPrefix(id, query) {
				filtered = append(filtered, choice)
			}
		}
		choices = filtered
		if len(choices) > 25 {
			choices = choices[:25]
		}
	}

	_ = s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	})
}

func focusedValue(i *discordgo.Interaction) string {
	data := i.ApplicationCommandData()
	for _, opt := range data.Options {
		if opt.Focused {
			if v, ok := opt.Value.(string); ok {
				return strings.ToLower(v)
			}
		}
	}
	return ""
}

// handleModalSubmit processes feed-template and feed-embed modals.
func handleModalSubmit(ctx *BotContext, s *discordgo.Session, i *discordgo.Interaction) {
	data := i.ModalSubmitData()
	parts := strings.SplitN(data.CustomID, ":", 2)
	if len(parts) != 2 || parts[1] == "" {
		return
	}
	customID, feedID := parts[0], parts[1]
	if i.GuildID == "" {
		return
	}

	feedRow, err := ctx.Store.GetFeed(feedID)
	if err != nil || feedRow == nil {
		_ = s.InteractionRespond(i, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: "Feed no longer exists.", Flags: ephemeralFlags},
		})
		return
	}
	// Modals edit a feed's template: enforce the channel-scoped permission.
	if feedRow.GuildID != i.GuildID || !ctx.canManageChannel(i, feedRow.ChannelID) {
		_ = s.InteractionRespond(i, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "🔒 You need the **Send Messages** permission in <#" + feedRow.ChannelID + "> to edit this feed's templates.",
				Flags:   ephemeralFlags,
			},
		})
		return
	}

	value := modalFieldValue(data, "template")
	if customID == "feed-template" {
		if err := ctx.Store.UpdateFeed(feedID, store.FeedPatch{TemplateText: &value}); err != nil {
			log.Error("modal update failed", "err", err)
		}
		_ = s.InteractionRespond(i, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "✅ Template updated for **" + feedRow.Name + "**.", Flags: ephemeralFlags,
			},
		})
		return
	}
	if customID == "feed-embed" {
		raw := strings.TrimSpace(modalFieldValue(data, "embed"))
		if raw != "" {
			var probe any
			if err := json.Unmarshal([]byte(raw), &probe); err != nil {
				_ = s.InteractionRespond(i, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "❌ Invalid JSON: " + err.Error(), Flags: ephemeralFlags,
					},
				})
				return
			}
		}
		var embedPatch *string
		if raw != "" {
			v := raw
			embedPatch = &v
		}
		if err := ctx.Store.UpdateFeed(feedID, store.FeedPatch{EmbedJSON: &embedPatch}); err != nil {
			log.Error("modal update failed", "err", err)
		}
		content := "✅ Embed removed for **" + feedRow.Name + "**."
		if raw != "" {
			content = "✅ Embed updated for **" + feedRow.Name + "**."
		}
		_ = s.InteractionRespond(i, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: content, Flags: ephemeralFlags},
		})
	}
}

// modalFieldValue extracts a text input value from the modal components.
func modalFieldValue(data discordgo.ModalSubmitInteractionData, customID string) string {
	for _, component := range data.Components {
		row, ok := component.(discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, inner := range row.Components {
			if input, ok := inner.(discordgo.TextInput); ok && input.CustomID == customID {
				return input.Value
			}
		}
	}
	return ""
}

// route dispatches an application command interaction by name.
func routeCommand(ctx *BotContext, s *discordgo.Session, i *discordgo.InteractionCreate) {
	c := newICC(s, i.Interaction)
	switch c.Data.Name {
	case "feed":
		handleFeed(ctx, c)
	case "import":
		handleImport(ctx, c)
	case "backup":
		handleBackup(ctx, c)
	case "settings":
		handleSettings(ctx, c)
	case "status":
		handleStatus(ctx, c)
	case "debug":
		handleDebug(ctx, c)
	default:
		_ = c.replyEphemeral("Unknown command.")
	}
}
