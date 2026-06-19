package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/nickawilliams/bosun/internal/notify"
	slackapi "github.com/slack-go/slack"
)

// rawBlock implements slack.Block by marshaling arbitrary JSON. This lets
// us use block types (like "card") that slack-go doesn't have native types for.
type rawBlock struct {
	blockType slackapi.MessageBlockType
	data      json.RawMessage
}

func (b rawBlock) BlockType() slackapi.MessageBlockType { return b.blockType }
func (b rawBlock) ID() string                           { return "" }
func (b rawBlock) MarshalJSON() ([]byte, error)         { return b.data, nil }

// cardBlock builds a raw "card" block with the given fields.
func cardBlock(s notify.Section, idPrefix string) rawBlock {
	card := map[string]any{
		"type": "card",
		"title": map[string]any{
			"type": "mrkdwn", "text": s.Text, "verbatim": false,
		},
	}
	if s.IconURL != "" {
		card["icon"] = map[string]any{
			"type": "image", "image_url": s.IconURL, "alt_text": "Icon",
		}
	}
	if s.Subtitle != "" {
		card["subtitle"] = map[string]any{
			"type": "mrkdwn", "text": s.Subtitle, "verbatim": false,
		}
	}
	if s.Body != "" {
		card["body"] = map[string]any{
			"type": "mrkdwn", "text": truncate(s.Body, 200), "verbatim": false,
		}
	}
	if len(s.Buttons) > 0 {
		actions := make([]map[string]any, len(s.Buttons))
		for i, btn := range s.Buttons {
			action := map[string]any{
				"type":      "button",
				"text":      map[string]any{"type": "plain_text", "text": btn.Text, "emoji": true},
				"url":       btn.URL,
				"action_id": fmt.Sprintf("%s_%d", idPrefix, i),
			}
			if btn.Style != "" {
				action["style"] = btn.Style
			}
			actions[i] = action
		}
		card["actions"] = actions
	}

	data, _ := json.Marshal(card)
	return rawBlock{blockType: "card", data: data}
}

// tableBlock builds a raw "table" block from rows of cells.
// Rows are arrays of cells (not objects with a "cells" key).
func tableBlock(rows []notify.TableRow) rawBlock {
	jsonRows := make([][]map[string]any, len(rows))
	for i, row := range rows {
		cells := make([]map[string]any, len(row.Cells))
		for j, cell := range row.Cells {
			cells[j] = buildTableCell(cell)
		}
		jsonRows[i] = cells
	}

	table := map[string]any{
		"type": "table",
		"rows": jsonRows,
	}

	data, _ := json.Marshal(table)
	return rawBlock{blockType: "table", data: data}
}

// buildTableCell converts a TableCell to a rich_text or raw_text JSON cell.
func buildTableCell(c notify.TableCell) map[string]any {
	// Emoji-only cell.
	if c.Emoji != "" && c.Text == "" {
		return map[string]any{
			"type": "rich_text",
			"elements": []map[string]any{
				{
					"type": "rich_text_section",
					"elements": []map[string]any{
						{"type": "emoji", "name": c.Emoji},
					},
				},
			},
		}
	}

	// Empty cell.
	if c.Text == "" && c.Emoji == "" {
		return map[string]any{
			"type": "rich_text",
			"elements": []map[string]any{
				{"type": "rich_text_section", "elements": []map[string]any{
					{"type": "text", "text": " "},
				}},
			},
		}
	}

	// Text cell with optional formatting.
	var elements []map[string]any

	if c.Emoji != "" {
		elements = append(elements, map[string]any{"type": "emoji", "name": c.Emoji})
		elements = append(elements, map[string]any{"type": "text", "text": " "})
	}

	if c.URL != "" {
		el := map[string]any{"type": "link", "url": c.URL, "text": c.Text}
		if c.Bold || c.Italic {
			style := map[string]any{}
			if c.Bold {
				style["bold"] = true
			}
			if c.Italic {
				style["italic"] = true
			}
			el["style"] = style
		}
		elements = append(elements, el)
	} else {
		el := map[string]any{"type": "text", "text": c.Text}
		if c.Bold || c.Italic {
			style := map[string]any{}
			if c.Bold {
				style["bold"] = true
			}
			if c.Italic {
				style["italic"] = true
			}
			el["style"] = style
		}
		elements = append(elements, el)
	}

	// Subtitle on a new line in the same cell.
	if c.Subtitle != "" {
		elements = append(elements,
			map[string]any{"type": "text", "text": "\n"},
			map[string]any{"type": "text", "text": c.Subtitle},
		)
	}

	return map[string]any{
		"type": "rich_text",
		"elements": []map[string]any{
			{
				"type":     "rich_text_section",
				"elements": elements,
			},
		},
	}
}

