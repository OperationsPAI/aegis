package blobclient

import (
	"bytes"
	"context"
	"io"
	"time"

	"aegis/module/blob"
)

// LocalClient is the in-process implementation. It maps the
// producer-facing requests onto module/blob.Service.
type LocalClient struct {
	svc *blob.Service
}

func NewLocalClient(svc *blob.Service) *LocalClient {
	return &LocalClient{svc: svc}
}

func (c *LocalClient) PresignPut(ctx context.Context, bucket string, req PresignPutReq) (*PresignPutResult, error) {
	res, err := c.svc.PresignPut(ctx, blob.PresignPutInput{
		Bucket: bucket, Key: req.Key,
		ContentType: req.ContentType, ContentLength: req.ContentLength,
		EntityKind: req.EntityKind, EntityID: req.EntityID,
		Metadata: req.Metadata,
		TTL:      time.Duration(req.TTLSeconds) * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return &PresignPutResult{
		ObjectID: res.ObjectID, Bucket: res.Bucket, Key: res.Key,
		Presigned: toClientPresigned(res.Presigned),
	}, nil
}

func (c *LocalClient) PresignGet(ctx context.Context, bucket, key string, req PresignGetReq) (*PresignedURL, error) {
	pr, err := c.svc.PresignGet(ctx, bucket, key, blob.GetOpts{
		ResponseContentType: req.ResponseContentType,
		TTL:                 time.Duration(req.TTLSeconds) * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return toClientPresigned(pr), nil
}

func (c *LocalClient) Stat(ctx context.Context, bucket, key string) (*ObjectMeta, error) {
	m, err := c.svc.Stat(ctx, bucket, key)
	if err != nil {
		return nil, err
	}
	return toClientMeta(m), nil
}

func (c *LocalClient) Delete(ctx context.Context, bucket, key string) error {
	return c.svc.Delete(ctx, bucket, key)
}

func (c *LocalClient) PutBytes(ctx context.Context, bucket string, body []byte, req PresignPutReq) (*ObjectMeta, error) {
	_, meta, err := c.svc.PutBytes(ctx, blob.PresignPutInput{
		Bucket: bucket, Key: req.Key,
		ContentType: req.ContentType, ContentLength: int64(len(body)),
		EntityKind: req.EntityKind, EntityID: req.EntityID, Metadata: req.Metadata,
	}, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	return toClientMeta(meta), nil
}

func (c *LocalClient) GetBytes(ctx context.Context, bucket, key string) ([]byte, *ObjectMeta, error) {
	rc, meta, err := c.svc.Get(ctx, bucket, key)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	if err != nil {
		return nil, nil, err
	}
	return body, toClientMeta(meta), nil
}

func toClientPresigned(pr *blob.PresignedRequest) *PresignedURL {
	if pr == nil {
		return nil
	}
	return &PresignedURL{
		Method: pr.Method, URL: pr.URL, Headers: pr.Headers, Expires: pr.Expires,
	}
}

func toClientMeta(m *blob.ObjectMeta) *ObjectMeta {
	if m == nil {
		return nil
	}
	return &ObjectMeta{
		Key: m.Key, Size: m.Size, ContentType: m.ContentType, ETag: m.ETag,
		UpdatedAt: m.UpdatedAt, Metadata: m.Metadata,
	}
}

var _ Client = (*LocalClient)(nil)
