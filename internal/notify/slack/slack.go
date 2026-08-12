package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/nickawilliams/bosun/internal/notify"
	slackapi "github.com/slack-go/slack"
)

// The Slack adapter owns all presentation: it consumes a provider-agnostic
// notify.Content and upgrades it to Block Kit cards. Card labels, glyphs,
// and icon normalization live here — the content layer never knows about
// cards.
const (
	btnViewIssue       = "View Issue"
	btnViewPullRequest = "View Pull Request"
	btnViewBranch      = "View Branch"
	btnViewDeployment  = "View Deployment"

	glyphJira  = ":jira:"
	glyphCloud = ":cloud:"

	// noDescription is the placeholder for an empty card body. Italics
	// render as a softer muted style in Slack mrkdwn, signalling "this
	// slot is intentionally empty" rather than "the field is missing."
	noDescription = "_no description_"

	// cardIconJiraSize is the Jira universal_avatar named size closest to
	// the shared card-icon footprint — Jira's avatar endpoint takes named
	// sizes (xsmall/small/medium/large/xlarge), not pixel counts.
	cardIconJiraSize = "large"
)

// section is the adapter's internal card model. renderContent produces
// these from a notify.Content; cardBlock emits each as a raw "card" block.
type section struct {
	Text     string       // Card title (mrkdwn).
	Subtitle string       // Card subtitle (mrkdwn).
	Body     string       // Card body (truncated to 200 chars by cardBlock).
	IconURL  string       // Small icon image URL.
	Buttons  []cardButton // Action buttons.
}

// cardButton is a link button rendered in a card's actions area.
type cardButton struct {
	Text  string // Button label.
	URL   string // Link URL.
	Style string // "primary" (green), "danger" (red), or "" (default).
}

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
func cardBlock(s section, idPrefix string) rawBlock {
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

// renderContent turns a provider-agnostic notify.Content into the card
// sections this adapter posts: a Jira ticket card, an ephemeral preview
// deployment card, and one card per repository item (PR/release). This is
// the presentation half of the notification split — the content layer
// hands us structured data, and the button labels, glyphs, and layout are
// decided here.
func renderContent(c notify.Content) []section {
	var sections []section

	var issueKey, issueTitle string
	if c.Issue != nil {
		issueKey, issueTitle = c.Issue.Key, c.Issue.Title
	}

	// Card title shared by the Jira ticket and its PR cards: "[KEY] Title",
	// or just "KEY" when the title is unknown.
	subjectTitle := issueKey
	if issueTitle != "" {
		subjectTitle = fmt.Sprintf("[%s] %s", issueKey, issueTitle)
	}

	// Jira ticket card.
	if c.Issue != nil {
		issueType := "Issue"
		if c.Issue.Type != "" {
			issueType = c.Issue.Type
		}
		var buttons []cardButton
		if c.Issue.URL != "" {
			buttons = append(buttons, cardButton{
				Text:  btnViewIssue,
				URL:   c.Issue.URL,
				Style: "primary",
			})
		}
		// Prefer the real issue-type icon (e.g. the Story/Bug avatar) as the
		// card image, mirroring the GitHub avatar on PR cards. When there's
		// no usable icon, fall back to the :jira: glyph prefixed on the title
		// so the card still reads as a Jira ticket. Key the fallback off the
		// normalized icon, not the raw URL.
		icon := slackIconURL(c.Issue.IconURL)
		text := subjectTitle
		if icon == "" {
			text = glyphJira + " " + subjectTitle
		}
		sections = append(sections, section{
			Text:     text,
			Subtitle: issueType,
			Body:     descriptionOrPlaceholder(c.Issue.Description),
			IconURL:  icon,
			Buttons:  buttons,
		})
	}

	// Ephemeral deployment card.
	if c.Preview != nil {
		name := c.Preview.Name
		if name == "" {
			name = "Preview"
		}
		var buttons []cardButton
		if c.Preview.URL != "" {
			buttons = append(buttons, cardButton{
				Text:  btnViewDeployment,
				URL:   c.Preview.URL,
				Style: "primary",
			})
		}
		sections = append(sections, section{
			Text:     glyphCloud + " " + name,
			Subtitle: "Ephemeral preview",
			Buttons:  buttons,
		})
	}

	// Per-repo PR card sections.
	for _, item := range c.Items {
		subtitle := fmt.Sprintf("`%s` %s", item.Label, item.Detail)
		var buttons []cardButton
		if item.URL != "" {
			buttons = append(buttons, cardButton{
				Text:  btnViewPullRequest,
				URL:   item.URL,
				Style: "primary",
			})
			if item.BranchURL != "" {
				buttons = append(buttons, cardButton{
					Text: btnViewBranch,
					URL:  item.BranchURL,
				})
			}
		}
		sections = append(sections, section{
			Text:     subjectTitle,
			Subtitle: subtitle,
			Body:     descriptionOrPlaceholder(item.Body),
			IconURL:  c.IconURL,
			Buttons:  buttons,
		})
	}

	return sections
}

// descriptionOrPlaceholder returns body when non-empty, otherwise the
// italicized "no description" placeholder.
func descriptionOrPlaceholder(body string) string {
	if strings.TrimSpace(body) == "" {
		return noDescription
	}
	return body
}

// slackIconURL normalizes a Jira issue-type icon URL into something Slack's
// image proxy can actually render, returning "" only when there is nothing
// usable (in which case the card falls back to the :jira: glyph).
//
// Slack fetches image_url server-side through its proxy and does NOT render
// SVG (browsers do — which is why an SVG icon opens fine in a browser but
// shows as a broken image on a card). Jira hands us icons in two shapes:
//
//   - universal_avatar URLs (most issue types) default to SVG but accept a
//     `format=png` parameter; we add it plus a `size` so Slack gets a
//     card-sized raster.
//   - legacy /images/icons/issuetypes/<name>.svg system icons (e.g. the
//     built-in Epic, which has no avatar record). Jira serves a PNG sibling
//     at the same path; we swap the extension. These are fixed ~16px —
//     there is no larger variant Jira will serve.
func slackIconURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	switch {
	case strings.Contains(u.Path, "/universal_avatar/"):
		// SVG by default; force a card-sized PNG.
		q := u.Query()
		q.Set("size", cardIconJiraSize)
		q.Set("format", "png")
		u.RawQuery = q.Encode()
	case strings.HasSuffix(strings.ToLower(u.Path), ".svg"):
		// Legacy system icon — swap to the PNG sibling Slack can render.
		u.Path = u.Path[:len(u.Path)-len(".svg")] + ".png"
	}
	return u.String()
}