// truncate shortens s to max bytes, adding "…" if truncated.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// Adapter implements notify.Notifier using the Slack API.
type Adapter struct {
	client *slackapi.Client
	cache  apiCache
}

// apiCache stores results from Slack API calls to avoid redundant requests
// within a single command invocation. No TTL — the adapter is short-lived.
type apiCache struct {
	channels map[string]string          // channel name → ID
	threads  map[string]notify.ThreadRef // "channelID:issueKey" → ThreadRef
}

// New returns a new Slack adapter.
func New(token string) *Adapter {
	return &Adapter{client: slackapi.New(token, slackapi.OptionRetry(3)), cache: loadCache()}
}

// NewWithOptions returns a Slack adapter with custom options (for testing).
func NewWithOptions(token string, opts ...slackapi.Option) *Adapter {
	return &Adapter{client: slackapi.New(token, opts...)}
}

// NewWithCookie returns a Slack adapter that authenticates using a xoxc-
// token and d cookie (extracted from the Slack desktop app).
func NewWithCookie(token, cookie string) *Adapter {
	client := &http.Client{Transport: &cookieTransport{
		base:   http.DefaultTransport,
		cookie: cookie,
	}}
	return &Adapter{
		client: slackapi.New(token,
			slackapi.OptionHTTPClient(client),
			slackapi.OptionRetry(3),
		),
		cache: loadCache(),
	}
}

// Close persists the cache to disk. Should be called when the adapter is
// no longer needed (end of command).
func (a *Adapter) Close() {
	saveCache(a.cache)
}


func (a *Adapter) AuthTest(ctx context.Context) (string, error) {
	resp, err := a.client.AuthTestContext(ctx)
	if err != nil {
		return "", fmt.Errorf("auth test: %w", err)
	}
	return resp.User, nil
}

func (a *Adapter) Notify(ctx context.Context, msg notify.Message) (notify.ThreadRef, error) {
	channelID, err := a.resolveChannelID(ctx, msg.Channel)
	if err != nil {
		return notify.ThreadRef{}, err
	}

	hash := notify.ContentHash(msg.Content)
	meta := bosunMetadata(msg.IssueKey)
	opts := buildMsgOptions(msg.Content)
	opts = append(opts, slackapi.MsgOptionMetadata(meta))

	// Upsert: update an existing message if one exists for this
	// issue. The findThreadInChannel call is cached, so repeated
	// calls from Assess → Notify don't hit the API twice.
	//
	// Two update strategies depending on what the API accepts:
	//   - Block content (review/preview): chat.update works cleanly.
	//   - markdown_text content (prerelease): chat.update rejects
	//     markdown_text with internal_error in practice (despite the
	//     docs listing it as supported). When chat.update fails we
	//     fall back to chat.delete + chat.postMessage so the upsert
	//     still succeeds. Subscribers see a re-notification — that's
	//     desirable for "the announcement was updated, look again".
	if msg.IssueKey != "" {
		cacheKey := channelID + ":" + msg.IssueKey
		existing, _ := a.findThreadInChannel(ctx, channelID, msg.IssueKey)
		if existing.Timestamp != "" {
			// Skip update if content hasn't changed.
			if existing.ContentHash == hash {
				return existing, nil
			}
			_, _, _, err := a.client.UpdateMessageContext(ctx, channelID, existing.Timestamp, opts...)
			if err == nil {
				// Update the cached hash so subsequent runs detect no change.
				existing.ContentHash = hash
				if a.cache.threads == nil {
					a.cache.threads = make(map[string]notify.ThreadRef)
				}
				a.cache.threads[cacheKey] = existing
				return existing, nil
			}
			if strings.Contains(err.Error(), "message_not_found") {
				// Already gone — invalidate cache and fall through to post.
				delete(a.cache.threads, cacheKey)
			} else {
				// chat.update failed (typically internal_error for
				// markdown_text). Delete the old message and let the
				// post-fresh path below complete the upsert.
				if _, _, derr := a.client.DeleteMessageContext(ctx, channelID, existing.Timestamp); derr != nil &&
					!strings.Contains(derr.Error(), "message_not_found") {
					return notify.ThreadRef{}, fmt.Errorf("updating message (delete fallback after %v): %w", err, derr)
				}
				delete(a.cache.threads, cacheKey)
			}
		}
	}

	_, ts, err := a.client.PostMessageContext(ctx, channelID, opts...)
	if err != nil {
		return notify.ThreadRef{}, fmt.Errorf("posting message: %w", err)
	}

	// Cache the new message with its content hash.
	ref := notify.ThreadRef{Channel: channelID, Timestamp: ts, ContentHash: hash}
	cacheKey := channelID + ":" + msg.IssueKey
	if msg.IssueKey != "" {
		if a.cache.threads == nil {
			a.cache.threads = make(map[string]notify.ThreadRef)
		}
		a.cache.threads[cacheKey] = ref
	}

	return ref, nil
}

