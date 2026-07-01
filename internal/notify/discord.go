package notify

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// discordColorFailure is the red bar shown on a health-failure embed (0xE01E5A).
const discordColorFailure = 0xE0_1E_5A

// discordPayload is the subset of Discord's webhook execute body harbrr sends: one
// embed per event. See https://discord.com/developers/docs/resources/webhook.
type discordPayload struct {
	Embeds []discordEmbed `json:"embeds"`
}

// discordEmbed is one rich embed. Timestamp is RFC3339 (Discord's required format);
// Color is the decimal side-bar color.
type discordEmbed struct {
	Title       string              `json:"title"`
	Description string              `json:"description,omitempty"`
	Color       int                 `json:"color"`
	Fields      []discordEmbedField `json:"fields,omitempty"`
	Timestamp   string              `json:"timestamp"`
}

// discordEmbedField is one name/value row in an embed.
type discordEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

// discord is the Discord-webhook Sender: it POSTs a single embed describing the event.
type discord struct {
	p poster
}

// newDiscord builds a Discord-webhook sender for a destination URL.
func newDiscord(url string, client *http.Client) *discord {
	return &discord{p: poster{kind: "discord", url: url, client: client}}
}

// Send posts the event as a Discord embed. The indexer + kind become embed fields and
// the scrubbed detail becomes the description, so a failure reads at a glance in a
// channel.
func (d *discord) Send(ctx context.Context, e Event) error {
	embed := discordEmbed{
		Title:       fmt.Sprintf("harbrr: indexer %q %s", e.Indexer, humanKind(e.Kind)),
		Description: e.Detail,
		Color:       discordColorFailure,
		Timestamp:   e.Timestamp.UTC().Format(time.RFC3339),
		Fields: []discordEmbedField{
			{Name: "Indexer", Value: fallback(e.Indexer, "unknown"), Inline: true},
			{Name: "Kind", Value: fallback(e.Kind, "unknown"), Inline: true},
			{Name: "Event", Value: fallback(e.Event, "unknown"), Inline: true},
		},
	}
	return d.p.post(ctx, discordPayload{Embeds: []discordEmbed{embed}})
}

// humanKind renders a health-event kind for the embed title (auth_failure ->
// "auth failure"); an empty kind reads as a generic failure.
func humanKind(kind string) string {
	if kind == "" {
		return "failed"
	}
	out := make([]rune, 0, len(kind))
	for _, r := range kind {
		if r == '_' {
			r = ' '
		}
		out = append(out, r)
	}
	return string(out)
}

// fallback returns v, or def when v is empty (Discord rejects an empty field value).
func fallback(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
