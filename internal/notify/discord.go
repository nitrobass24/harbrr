package notify

import (
	"context"
	"fmt"
	"net/http"
	"time"
	"unicode/utf8"
)

// discordColorFailure is the red bar shown on a health-failure embed (0xE01E5A).
const discordColorFailure = 0xE0_1E_5A

// Discord rejects an embed that exceeds any of these caps with HTTP 400, and the poster
// logs-and-drops a non-2xx with no retry — so an oversize field silently loses the very
// notification that mattered (e.g. a broad login-error selector matching a whole <div>).
// We truncate (by rune) to margins BELOW Discord's documented caps: title 256,
// description 4096, field name 256, field value 1024, total embed 6000.
//
// Discord counts UTF-16 code units, not runes: an astral char is two units, so 4000
// runes could be up to 8000 units. The description is apphttp.RedactError(err) — scrubbed
// Go error text, effectively ASCII/BMP (runes ≈ units), so the 96-rune margin holds in
// practice; a pathological all-astral detail is the one residual case (the fix still
// shrinks the failure surface from "any oversize detail" to that). The total-embed 6000
// cap is not enforced separately: only the Indexer field value is length-variable (Kind
// and Event are fixed short strings), so description(<=4000) + one <=1000 slug + short
// fields + title stays well under 6000.
const (
	discordDescriptionMax = 4000 // cap 4096
	discordFieldValueMax  = 1000 // cap 1024
	discordTitleMax       = 250  // cap 256
)

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
		Title:       truncate(fmt.Sprintf("harbrr: indexer %q %s", e.Indexer, humanKind(e.Kind)), discordTitleMax),
		Description: truncate(e.Detail, discordDescriptionMax),
		Color:       discordColorFailure,
		Timestamp:   e.Timestamp.UTC().Format(time.RFC3339),
		Fields: []discordEmbedField{
			{Name: "Indexer", Value: truncate(fallback(e.Indexer, "unknown"), discordFieldValueMax), Inline: true},
			{Name: "Kind", Value: truncate(fallback(e.Kind, "unknown"), discordFieldValueMax), Inline: true},
			{Name: "Event", Value: truncate(fallback(e.Event, "unknown"), discordFieldValueMax), Inline: true},
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

// truncate caps s to at most limit runes, appending a single ellipsis rune ('…') when it
// trims so the cut is visible. It counts runes (not bytes) so a multibyte scrubbed detail
// is never sliced mid-rune. A string already within limit is returned unchanged.
func truncate(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	out := make([]rune, 0, limit)
	for _, r := range s {
		if len(out) >= limit-1 {
			break
		}
		out = append(out, r)
	}
	return string(out) + "…"
}