func (a *Adapter) FindThread(ctx context.Context, channel, issueKey string) (notify.ThreadRef, error) {
	channelID, err := a.resolveChannelID(ctx, channel)
	if err != nil {
		return notify.ThreadRef{}, err
	}

	return a.findThreadInChannel(ctx, channelID, issueKey)
}

// findThreadInChannel searches recent messages in a resolved channel ID for
// a bosun notification matching the issue key. Results are cached — repeated
// calls with the same parameters return the cached result without hitting the API.
func (a *Adapter) findThreadInChannel(ctx context.Context, channelID, issueKey string) (notify.ThreadRef, error) {
	cacheKey := channelID + ":" + issueKey
	if !notify.NoCache(ctx) {
		if ref, ok := a.cache.threads[cacheKey]; ok {
			return ref, nil
		}
	}

	params := &slackapi.GetConversationHistoryParameters{
		ChannelID:          channelID,
		Limit:              200,
		IncludeAllMetadata: true,
	}

	resp, err := a.client.GetConversationHistoryContext(ctx, params)
	if err != nil {
		return notify.ThreadRef{}, fmt.Errorf("fetching channel history: %w", err)
	}

	var result notify.ThreadRef

	// First pass: match on metadata (exact, reliable).
	for _, msg := range resp.Messages {
		if msg.Metadata.EventType == metadataEventType {
			if key, _ := msg.Metadata.EventPayload["issue_key"].(string); key == issueKey {
				hash, _ := msg.Metadata.EventPayload["content_hash"].(string)
				result = notify.ThreadRef{Channel: channelID, Timestamp: msg.Timestamp, ContentHash: hash}
				break
			}
		}
	}

	// Second pass: fall back to text/block content search (for messages
	// sent before metadata was added, or by other tools).
	if result.Timestamp == "" {
		for _, msg := range resp.Messages {
			if strings.Contains(msg.Text, issueKey) {
				result = notify.ThreadRef{Channel: channelID, Timestamp: msg.Timestamp}
				break
			}
			for _, block := range msg.Blocks.BlockSet {
				if section, ok := block.(*slackapi.SectionBlock); ok && section.Text != nil {
					if strings.Contains(section.Text.Text, issueKey) {
						result = notify.ThreadRef{Channel: channelID, Timestamp: msg.Timestamp}
						break
					}
				}
				if header, ok := block.(*slackapi.HeaderBlock); ok && header.Text != nil {
					if strings.Contains(header.Text.Text, issueKey) {
						result = notify.ThreadRef{Channel: channelID, Timestamp: msg.Timestamp}
						break
					}
				}
			}
			if result.Timestamp != "" {
				break
			}
		}
	}

	// Cache the result (including zero refs — "not found" is also cached).
	if a.cache.threads == nil {
		a.cache.threads = make(map[string]notify.ThreadRef)
	}
	a.cache.threads[cacheKey] = result

	return result, nil
}

