package notificationclient

import (
	"context"
	"strconv"

	"aegis/module/notification"
)

// LocalClient is the in-process implementation. It maps the
// producer-facing PublishReq to notification.Event and calls the
// in-process Publisher.
type LocalClient struct {
	pub notification.Publisher
}

func NewLocalClient(pub notification.Publisher) *LocalClient {
	return &LocalClient{pub: pub}
}

func (c *LocalClient) Publish(ctx context.Context, req PublishReq) (*PublishResult, error) {
	evt := toEvent(req)
	res, err := c.pub.Publish(ctx, evt)
	if err != nil {
		return nil, err
	}
	out := &PublishResult{DroppedDedupe: res.DroppedDedupe}
	for _, id := range res.NotificationIDs {
		out.NotificationIDs = append(out.NotificationIDs, strconv.FormatInt(id, 10))
	}
	return out, nil
}

func toEvent(req PublishReq) *notification.Event {
	sev := notification.Severity(req.Severity)
	if sev == "" {
		sev = notification.SeverityInfo
	}
	evt := &notification.Event{
		Category:    req.Category,
		Severity:    sev,
		Title:       req.Title,
		Body:        req.Body,
		LinkTo:      req.LinkTo,
		ActorUserID: req.ActorUserID,
		EntityKind:  req.EntityKind,
		EntityID:    req.EntityID,
		Payload:     req.Payload,
		DedupeKey:   req.DedupeKey,
	}
	for _, u := range req.UserIDs {
		evt.Recipients = append(evt.Recipients, notification.Recipient{
			Kind: notification.RecipientUser, UserID: u,
		})
	}
	for _, role := range req.Roles {
		evt.Recipients = append(evt.Recipients, notification.Recipient{
			Kind: notification.RecipientRole, RoleName: role,
		})
	}
	return evt
}

var _ Client = (*LocalClient)(nil)