// truncate shortens s to at most max bytes, marking the cut with "…".
//
// Two things the obvious implementation gets wrong. The ellipsis is three
// bytes in UTF-8, so it has to come out of the budget rather than be added
// on top; and the cut has to land on a rune boundary, or a multi-byte
// character is sliced in half and the result is invalid UTF-8. Bodies
// reaching here are issue and PR descriptions, which routinely carry
// em-dashes, smart quotes, and accented names.
//
// When max cannot even hold the marker, s is hard-cut instead — still on
// a rune boundary.
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	const ellipsis = "…"
	if cut := max - len(ellipsis); cut >= 0 {
		return s[:runeBoundary(s, cut)] + ellipsis
	}
	return s[:runeBoundary(s, max)]
}

// runeBoundary returns the largest offset <= n at which a rune starts, so
// that s[:runeBoundary(s, n)] does not end mid-rune. It preserves validity
// rather than conferring it: an s that is already invalid UTF-8 stays that
// way. Callers must pass an n within s.
func runeBoundary(s string, n int) int {
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return n
}

// Adapter implements notify.Notifier using the Slack API.
type Adapter struct {
	client *slackapi.Client
	cache  apiCache
	self   selfIdentity
}

// selfIdentity caches the authenticated identity (resolved once via
// auth.test). Used to recognize bosun's own messages when matching by
// author — the upsert fallback for xoxc/user tokens, which can't persist
// readable message metadata.
type selfIdentity struct {
	resolved bool
	userID   string
	botID    string
}

