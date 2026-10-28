package bot

import (
	"encoding/json"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// modalPayload builds a realistic ModalSubmit interaction data payload the
// way Discord sends it (rows and inputs unmarshal to pointers).
func modalPayload(customID, inputID, value string) []byte {
	inner := map[string]any{
		"type": 4, "custom_id": inputID, "style": 2, "value": value,
	}
	doc := map[string]any{
		"custom_id": customID,
		"components": []any{
			map[string]any{"type": 1, "components": []any{inner}},
		},
	}
	raw, _ := json.Marshal(doc)
	return raw
}

func assertModalValue(t *testing.T, customID, inputID, value, want string) {
	t.Helper()
	var data discordgo.ModalSubmitInteractionData
	if err := json.Unmarshal(modalPayload(customID, inputID, value), &data); err != nil {
		t.Fatal(err)
	}
	if got := modalFieldValue(data, inputID); got != want {
		t.Fatalf("modalFieldValue(%s) = %q, want %q (components: %T)", inputID, got, want, data.Components[0])
	}
}

func TestModalFieldValueFeedEmbed(t *testing.T) {
	assertModalValue(t, "feed-embed:f1", "embed", `{"title":"🚨 {title} — CVSS {score}"}`, `{"title":"🚨 {title} — CVSS {score}"}`)
}

func TestModalFieldValueFeedTemplate(t *testing.T) {
	assertModalValue(t, "feed-template:f1", "template", "**{title}**\n{link}", "**{title}**\n{link}")
}

func TestModalFieldValueFeedSource(t *testing.T) {
	assertModalValue(t, "feed-source:f1", "config", `{"extras":{"score":"baseScore"}}`, `{"extras":{"score":"baseScore"}}`)
}

func TestModalFieldValueMissingInput(t *testing.T) {
	assertModalValue(t, "feed-embed:f1", "embed", `{"title":"x"}`, `{"title":"x"}`)
	var data discordgo.ModalSubmitInteractionData
	if err := json.Unmarshal(modalPayload("feed-embed:f1", "embed", "v"), &data); err != nil {
		t.Fatal(err)
	}
	if got := modalFieldValue(data, "nope"); got != "" {
		t.Errorf("modalFieldValue(nope) = %q, want empty", got)
	}
}
