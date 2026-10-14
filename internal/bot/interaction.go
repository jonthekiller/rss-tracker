package bot

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	"github.com/bwmarrin/discordgo"
)

// icc wraps an interaction with reply helpers, mirroring the reply/deferred
// semantics of the TypeScript commands (ephemeral by default, editReply after
// defer, follow-ups for extra chunks).
type icc struct {
	Session     *discordgo.Session
	Interaction *discordgo.Interaction
	Data        discordgo.ApplicationCommandInteractionData
	deferred    bool
}

func newICC(s *discordgo.Session, i *discordgo.Interaction) *icc {
	return &icc{Session: s, Interaction: i, Data: i.ApplicationCommandData()}
}

const ephemeralFlags = discordgo.MessageFlagsEphemeral

func (c *icc) guildID() string {
	if c.Interaction.GuildID == "" {
		return ""
	}
	return c.Interaction.GuildID
}

func (c *icc) channelID() string { return c.Interaction.ChannelID }

// reply answers the interaction (ephemeral by default). After a defer, it
// edits the deferred response instead.
func (c *icc) reply(content string, ephemeral bool) error {
	if c.deferred {
		return c.editReply(content)
	}
	flags := discordgo.MessageFlags(0)
	if ephemeral {
		flags = ephemeralFlags
	}
	return c.Session.InteractionRespond(c.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content, Flags: flags},
	})
}

// replyEphemeral answers ephemerally.
func (c *icc) replyEphemeral(content string) error { return c.reply(content, true) }

// deferReply defers ephemerally so longer work (fetches) can follow.
func (c *icc) deferReply() error {
	c.deferred = true
	return c.Session.InteractionRespond(c.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: ephemeralFlags},
	})
}

// editReply edits the deferred response.
func (c *icc) editReply(content string) error {
	_, err := c.Session.InteractionResponseEdit(c.Interaction, &discordgo.WebhookEdit{Content: &content})
	return err
}

// followUpEphemeral posts an extra ephemeral chunk after the initial reply.
func (c *icc) followUpEphemeral(content string) error {
	_, err := c.Session.FollowupMessageCreate(c.Interaction, true, &discordgo.WebhookParams{
		Content: content, Flags: ephemeralFlags,
	})
	return err
}

// respondWithFile replies with a JSON attachment (backup export).
func (c *icc) respondWithFile(content string, filename string, data []byte) error {
	return c.Session.InteractionRespond(c.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   ephemeralFlags,
			Files: []*discordgo.File{{
				Name:        filename,
				ContentType: "application/json",
				Reader:      bytes.NewReader(data),
			}},
		},
	})
}

// showModal opens a modal form.
func (c *icc) showModal(customID, title string, input discordgo.TextInput) error {
	return c.Session.InteractionRespond(c.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: customID,
			Title:    title,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{input}},
			},
		},
	})
}

// ---------------------------------------------------------------------------
// Option accessors (options of the active subcommand)
// ---------------------------------------------------------------------------

// options returns the option list of the active subcommand, descending
// through the subcommand (and subcommand group) levels: Discord nests the
// actual options under the SubCommand/SubCommandGroup entries of Data.Options.
func (c *icc) options() []*discordgo.ApplicationCommandInteractionDataOption {
	opts := c.Data.Options
	for len(opts) > 0 && (opts[0].Type == discordgo.ApplicationCommandOptionSubCommandGroup ||
		opts[0].Type == discordgo.ApplicationCommandOptionSubCommand) {
		opts = opts[0].Options
	}
	return opts
}

// subcommand returns (group, sub): group is "" when there is no group level.
func (c *icc) subcommand() (string, string) {
	opts := c.Data.Options
	if len(opts) == 0 {
		return "", ""
	}
	if opts[0].Type == discordgo.ApplicationCommandOptionSubCommandGroup {
		group := opts[0].Name
		if len(opts[0].Options) > 0 {
			return group, opts[0].Options[0].Name
		}
		return group, ""
	}
	if opts[0].Type == discordgo.ApplicationCommandOptionSubCommand {
		return "", opts[0].Name
	}
	return "", ""
}

func findOption(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) *discordgo.ApplicationCommandInteractionDataOption {
	for _, opt := range opts {
		if opt.Name == name {
			return opt
		}
	}
	return nil
}

func (c *icc) optString(name string) *string {
	opt := findOption(c.options(), name)
	if opt == nil {
		return nil
	}
	value, ok := opt.Value.(string)
	if !ok {
		return nil
	}
	return &value
}

func (c *icc) optInt(name string) *int64 {
	opt := findOption(c.options(), name)
	if opt == nil {
		return nil
	}
	switch v := opt.Value.(type) {
	case int64:
		return &v
	case float64:
		n := int64(v)
		return &n
	case int:
		n := int64(v)
		return &n
	}
	return nil
}

func (c *icc) optBool(name string) *bool {
	opt := findOption(c.options(), name)
	if opt == nil {
		return nil
	}
	if v, ok := opt.Value.(bool); ok {
		return &v
	}
	return nil
}

// optChannelID returns the resolved channel id for a channel option.
func (c *icc) optChannelID(name string) *string {
	opt := findOption(c.options(), name)
	if opt == nil {
		return nil
	}
	if id, ok := opt.Value.(string); ok && id != "" {
		return &id
	}
	return nil
}

// optChannel returns the resolved channel object for a channel option.
func (c *icc) optChannel(name string) *discordgo.Channel {
	id := c.optChannelID(name)
	if id == nil {
		return nil
	}
	if c.Data.Resolved != nil {
		if ch, ok := c.Data.Resolved.Channels[*id]; ok {
			return ch
		}
	}
	ch, err := c.Session.State.Channel(*id)
	if err != nil {
		return nil
	}
	return ch
}

// optUser returns the resolved user id for a user option.
func (c *icc) optUserID(name string) *string {
	opt := findOption(c.options(), name)
	if opt == nil {
		return nil
	}
	if id, ok := opt.Value.(string); ok && id != "" {
		return &id
	}
	return nil
}

// optAttachment returns the attachment of an attachment option.
func (c *icc) optAttachment(name string) *discordgo.MessageAttachment {
	opt := findOption(c.options(), name)
	if opt == nil {
		return nil
	}
	id, ok := opt.Value.(string)
	if !ok || c.Data.Resolved == nil {
		return nil
	}
	return c.Data.Resolved.Attachments[id]
}

// getAttachmentJSON downloads and parses a .json attachment (backups).
func (c *icc) getAttachmentJSON(name string) ([]byte, error) {
	attachment := c.optAttachment(name)
	if attachment == nil {
		return nil, fmt.Errorf("Please attach a .json file")
	}
	if len(attachment.Filename) < 5 || attachment.Filename[len(attachment.Filename)-5:] != ".json" {
		return nil, fmt.Errorf("Please attach a .json file")
	}
	resp, err := http.Get(attachment.URL)
	if err != nil {
		return nil, fmt.Errorf("Could not download the attachment")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Could not download the attachment (HTTP %d)", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024+1))
	if err != nil {
		return nil, fmt.Errorf("Could not download the attachment")
	}
	if len(body) > 8*1024*1024 {
		return nil, fmt.Errorf("File too large (max 8 MB)")
	}
	return body, nil
}
