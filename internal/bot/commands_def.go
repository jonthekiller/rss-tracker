package bot

import (
	"github.com/bwmarrin/discordgo"
)

// feedIDOption is the shared autocomplete feed id option.
func feedIDOption() *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:         discordgo.ApplicationCommandOptionString,
		Name:         "id",
		Description:  "Feed id (autocomplete)",
		Required:     true,
		Autocomplete: true,
	}
}

func cmdFeed() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		DMPermission: ptrFalse(),
		Name:         "feed",
		Description:  "Manage RSS feeds",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "add",
				Description: "Add a feed to a channel",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionString, Name: "url", Description: "Feed URL", Required: true},
					{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Target channel (default: current channel)"},
					{Type: discordgo.ApplicationCommandOptionString, Name: "name", Description: "Feed display name"},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "list",
				Description: "List feeds (of a channel or the whole server)",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Only this channel"},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "remove",
				Description: "Remove a feed",
				Options:     []*discordgo.ApplicationCommandOption{feedIDOption()},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "show",
				Description: "Show a feed configuration",
				Options:     []*discordgo.ApplicationCommandOption{feedIDOption()},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "pause",
				Description: "Pause a feed",
				Options:     []*discordgo.ApplicationCommandOption{feedIDOption()},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "resume",
				Description: "Resume a feed",
				Options:     []*discordgo.ApplicationCommandOption{feedIDOption()},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "move",
				Description: "Move a feed to another channel",
				Options: []*discordgo.ApplicationCommandOption{
					feedIDOption(),
					{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "New channel", Required: true},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "template",
				Description: "Edit the message template",
				Options:     []*discordgo.ApplicationCommandOption{feedIDOption()},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "embed",
				Description: "Edit the embed template (JSON)",
				Options:     []*discordgo.ApplicationCommandOption{feedIDOption()},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "frequency",
				Description: "Check frequency in minutes",
				Options: []*discordgo.ApplicationCommandOption{
					feedIDOption(),
					{
						Type: discordgo.ApplicationCommandOptionInteger, Name: "minutes",
						Description: "Interval in minutes (min 2)", Required: true,
						MinValue: ptrF64(2), MaxValue: 1440,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "batch",
				Description: "Burst handling mode",
				Options: []*discordgo.ApplicationCommandOption{
					feedIDOption(),
					{
						Type: discordgo.ApplicationCommandOptionString, Name: "mode",
						Description: "auto: digest above threshold", Required: true,
						Choices: []*discordgo.ApplicationCommandOptionChoice{
							{Name: "auto", Value: "auto"},
							{Name: "individual", Value: "individual"},
							{Name: "digest", Value: "digest"},
						},
					},
					{
						Type: discordgo.ApplicationCommandOptionInteger, Name: "threshold",
						Description: "Digest threshold (auto mode)", MinValue: ptrF64(2),
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
				Name:        "filter",
				Description: "Manage content filters",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "add",
						Description: "Add a filter",
						Options: []*discordgo.ApplicationCommandOption{
							feedIDOption(),
							{
								Type: discordgo.ApplicationCommandOptionString, Name: "kind",
								Description: "include: only matching articles; exclude: drop matching articles",
								Required:    true,
								Choices: []*discordgo.ApplicationCommandOptionChoice{
									{Name: "include", Value: "include"},
									{Name: "exclude", Value: "exclude"},
								},
							},
							{
								Type: discordgo.ApplicationCommandOptionString, Name: "field",
								Description: "Field to match", Required: true,
								Choices: []*discordgo.ApplicationCommandOptionChoice{
									{Name: "title", Value: "title"},
									{Name: "description", Value: "description"},
									{Name: "any", Value: "any"},
								},
							},
							{Type: discordgo.ApplicationCommandOptionString, Name: "text", Description: "Keyword or regex", Required: true},
							{Type: discordgo.ApplicationCommandOptionBoolean, Name: "regex", Description: "Treat the text as a regex"},
						},
					},
					{
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "remove",
						Description: "Remove a filter by id (see /feed show)",
						Options: []*discordgo.ApplicationCommandOption{
							feedIDOption(),
							{Type: discordgo.ApplicationCommandOptionInteger, Name: "filter_id", Description: "Filter id", Required: true},
						},
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "test",
				Description: "Simulate a send with the current template (no state change)",
				Options: []*discordgo.ApplicationCommandOption{
					feedIDOption(),
					{
						Type: discordgo.ApplicationCommandOptionInteger, Name: "n",
						Description: "Number of articles (1-5)", MinValue: ptrF64(1), MaxValue: 5,
					},
					{
						Type: discordgo.ApplicationCommandOptionString, Name: "mode",
						Description: "preview: ephemeral only; send: real message in this channel",
						Choices: []*discordgo.ApplicationCommandOptionChoice{
							{Name: "preview (ephemeral)", Value: "preview"},
							{Name: "send to this channel", Value: "send"},
						},
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "refresh",
				Description: "Check a feed immediately",
				Options:     []*discordgo.ApplicationCommandOption{feedIDOption()},
			},
		},
	}
}

func cmdImport() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		DMPermission: ptrFalse(),
		Name:         "import",
		Description:  "Import data from an external bot",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "monitorss",
				Description: "Import a MonitoRSS backup (JSON attachment)",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionAttachment, Name: "file", Description: "MonitoRSS backup .json", Required: true},
					{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Fallback destination channel"},
				},
			},
		},
	}
}

func cmdBackup() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		DMPermission: ptrFalse(),
		Name:         "backup",
		Description:  "Backup and restore configuration",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "export",
				Description: "Export configuration as JSON (this channel by default)",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Export this channel only (default: current)"},
					{Type: discordgo.ApplicationCommandOptionBoolean, Name: "all_channels", Description: "Export every channel (requires Manage Server)"},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "restore",
				Description: "Restore from a backup JSON file",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionAttachment, Name: "file", Description: "Backup .json file", Required: true},
				},
			},
		},
	}
}

func cmdSettings() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		DMPermission: ptrFalse(),
		Name:         "settings",
		Description:  "Bot settings",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "alert",
				Description: "Where to notify when a feed of THIS channel fails (run the command in that channel)",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "User to DM on failures (empty to clear)"},
					{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Channel to post failures in (empty to clear)"},
					{Type: discordgo.ApplicationCommandOptionBoolean, Name: "enabled", Description: "Turn failure alerts for this channel on/off (default: on)"},
					{
						Type: discordgo.ApplicationCommandOptionInteger, Name: "threshold",
						Description: "Consecutive failures before alerting for this channel (default: global setting)",
						MinValue:    ptrF64(1), MaxValue: 100,
					},
				},
			},
		},
	}
}

func cmdDebug() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		DMPermission: ptrFalse(),
		Name:         "debug",
		Description:  "Diagnostics for troubleshooting",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "feed",
				Description: "Dry-run a feed: fetch now and report what would happen (no state change)",
				Options:     []*discordgo.ApplicationCommandOption{feedIDOption()},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "system",
				Description: "System diagnostics (memory, queues, scheduler)",
			},
		},
	}
}

func cmdStatus() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		DMPermission: ptrFalse(),
		Name:         "status",
		Description:  "Bot health status",
	}
}

// buildCommandPayloads returns every command definition.
func buildCommandPayloads() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		cmdFeed(), cmdImport(), cmdBackup(), cmdSettings(), cmdStatus(), cmdDebug(),
	}
}

func ptrF64(v float64) *float64 { return &v }

func ptrFalse() *bool { return new(bool) }
