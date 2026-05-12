// Package blobclient is the producer-side surface for object storage.
// Producers (dataset, user avatars, evaluation export, injection
// artifacts, …) depend ONLY on this package.
//
// Two interchangeable implementations are wired via fx:
//
//   - Local — calls module/blob.Service in-process. Used in the
//     monolith.
//   - Remote — POSTs to aegis-blob's `/api/v2/blob/*` endpoints over
//     HTTP, authenticated with a service token from SSO's
//     client_credentials grant.
//
// Switching modes is a config flip — no producer code changes. Mirrors
// the notificationclient pattern by design.
package blobclient

import (
	"context"
	"time"
)

// Client is the only type producers reference.
type Client interface {
	PresignPut(ctx context.Context, bucket string, req PresignPutReq) (*PresignPutResult, error)
	PresignGet(ctx context.Context, bucket, key string, req PresignGetReq) (*PresignedURL, error)
	Stat(ctx context.Context, bucket, key string) (*ObjectMeta, error)
	Delete(ctx context.Context, bucket, key string) error
	// Small-payload helpers — useful for service-side writes that don't
	// need the presign round trip.
	PutBytes(ctx context.Context, bucket string, body []byte, req PresignPutReq) (*ObjectMeta, error)
	GetBytes(ctx context.Context, bucket, key string) ([]byte, *ObjectMeta, error)
}

// PresignPutReq is the producer-facing payload, kept simple — JSON-ish
// fields, no internal types.
type PresignPutReq struct {
	Key           string            `json:"key,omitempty"`
	ContentType   string            `json:"content_type"`
	ContentLength int64             `json:"content_length,omitempty"`
	EntityKind    string            `json:"entity_kind,omitempty"`
	EntityID      string            `json:"entity_id,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	TTLSeconds    int               `json:"ttl_seconds,omitempty"`
}

type PresignGetReq struct {
	ResponseContentType string `json:"response_content_type,omitempty"`
	TTLSeconds          int    `json:"ttl_seconds,omitempty"`
}

type PresignPutResult struct {
	ObjectID  int64         `json:"object_id"`
	Bucket    string        `json:"bucket"`
	Key       string        `json:"key"`
	Presigned *PresignedURL `json:"presigned"`
}

type PresignedURL struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Expires time.Time         `json:"expires_at"`
}

type ObjectMeta struct {
	Key         string            `json:"key"`
	Size        int64             `json:"size_bytes"`
	ContentType string            `json:"content_type"`
	ETag        string            `json:"etag,omitempty"`
	UpdatedAt   time.Time         `json:"updated_at"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}