// HasAnnouncement searches the channel's recent history for any
// message containing query (typically a release URL). Reuses the
// same GetConversationHistory pull that FindThread uses — no
// search.messages call, so no extra scope requirements.
//
// excludeIssueKey scopes the dedup: messages whose bosun metadata
// names that issue key are NOT counted as matches, so same-workspace
// re-runs fall through to Notify (which upserts). Empty string means
// "any message matches."
//
// Soft-fail policy: errors return (false, err) so callers can choose
// to log and proceed; a false result on error means "we don't know,
// announce conservatively." Empty query → (false, nil) without a
// network call (defensive — nothing useful to match against).
func (a *Adapter) HasAnnouncement(ctx context.Context, channel, query, excludeIssueKey string) (bool, error) {
	if query == "" {
		return false, nil
	}
	channelID, err := a.resolveChannelID(ctx, channel)
	if err != nil {
		return false, err
	}

	params := &slackapi.GetConversationHistoryParameters{
		ChannelID:          channelID,
		Limit:              200,
		IncludeAllMetadata: excludeIssueKey != "",
	}
	resp, err := a.client.GetConversationHistoryContext(ctx, params)
	if err != nil {
		return false, fmt.Errorf("fetching channel history: %w", err)
	}

	isOwn := func(msg slackapi.Message) bool {
		if excludeIssueKey == "" || msg.Metadata.EventType != metadataEventType {
			return false
		}
		key, _ := msg.Metadata.EventPayload["issue_key"].(string)
		return key == excludeIssueKey
	}

	for _, msg := range resp.Messages {
		if isOwn(msg) {
			continue
		}
		if strings.Contains(msg.Text, query) {
			return true, nil
		}
		for _, block := range msg.Blocks.BlockSet {
			if section, ok := block.(*slackapi.SectionBlock); ok && section.Text != nil {
				if strings.Contains(section.Text.Text, query) {
					return true, nil
				}
			}
			if header, ok := block.(*slackapi.HeaderBlock); ok && header.Text != nil {
				if strings.Contains(header.Text.Text, query) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

const metadataEventType = "bosun_notification"

// bosunMetadata builds the Slack message metadata for a bosun notification.
// Note: metadata is only readable with app tokens, not xoxc- user tokens.
// The text-search fallback in findThreadInChannel handles xoxc- tokens.
func bosunMetadata(issueKey string) slackapi.SlackMetadata {
	return slackapi.SlackMetadata{
		EventType: metadataEventType,
		EventPayload: map[string]any{
			"issue_key": issueKey,
		},
	}
}


func (a *Adapter) ReplyToThread(ctx context.Context, ref notify.ThreadRef, msg notify.Message) error {
	opts := buildMsgOptions(msg.Content)
	opts = append(opts, slackapi.MsgOptionTS(ref.Timestamp))

	_, _, err := a.client.PostMessageContext(ctx, ref.Channel, opts...)
	if err != nil {
		return fmt.Errorf("replying to thread: %w", err)
	}

	return nil
}

// resolveChannelID resolves a channel or user target to a Slack ID.
// Supports: "@U..." (user ID), "#channel" or "channel" (channel name lookup).
// Results are cached for the lifetime of the adapter.
func (a *Adapter) resolveChannelID(ctx context.Context, name string) (string, error) {
	// @U... — user ID, pass through directly.
	if strings.HasPrefix(name, "@") {
		return strings.TrimPrefix(name, "@"), nil
	}

	name = strings.TrimPrefix(name, "#")

	if !notify.NoCache(ctx) {
		if id, ok := a.cache.channels[name]; ok {
			return id, nil
		}
	}

	var cursor string
	for {
		params := &slackapi.GetConversationsParameters{
			Cursor:          cursor,
			Limit:           200,
			ExcludeArchived: true,
			Types:           []string{"public_channel", "private_channel"},
		}

		channels, nextCursor, err := a.client.GetConversationsContext(ctx, params)
		if err != nil {
			return "", fmt.Errorf("listing channels: %w", err)
		}

		for _, ch := range channels {
			if ch.Name == name {
				if a.cache.channels == nil {
					a.cache.channels = make(map[string]string)
				}
				a.cache.channels[name] = ch.ID
				return ch.ID, nil
			}
		}

		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}

	return "", fmt.Errorf("channel %q not found", name)
}

// buildMsgOptions constructs Slack message options from notification content.
// When block fields are set, it renders Block Kit blocks (not client-editable
// but richer formatting). When only Text is set, it sends the text via the
// top-level `markdown_text` parameter — Slack's native renderer for
// standard Markdown, which handles headings, bullets, links, and tables
// that classic mrkdwn in the `text` field doesn't render. We use the
// parameter rather than wrapping in a Block Kit markdown block because
// the parameter is documented as supported on both `chat.postMessage`
// and `chat.update` (the block isn't accepted by `chat.update`, which
// rejects it with internal_error). Slack derives the notification preview
// from `markdown_text` directly, so no separate `text` fallback is needed
// — and the parameter is documented as mutually exclusive with `text`.
func buildMsgOptions(c notify.Content) []slackapi.MsgOption {
	if !c.HasBlocks() {
		// markdown_text renders standard CommonMark, where a single `\n`
		// is a soft break that joins lines into one paragraph. The
		// generic content layer above us treats each `\n` as a visible
		// line break (matching classic mrkdwn intuition); convert
		// non-empty lines to end with the two-space Markdown hard-break
		// marker so each line renders on its own line, leaving blank
		// lines intact so paragraph breaks (`\n\n`) survive.
		text := forceHardLineBreaks(c.Text)
		return []slackapi.MsgOption{
			slackapi.MsgOptionMarkdownText(text),
		}
	}

	var blocks []slackapi.Block

	if c.Header != "" {
		blocks = append(blocks, slackapi.NewHeaderBlock(
			slackapi.NewTextBlockObject(slackapi.PlainTextType, c.Header, false, false),
		))
	}

	if c.Body != "" {
		blocks = append(blocks, slackapi.NewSectionBlock(
			slackapi.NewTextBlockObject(slackapi.MarkdownType, c.Body, false, false),
			nil, nil,
		))
	}

	for i, btn := range c.Actions {
		b := slackapi.NewButtonBlockElement(
			fmt.Sprintf("action_%d", i), "",
			slackapi.NewTextBlockObject(slackapi.PlainTextType, btn.Text, true, false),
		)
		if btn.URL != "" {
			b.WithURL(btn.URL)
		}
		if btn.Style != "" {
			b.WithStyle(slackapi.Style(btn.Style))
		}
		blocks = append(blocks, slackapi.NewActionBlock("", b))
	}

	if len(c.Table) > 0 {
		blocks = append(blocks, tableBlock(c.Table))
	}

	if len(c.Fields) > 0 {
		fields := make([]*slackapi.TextBlockObject, len(c.Fields)*2)
		for i, f := range c.Fields {
			fields[i*2] = slackapi.NewTextBlockObject(slackapi.MarkdownType, f.Key, false, false)
			fields[i*2+1] = slackapi.NewTextBlockObject(slackapi.MarkdownType, f.Value, false, false)
		}
		blocks = append(blocks, slackapi.NewSectionBlock(nil, fields, nil))
	}

	if len(c.Sections) > 0 && (c.Header != "" || c.Body != "" || len(c.Fields) > 0) {
		blocks = append(blocks, slackapi.NewDividerBlock())
	}

	for i, s := range c.Sections {
		blocks = append(blocks, cardBlock(s, fmt.Sprintf("view_%d", i)))
	}

	if c.Context != "" {
		blocks = append(blocks, slackapi.NewContextBlock("",
			slackapi.NewTextBlockObject(slackapi.MarkdownType, c.Context, false, false),
		))
	}

	// Fallback text for notifications/accessibility.
	fallback := c.Header
	if c.Body != "" {
		fallback = c.Header + " — " + c.Body
	}
	for _, f := range c.Fields {
		fallback += "\n" + f.Key + ": " + f.Value
	}
	for _, s := range c.Sections {
		fallback += "\n" + s.Text
	}

	return []slackapi.MsgOption{
		slackapi.MsgOptionBlocks(blocks...),
		slackapi.MsgOptionText(fallback, false),
	}
}

// forceHardLineBreaks rewrites text so every visible line ends with a
// Markdown hard-break marker (two trailing spaces). The markdown block
// renders standard CommonMark, where consecutive non-blank lines join
// into one paragraph; the generic content layer's intent is "each `\n` is
// a visible line break" (matching mrkdwn semantics). Appending the marker
// to non-empty lines makes each render on its own line while leaving
// blank lines intact, so `\n\n` paragraph breaks survive.
func forceHardLineBreaks(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = line + "  "
		}
	}
	return strings.Join(lines, "\n")
}
