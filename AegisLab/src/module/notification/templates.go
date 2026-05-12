package notification

import "context"

// Templater renders an Event into per-channel content. v1's default
// is a passthrough — producers supply Title/Body and the inbox shows
// them verbatim. When email / Slack land, a richer Templater can be
// dropped in (e.g. text/template or a per-category registry) without
// touching the orchestrator.
type Templater interface {
	Render(ctx context.Context, channel ChannelKey, evt *Event) (*RenderedContent, error)
}

type PassthroughTemplater struct{}

func NewPassthroughTemplater() *PassthroughTemplater { return &PassthroughTemplater{} }

func (PassthroughTemplater) Render(_ context.Context, _ ChannelKey, evt *Event) (*RenderedContent, error) {
	title := evt.Title
	if title == "" {
		// Fallback so the inbox never renders an empty header. Producers
		// that hit this are bugs to fix at the source.
		title = "<untitled event>"
	}
	return &RenderedContent{
		Title: title,
		Body:  evt.Body,
		Extra: evt.Payload,
	}, nil
}