// apiCache stores results from Slack API calls to avoid redundant requests
// within a single command invocation. No TTL — the adapter is short-lived.
type apiCache struct {
	channels map[string]string           // channel name → ID
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
	meta := bosunMetadata(msg.IssueKey, hash)
	opts := buildMsgOptions(msg.Content)
	opts = append(opts, slackapi.MsgOptionMetadata(meta))

	// Upsert: update an existing message only when bosun owns it —
	// matched on bosun metadata, or (for xoxc/user tokens that can't
	// persist readable metadata) on a message bosun itself authored that
	// mentions the issue key. See findOwnThread. The author scope is what
	// keeps this safe: we never update/delete a message we don't own, so
	// a stale match on someone else's message can't destroy it. A failed
	// history fetch is an error, not "no existing message" — posting
	// blind would duplicate the announcement the fetch failed to see.
	//
	// Two update strategies depending on what the API accepts:
	//   - Block content (review/preview): chat.update works cleanly.
	//   - markdown_text content (prerelease): chat.update rejects
	//     markdown_text with internal_error in practice (despite the
	//     docs listing it as supported). When chat.update fails we
	//     fall back to chat.postMessage + chat.delete — post first, so
	//     a failed post leaves the old announcement standing instead
	//     of destroying it with nothing in its place. Subscribers see
	//     a re-notification — that's desirable for "the announcement
	//     was updated, look again". If the trailing delete fails, the
	//     superseded message is orphaned; better than a hard failure
	//     on the announcement path.
	var replaceTS string // superseded message; deleted after a successful post
	if msg.IssueKey != "" {
		cacheKey := channelID + ":own:" + msg.IssueKey
		existing, ferr := a.findOwnThread(ctx, channelID, msg.IssueKey)
		if ferr != nil {
			return notify.ThreadRef{}, fmt.Errorf("locating existing notification: %w", ferr)
		}
		if existing.Timestamp != "" {
			// Skip update if content hasn't changed.
			if existing.ContentHash == hash {
				return existing, nil
			}
			_, _, _, err := a.client.UpdateMessageContext(ctx, channelID, existing.Timestamp, opts...)
			if err == nil {
				// Update the cached hash so subsequent runs detect no change.
				existing.ContentHash = hash
				a.rememberThread(channelID, msg.IssueKey, existing)
				return existing, nil
			}
			if !strings.Contains(err.Error(), "message_not_found") {
				replaceTS = existing.Timestamp
			}
			delete(a.cache.threads, cacheKey)
			delete(a.cache.threads, channelID+":"+msg.IssueKey)
		}
	}

	_, ts, err := a.client.PostMessageContext(ctx, channelID, opts...)
	if err != nil {
		return notify.ThreadRef{}, fmt.Errorf("posting message: %w", err)
	}
	if replaceTS != "" {
		// Best-effort removal of the superseded message — the fresh
		// post above already carries the announcement.
		_, _, _ = a.client.DeleteMessageContext(ctx, channelID, replaceTS)
	}

	// Cache the new message under both lookup keys so a subsequent
	// upsert or FindThread in this session finds the message we just
	// posted instead of a stale "not found".
	ref := notify.ThreadRef{Channel: channelID, Timestamp: ts, ContentHash: hash}
	if msg.IssueKey != "" {
		a.rememberThread(channelID, msg.IssueKey, ref)
	}

	return ref, nil
}

// rememberThread caches ref under both keys an issue's notification is
// looked up by: the upsert key (":own:", read by findOwnThread) and the
// plain key (read by FindThread). Writing both keeps an Assess-side
// FindThread consistent with what Apply just posted or updated — within
// this session, and across runs for the xoxc adapter's persistent cache,
// where a run-1 Assess caches "not found", run 1 then posts, and a
// run-2 Assess would otherwise read the stale negative back from disk.
func (a *Adapter) rememberThread(channelID, issueKey string, ref notify.ThreadRef) {
	if a.cache.threads == nil {
		a.cache.threads = make(map[string]notify.ThreadRef)
	}
	a.cache.threads[channelID+":own:"+issueKey] = ref
	a.cache.threads[channelID+":"+issueKey] = ref
}

func (a *Adapter) FindThread(ctx context.Context, channel, issueKey string) (notify.ThreadRef, error) {
	channelID, err := a.resolveChannelID(ctx, channel)
	if err != nil {
		return notify.ThreadRef{}, err
	}

	return a.findThreadInChannel(ctx, channelID, issueKey)
}

// findOwnThread locates a bosun-owned notification for the upsert path.
// It matches on bosun metadata first (exact, app tokens). When that
// finds nothing — the xoxc/user-token case, where Slack does not persist
// readable message metadata — it falls back to a message authored by the
// authenticated identity that mentions the issue key.
//
// BOTH passes are author-scoped. Metadata proves "a bosun posted this
// for this issue", not "this principal's bosun posted it" — teammate
// B's bosun writes the same event type and issue key, and an unscoped
// metadata match would hand B's message to A's update/delete (which a
// workspace-admin user token can actually destroy). When the identity
// can't be resolved at all, ownership can't be verified, so nothing
// matches — the caller posts fresh rather than touching a message that
// might be someone else's.
func (a *Adapter) findOwnThread(ctx context.Context, channelID, issueKey string) (notify.ThreadRef, error) {
	cacheKey := channelID + ":own:" + issueKey
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
	selfUser, selfBot := a.resolveSelf(ctx)
	selfKnown := selfUser != "" || selfBot != ""

	// Metadata pass — exact key + hash (app tokens), gated on
	// authorship like everything else here.
	if selfKnown {
		for _, msg := range resp.Messages {
			if !messageAuthoredBy(msg, selfUser, selfBot) {
				continue
			}
			if msg.Metadata.EventType != metadataEventType {
				continue
			}
			key, _ := msg.Metadata.EventPayload["issue_key"].(string)
			if key != issueKey {
				continue
			}
			hash, _ := msg.Metadata.EventPayload["content_hash"].(string)
			result = notify.ThreadRef{Channel: channelID, Timestamp: msg.Timestamp, ContentHash: hash}
			break
		}
	}

	// Author-scoped fallback — xoxc/user tokens can't persist readable
	// metadata, so recognize our own prior message by author + mention.
	if result.Timestamp == "" && selfKnown {
		for _, msg := range resp.Messages {
			if messageAuthoredBy(msg, selfUser, selfBot) && messageMentions(msg, issueKey) {
				result = notify.ThreadRef{Channel: channelID, Timestamp: msg.Timestamp}
				break
			}
		}
	}

	if a.cache.threads == nil {
		a.cache.threads = make(map[string]notify.ThreadRef)
	}
	a.cache.threads[cacheKey] = result
	return result, nil
}

