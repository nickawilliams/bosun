package slack

import (
	"context"
	"fmt"
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

func (a *Adapter) Notify(ctx context.Context, msg notify.Message) (notify.ThreadRef, error) {
	channelID, err := a.resolveChannelID(ctx, msg.Channel)
	if err != nil {
		return notify.ThreadRef{}, err
	}

	blocks := buildBlocks(msg)
	fallback := buildFallbackText(msg)

	_, ts, err := a.client.PostMessageContext(ctx, channelID,
		slackapi.MsgOptionBlocks(blocks...),
		slackapi.MsgOptionText(fallback, false),
	)
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
		// Also check block text content.
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
	blocks := buildBlocks(msg)
	fallback := buildFallbackText(msg)

	_, _, err := a.client.PostMessageContext(ctx, ref.Channel,
		slackapi.MsgOptionTS(ref.Timestamp),
		slackapi.MsgOptionBlocks(blocks...),
		slackapi.MsgOptionText(fallback, false),
	)
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

// buildBlocks constructs Slack Block Kit blocks from a notification message.
func buildBlocks(msg notify.Message) []slackapi.Block {
	var blocks []slackapi.Block

	// Header: issue key + title.
	headerText := msg.IssueKey
	if msg.Title != "" {
		headerText += ": " + msg.Title
	}
	blocks = append(blocks, slackapi.NewHeaderBlock(
		slackapi.NewTextBlockObject(slackapi.PlainTextType, headerText, false, false),
	))

	// Summary line (for simple updates like preview).
	if msg.Summary != "" {
		blocks = append(blocks, slackapi.NewSectionBlock(
			slackapi.NewTextBlockObject(slackapi.MarkdownType, msg.Summary, false, false),
			nil, nil,
		))
	}

	// Per-repository items.
	for _, item := range msg.Items {
		var text string
		if item.URL != "" {
			text = fmt.Sprintf("*%s*  <%s|%s>", item.Label, item.URL, item.Detail)
		} else {
			text = fmt.Sprintf("*%s*  %s", item.Label, item.Detail)
		}
		blocks = append(blocks, slackapi.NewSectionBlock(
			slackapi.NewTextBlockObject(slackapi.MarkdownType, text, false, false),
			nil, nil,
		))
	}

	// Context: issue URL.
	if msg.IssueURL != "" {
		link := fmt.Sprintf("<%s|View in issue tracker>", msg.IssueURL)
		blocks = append(blocks, slackapi.NewContextBlock("",
			slackapi.NewTextBlockObject(slackapi.MarkdownType, link, false, false),
		))
	}

	return blocks
}

// buildFallbackText builds a plain-text fallback for clients that can't render blocks.
func buildFallbackText(msg notify.Message) string {
	var b strings.Builder
	b.WriteString(msg.IssueKey)
	if msg.Title != "" {
		b.WriteString(": ")
		b.WriteString(msg.Title)
	}
	if msg.Summary != "" {
		b.WriteString(" — ")
		b.WriteString(msg.Summary)
	}
	for _, item := range msg.Items {
		b.WriteString("\n• ")
		b.WriteString(item.Label)
		b.WriteString(": ")
		b.WriteString(item.Detail)
		if item.URL != "" {
			b.WriteString(" ")
			b.WriteString(item.URL)
		}
	}
	return b.String()
}
