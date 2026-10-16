package bot

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

// subOption builds a SubCommand data option with nested options, as Discord
// sends them for slash command interactions.
func subOption(name string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{
		Name:    name,
		Type:    discordgo.ApplicationCommandOptionSubCommand,
		Options: opts,
	}
}

func groupOption(name string, subs ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{
		Name:    name,
		Type:    discordgo.ApplicationCommandOptionSubCommandGroup,
		Options: subs,
	}
}

func strOpt(name, value string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{
		Name: name, Type: discordgo.ApplicationCommandOptionString, Value: value,
	}
}

func TestOptionsDescendIntoSubcommand(t *testing.T) {
	// /feed test id:abc → Data.Options = [sub "test" [id "abc"]]
	c := &icc{Data: discordgo.ApplicationCommandInteractionData{
		Name: "feed",
		Options: []*discordgo.ApplicationCommandInteractionDataOption{
			subOption("test", strOpt("id", "abc")),
		},
	}}
	group, sub := c.subcommand()
	if group != "" || sub != "test" {
		t.Errorf("subcommand = %q/%q", group, sub)
	}
	if got := deref(c.optString("id")); got != "abc" {
		t.Errorf("id option = %q, want %q (options must descend into the subcommand)", got, "abc")
	}
}

func TestOptionsDescendIntoSubcommandGroup(t *testing.T) {
	// /feed filter add kind:include → [group "filter" [sub "add" [kind ...]]]
	c := &icc{Data: discordgo.ApplicationCommandInteractionData{
		Name: "feed",
		Options: []*discordgo.ApplicationCommandInteractionDataOption{
			groupOption("filter",
				subOption("add",
					strOpt("id", "feed-uuid"),
					strOpt("kind", "include"),
					strOpt("field", "title"),
					strOpt("text", "needle"),
				),
			),
		},
	}}
	group, sub := c.subcommand()
	if group != "filter" || sub != "add" {
		t.Errorf("subcommand = %q/%q", group, sub)
	}
	if got := deref(c.optString("kind")); got != "include" {
		t.Errorf("kind = %q", got)
	}
	if got := deref(c.optString("text")); got != "needle" {
		t.Errorf("text = %q", got)
	}
	if hasOption(c, "regex") {
		t.Error("absent option must not be found")
	}
}

func TestOptionsPlainSubcommandValues(t *testing.T) {
	// /debug feed id:x and /backup export all_channels:true
	c := &icc{Data: discordgo.ApplicationCommandInteractionData{
		Name: "debug",
		Options: []*discordgo.ApplicationCommandInteractionDataOption{
			subOption("feed", strOpt("id", "uuid-1")),
		},
	}}
	if _, sub := c.subcommand(); sub != "feed" {
		t.Errorf("sub = %q", sub)
	}
	if got := deref(c.optString("id")); got != "uuid-1" {
		t.Errorf("id = %q", got)
	}

	c2 := &icc{Data: discordgo.ApplicationCommandInteractionData{
		Name: "backup",
		Options: []*discordgo.ApplicationCommandInteractionDataOption{
			subOption("export",
				&discordgo.ApplicationCommandInteractionDataOption{
					Name: "all_channels", Type: discordgo.ApplicationCommandOptionBoolean, Value: true,
				},
			),
		},
	}}
	if v := c2.optBool("all_channels"); v == nil || !*v {
		t.Errorf("all_channels = %v", v)
	}
}

func TestFindFocusedOptionNested(t *testing.T) {
	// Autocomplete on /feed test id:<query> — the focused option is nested
	// under the subcommand.
	data := discordgo.ApplicationCommandInteractionData{
		Name: "feed",
		Options: []*discordgo.ApplicationCommandInteractionDataOption{
			subOption("test", &discordgo.ApplicationCommandInteractionDataOption{
				Name: "id", Type: discordgo.ApplicationCommandOptionString,
				Value: "git", Focused: true,
			}),
		},
	}
	name, value := findFocusedOption(data.Options)
	if name != "id" || value != "git" {
		t.Errorf("focused = %q/%q", name, value)
	}

	// Nested two levels deep (/feed filter add id:<query>).
	data2 := discordgo.ApplicationCommandInteractionData{
		Name: "feed",
		Options: []*discordgo.ApplicationCommandInteractionDataOption{
			groupOption("filter", subOption("add", &discordgo.ApplicationCommandInteractionDataOption{
				Name: "id", Type: discordgo.ApplicationCommandOptionString,
				Value: "prom", Focused: true,
			})),
		},
	}
	name, value = findFocusedOption(data2.Options)
	if name != "id" || value != "prom" {
		t.Errorf("focused (group) = %q/%q", name, value)
	}

	// No focused option.
	if name, _ := findFocusedOption(data.Options[:0]); name != "" {
		t.Errorf("empty options must yield no focused name, got %q", name)
	}
}

func TestCommandsAreGuildOnly(t *testing.T) {
	for _, cmd := range buildCommandPayloads() {
		if cmd.DMPermission == nil || *cmd.DMPermission {
			t.Errorf("command %s must disable DM permission", cmd.Name)
		}
	}
}
