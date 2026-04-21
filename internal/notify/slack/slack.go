package slack

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/nickawilliams/bosun/internal/notify"
	slackapi "github.com/slack-go/slack"
)

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

	meta := bosunMetadata(msg.IssueKey)
	opts := buildMsgOptions(msg.Content)
	opts = append(opts, slackapi.MsgOptionMetadata(meta))

	// Upsert: update existing message if one exists for this issue.
	// The findThreadInChannel call is cached, so repeated calls from
	// Assess → Notify don't hit the API twice.
	if msg.IssueKey != "" {
		existing, _ := a.findThreadInChannel(ctx, channelID, msg.IssueKey)
		if existing.Timestamp != "" {
			_, _, _, err := a.client.UpdateMessageContext(ctx, channelID, existing.Timestamp, opts...)
			if err != nil {
				return notify.ThreadRef{}, fmt.Errorf("updating message: %w", err)
			}
			return existing, nil
		}
	}

	_, ts, err := a.client.PostMessageContext(ctx, channelID, opts...)
	if err != nil {
		return notify.ThreadRef{}, fmt.Errorf("posting message: %w", err)
	}

	return notify.ThreadRef{Channel: channelID, Timestamp: ts}, nil
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
	if ref, ok := a.cache.threads[cacheKey]; ok {
		return ref, nil
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
				result = notify.ThreadRef{Channel: channelID, Timestamp: msg.Timestamp}
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

const metadataEventType = "bosun_notification"

// bosunMetadata builds the Slack message metadata for a bosun notification.
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

// resolveChannelID finds the channel ID for a given channel name.
// Results are cached for the lifetime of the adapter.
func (a *Adapter) resolveChannelID(ctx context.Context, name string) (string, error) {
	name = strings.TrimPrefix(name, "#")

	if id, ok := a.cache.channels[name]; ok {
		return id, nil
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
// but richer formatting). When only Text is set, it posts plain mrkdwn
// (client-editable).
func buildMsgOptions(c notify.Content) []slackapi.MsgOption {
	if !c.HasBlocks() {
		return []slackapi.MsgOption{
			slackapi.MsgOptionText(c.Text, false),
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

	if c.Context != "" {
		blocks = append(blocks, slackapi.NewContextBlock("",
			slackapi.NewTextBlockObject(slackapi.MarkdownType, c.Context, false, false),
		))
	}

	// Fallback text for notifications/accessibility.
	fallback := c.Body
	if fallback == "" {
		fallback = c.Header
	}

	return []slackapi.MsgOption{
		slackapi.MsgOptionBlocks(blocks...),
		slackapi.MsgOptionText(fallback, false),
	}
}
