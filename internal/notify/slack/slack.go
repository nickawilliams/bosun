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
}

// New returns a new Slack adapter.
func New(token string) *Adapter {
	return &Adapter{client: slackapi.New(token)}
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
	return &Adapter{client: slackapi.New(token, slackapi.OptionHTTPClient(client))}
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

	// Upsert: update existing message if one exists for this issue.
	if msg.IssueKey != "" {
		existing, _ := a.findThreadInChannel(ctx, channelID, msg.IssueKey)
		if existing.Timestamp != "" {
			opts := buildMsgOptions(msg.Content)
			_, _, _, err := a.client.UpdateMessageContext(ctx, channelID, existing.Timestamp, opts...)
			if err != nil {
				return notify.ThreadRef{}, fmt.Errorf("updating message: %w", err)
			}
			return existing, nil
		}
	}

	opts := buildMsgOptions(msg.Content)

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
// one containing the issue key. Returns a zero ThreadRef if not found.
func (a *Adapter) findThreadInChannel(ctx context.Context, channelID, issueKey string) (notify.ThreadRef, error) {
	params := &slackapi.GetConversationHistoryParameters{
		ChannelID: channelID,
		Limit:     200,
	}

	resp, err := a.client.GetConversationHistoryContext(ctx, params)
	if err != nil {
		return notify.ThreadRef{}, fmt.Errorf("fetching channel history: %w", err)
	}

	for _, msg := range resp.Messages {
		if strings.Contains(msg.Text, issueKey) {
			return notify.ThreadRef{Channel: channelID, Timestamp: msg.Timestamp}, nil
		}
		for _, block := range msg.Blocks.BlockSet {
			if section, ok := block.(*slackapi.SectionBlock); ok && section.Text != nil {
				if strings.Contains(section.Text.Text, issueKey) {
					return notify.ThreadRef{Channel: channelID, Timestamp: msg.Timestamp}, nil
				}
			}
			if header, ok := block.(*slackapi.HeaderBlock); ok && header.Text != nil {
				if strings.Contains(header.Text.Text, issueKey) {
					return notify.ThreadRef{Channel: channelID, Timestamp: msg.Timestamp}, nil
				}
			}
		}
	}

	return notify.ThreadRef{}, nil
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
func (a *Adapter) resolveChannelID(ctx context.Context, name string) (string, error) {
	name = strings.TrimPrefix(name, "#")

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
