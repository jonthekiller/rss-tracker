package bot

import (
	"encoding/json"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/jonthekiller/rss-tracker/internal/feed"
	"github.com/jonthekiller/rss-tracker/internal/store"
)

// findFocusedOption locates the autocomplete-focused string option, which is
// nested under the subcommand (and possibly subcommand group) levels.
func findFocusedOption(opts []*discordgo.ApplicationCommandInteractionDataOption) (string, string) {
	for _, opt := range opts {
		if opt.Focused && opt.Type == discordgo.ApplicationCommandOptionString {
			value, _ := opt.Value.(string)
			return opt.Name, value
		}
		if len(opt.Options) > 0 {
			if name, value := findFocusedOption(opt.Options); name != "" {
				return name, value
			}
		}
	}
	return "", ""
}

// handleAutocomplete responds to feed-id autocomplete requests with the feeds
// of channels the user can manage.
func handleAutocomplete(ctx *BotContext, s *discordgo.Session, i *discordgo.Interaction) {
	data := i.ApplicationCommandData()
	focusedName, focusedValue := findFocusedOption(data.Options)

	choices := []*discordgo.ApplicationCommandOptionChoice{}
	if focusedName == "id" {
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
		query := strings.ToLower(focusedValue)
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

// handleModalSubmit processes feed-template, feed-embed and feed-source modals.
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
	if customID == "feed-source" {
		raw := strings.TrimSpace(modalFieldValue(data, "config"))
		var configPatch *string
		if raw != "" {
			if _, err := feed.ParseExtractConfig(&raw); err != nil {
				_ = s.InteractionRespond(i, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "❌ " + err.Error(), Flags: ephemeralFlags,
					},
				})
				return
			}
			v := raw
			configPatch = &v
		}
		patch := store.FeedPatch{ExtractConfig: &configPatch}
		// Adding an extraction config to an rss feed converts it to auto.
		if raw != "" && (feedRow.SourceType == "" || feedRow.SourceType == store.SourceTypeRSS) {
			autoType := store.SourceTypeAuto
			patch.SourceType = &autoType
		}
		if err := ctx.Store.UpdateFeed(feedID, patch); err != nil {
			log.Error("modal update failed", "err", err)
		}
		content := "✅ Extraction config cleared for **" + feedRow.Name + "** (defaults apply)."
		if raw != "" {
			content = "✅ Extraction config updated for **" + feedRow.Name + "**. Test with `/feed test` or `/debug feed`."
		}
		_ = s.InteractionRespond(i, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: content, Flags: ephemeralFlags},
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
// Discord unmarshals rows and inputs as pointers (*ActionsRow/*TextInput),
// so both forms are accepted.
func modalFieldValue(data discordgo.ModalSubmitInteractionData, customID string) string {
	for _, component := range data.Components {
		row := asActionsRow(component)
		if row == nil {
			continue
		}
		for _, inner := range row.Components {
			if input := asTextInput(inner); input != nil && input.CustomID == customID {
				return input.Value
			}
		}
	}
	return ""
}

func asActionsRow(component discordgo.MessageComponent) *discordgo.ActionsRow {
	switch row := component.(type) {
	case discordgo.ActionsRow:
		return &row
	case *discordgo.ActionsRow:
		return row
	default:
		return nil
	}
}

func asTextInput(component discordgo.MessageComponent) *discordgo.TextInput {
	switch input := component.(type) {
	case discordgo.TextInput:
		return &input
	case *discordgo.TextInput:
		return input
	default:
		return nil
	}
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