// resolveSelf returns the authenticated identity's user and bot IDs,
// resolved once via auth.test and cached for the adapter's lifetime.
// Returns empty strings when auth.test fails (the author-scoped fallback
// then no-ops, leaving metadata matching as the only path).
func (a *Adapter) resolveSelf(ctx context.Context) (userID, botID string) {
	if a.self.resolved {
		return a.self.userID, a.self.botID
	}
	a.self.resolved = true
	if resp, err := a.client.AuthTestContext(ctx); err == nil {
		a.self.userID = resp.UserID
		a.self.botID = resp.BotID
	}
	return a.self.userID, a.self.botID
}

// messageAuthoredBy reports whether msg was posted by the given identity.
func messageAuthoredBy(msg slackapi.Message, userID, botID string) bool {
	return (userID != "" && msg.User == userID) || (botID != "" && msg.BotID == botID)
}

// messageMentions reports whether issueKey appears in the message's
// fallback text or its section/header blocks.
func messageMentions(msg slackapi.Message, issueKey string) bool {
	if strings.Contains(msg.Text, issueKey) {
		return true
	}
	for _, block := range msg.Blocks.BlockSet {
		switch b := block.(type) {
		case *slackapi.SectionBlock:
			if b.Text != nil && strings.Contains(b.Text.Text, issueKey) {
				return true
			}
		case *slackapi.HeaderBlock:
			if b.Text != nil && strings.Contains(b.Text.Text, issueKey) {
				return true
			}
		}
	}
	return false
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
			if messageMentions(msg, issueKey) {
				result = notify.ThreadRef{Channel: channelID, Timestamp: msg.Timestamp}
				break
			}
		}
	}

	// Cache the result (including zero refs — "not found" is also cached,
	// but only for this session: saveCache skips negatives, so a later
	// run re-scans instead of trusting a stale "not found").
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

	var selfUser, selfBot string
	if excludeIssueKey != "" {
		selfUser, selfBot = a.resolveSelf(ctx)
	}
	isOwn := func(msg slackapi.Message) bool {
		if excludeIssueKey == "" {
			return false
		}
		if msg.Metadata.EventType == metadataEventType {
			key, _ := msg.Metadata.EventPayload["issue_key"].(string)
			if key == excludeIssueKey {
				return true
			}
		}
		// xoxc/user tokens can't read message metadata back, so an
		// announcement this identity posted would otherwise read as a
		// foreign match and the caller would skip the upsert, leaving
		// the stale announcement in place. Mirror findOwnThread's
		// author + mention recognition.
		return messageAuthoredBy(msg, selfUser, selfBot) && messageMentions(msg, excludeIssueKey)
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

// bosunMetadata builds the Slack message metadata for a bosun
// notification. The content_hash lets the upsert skip an unchanged
// message without re-rendering. Note: metadata is only readable with app
// tokens, not xoxc- user tokens — for those, findOwnThread falls back to
// matching bosun's own authored message by issue-key mention.
func bosunMetadata(issueKey, contentHash string) slackapi.SlackMetadata {
	return slackapi.SlackMetadata{
		EventType: metadataEventType,
		EventPayload: map[string]any{
			"issue_key":    issueKey,
			"content_hash": contentHash,
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
	if !c.Structured() {
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

	// Structured data (issue, preview, items) becomes card blocks.
	sections := renderContent(c)

	if len(sections) > 0 && (c.Header != "" || c.Body != "") {
		blocks = append(blocks, slackapi.NewDividerBlock())
	}

	for i, s := range sections {
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
	for _, s := range sections {
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
