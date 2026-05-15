package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Driver talks to any S3-compatible backend (RustFS, MinIO, AWS,
// Aliyun OSS) via minio-go. Presign returns native S3 V4 URLs the
// frontend hits directly, no /raw/:token round-trip.
type S3Driver struct {
	cfg    BucketConfig
	client *minio.Client
	// presignClient signs URLs handed to browsers. It is identical to
	// `client` unless BucketConfig.PublicEndpoint is set, in which case
	// it is constructed against the externally-reachable host so SigV4
	// validates against the browser-visible Host header.
	presignClient *minio.Client
	bucket        string
}

// NewS3Driver constructs the driver, verifies credentials, and ensures
// the remote bucket exists (idempotent MakeBucket).
func NewS3Driver(cfg BucketConfig) (*S3Driver, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("s3 driver requires endpoint")
	}
	accessKey, secretKey, err := resolveS3Credentials(cfg)
	if err != nil {
		return nil, err
	}
	bucket := cfg.Bucket
	if bucket == "" {
		bucket = cfg.Name
	}

	endpoint, useSSL := normalizeS3Endpoint(cfg.Endpoint, cfg.UseSSL)
	client, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure:       useSSL,
		Region:       cfg.Region,
		BucketLookup: bucketLookup(cfg.PathStyle),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 driver init %s: %w", endpoint, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("s3 driver bucket-exists %q: %w", bucket, err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
			exists, existsErr := client.BucketExists(ctx, bucket)
			if existsErr != nil || !exists {
				return nil, fmt.Errorf("s3 driver make-bucket %q: %w", bucket, err)
			}
		}
	}

	presignClient := client
	if cfg.PublicEndpoint != "" {
		pubEndpoint, pubSSL := normalizeS3Endpoint(cfg.PublicEndpoint, cfg.UseSSL)
		presignClient, err = minio.New(pubEndpoint, &minio.Options{
			Creds:        credentials.NewStaticV4(accessKey, secretKey, ""),
			Secure:       pubSSL,
			Region:       cfg.Region,
			BucketLookup: bucketLookup(cfg.PathStyle),
		})
		if err != nil {
			return nil, fmt.Errorf("s3 driver presign-init %s: %w", pubEndpoint, err)
		}
	}
	return &S3Driver{cfg: cfg, client: client, presignClient: presignClient, bucket: bucket}, nil
}

func resolveS3Credentials(cfg BucketConfig) (string, string, error) {
	accessKey := cfg.AccessKey
	secretKey := cfg.SecretKey
	if cfg.AccessKeyEnv != "" {
		accessKey = os.Getenv(cfg.AccessKeyEnv)
	}
	if cfg.SecretKeyEnv != "" {
		secretKey = os.Getenv(cfg.SecretKeyEnv)
	}
	if accessKey == "" || secretKey == "" {
		return "", "", fmt.Errorf("s3 driver requires access_key + secret_key (or *_env)")
	}
	return accessKey, secretKey, nil
}

func normalizeS3Endpoint(raw string, useSSL bool) (string, bool) {
	switch {
	case strings.HasPrefix(raw, "https://"):
		return strings.TrimPrefix(raw, "https://"), true
	case strings.HasPrefix(raw, "http://"):
		return strings.TrimPrefix(raw, "http://"), false
	default:
		return raw, useSSL
	}
}

func bucketLookup(pathStyle bool) minio.BucketLookupType {
	if pathStyle {
		return minio.BucketLookupPath
	}
	return minio.BucketLookupDNS
}

func (d *S3Driver) Name() string { return "s3" }

func presignTTL(d time.Duration) time.Duration {
	if d <= 0 {
		return 15 * time.Minute
	}
	if d > 7*24*time.Hour {
		return 7 * 24 * time.Hour
	}
	return d
}

func (d *S3Driver) PresignPut(ctx context.Context, key string, opts PutOpts) (*PresignedRequest, error) {
	ttl := presignTTL(opts.TTL)
	u, err := d.presignClient.PresignedPutObject(ctx, d.bucket, key, ttl)
	if err != nil {
		return nil, fmt.Errorf("s3 presign put %q: %w", key, err)
	}
	headers := map[string]string{}
	if opts.ContentType != "" {
		headers["Content-Type"] = opts.ContentType
	}
	return &PresignedRequest{
		Method:  "PUT",
		URL:     u.String(),
		Headers: headers,
		Expires: time.Now().Add(ttl),
	}, nil
}

func (d *S3Driver) PresignGet(ctx context.Context, key string, opts GetOpts) (*PresignedRequest, error) {
	ttl := presignTTL(opts.TTL)
	params := url.Values{}
	if opts.ResponseContentType != "" {
		params.Set("response-content-type", opts.ResponseContentType)
	}
	if opts.ResponseContentDisposition != "" {
		params.Set("response-content-disposition", opts.ResponseContentDisposition)
	}
	u, err := d.presignClient.PresignedGetObject(ctx, d.bucket, key, ttl, params)
	if err != nil {
		return nil, fmt.Errorf("s3 presign get %q: %w", key, err)
	}
	return &PresignedRequest{
		Method:  "GET",
		URL:     u.String(),
		Headers: map[string]string{},
		Expires: time.Now().Add(ttl),
	}, nil
}

func (d *S3Driver) Put(ctx context.Context, key string, r io.Reader, opts PutOpts) (*ObjectMeta, error) {
	ct := opts.ContentType
	if ct == "" {
		ct = mime.TypeByExtension(filepath.Ext(key))
	}
	size := opts.ContentLength
	if size <= 0 {
		size = -1
	}
	info, err := d.client.PutObject(ctx, d.bucket, key, r, size, minio.PutObjectOptions{
		ContentType:  ct,
		CacheControl: opts.CacheControl,
		UserMetadata: opts.Metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("s3 put %q: %w", key, err)
	}
	return &ObjectMeta{
		Key:         key,
		Size:        info.Size,
		ContentType: ct,
		ETag:        info.ETag,
		UpdatedAt:   time.Now(),
		Metadata:    opts.Metadata,
	}, nil
}

func (d *S3Driver) Get(ctx context.Context, key string) (io.ReadCloser, *ObjectMeta, error) {
	obj, err := d.client.GetObject(ctx, d.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("s3 get %q: %w", key, err)
	}
	info, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, nil, mapS3Err("s3 get-stat", key, err)
	}
	return obj, statToMeta(key, info), nil
}

func (d *S3Driver) Stat(ctx context.Context, key string) (*ObjectMeta, error) {
	info, err := d.client.StatObject(ctx, d.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return nil, mapS3Err("s3 stat", key, err)
	}
	return statToMeta(key, info), nil
}

func (d *S3Driver) Delete(ctx context.Context, key string) error {
	err := d.client.RemoveObject(ctx, d.bucket, key, minio.RemoveObjectOptions{})
	if err == nil {
		return nil
	}
	if mapped := mapS3Err("s3 delete", key, err); errors.Is(mapped, ErrObjectNotFound) {
		return nil
	}
	return fmt.Errorf("s3 delete %q: %w", key, err)
}

// Copy duplicates srcKey to dstKey using S3 server-side CopyObject.
func (d *S3Driver) Copy(ctx context.Context, srcKey, dstKey string) (*ObjectMeta, error) {
	src := minio.CopySrcOptions{Bucket: d.bucket, Object: srcKey}
	dst := minio.CopyDestOptions{Bucket: d.bucket, Object: dstKey}
	info, err := d.client.CopyObject(ctx, dst, src)
	if err != nil {
		return nil, mapS3Err("s3 copy", srcKey, err)
	}
	return &ObjectMeta{
		Key:       dstKey,
		Size:      info.Size,
		ETag:      info.ETag,
		UpdatedAt: info.LastModified,
	}, nil
}

// List paginates via minio's StartAfter. We treat the continuation
// token as the last key returned on the previous page. Delimiter rolls
// up subtrees into CommonPrefixes (S3 native semantics).
func (d *S3Driver) List(ctx context.Context, opts ListObjectsOpts) (*ListResult, error) {
	limit := opts.MaxKeys
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	ch := d.client.ListObjects(ctx, d.bucket, minio.ListObjectsOptions{
		Prefix:     opts.Prefix,
		StartAfter: opts.ContinuationToken,
		Recursive:  opts.Delimiter == "",
	})
	res := &ListResult{}
	var lastKey string
	for obj := range ch {
		if obj.Err != nil {
			return nil, fmt.Errorf("s3 list: %w", obj.Err)
		}
		// minio surfaces CommonPrefixes as zero-size entries whose Key
		// ends with the delimiter. Split them out for the caller.
		if opts.Delimiter != "" && strings.HasSuffix(obj.Key, opts.Delimiter) && obj.Size == 0 {
			res.CommonPrefixes = append(res.CommonPrefixes, obj.Key)
		} else {
			res.Items = append(res.Items, ObjectMeta{
				Key:         obj.Key,
				Size:        obj.Size,
				ContentType: obj.ContentType,
				ETag:        obj.ETag,
				UpdatedAt:   obj.LastModified,
			})
		}
		lastKey = obj.Key
		if len(res.Items)+len(res.CommonPrefixes) >= limit {
			res.IsTruncated = true
			res.NextContinuationToken = lastKey
			break
		}
	}
	return res, nil
}

func statToMeta(key string, info minio.ObjectInfo) *ObjectMeta {
	meta := map[string]string{}
	for k, v := range info.UserMetadata {
		meta[k] = v
	}
	return &ObjectMeta{
		Key:         key,
		Size:        info.Size,
		ContentType: info.ContentType,
		ETag:        info.ETag,
		UpdatedAt:   info.LastModified,
		Metadata:    meta,
	}
}

func mapS3Err(op, key string, err error) error {
	if err == nil {
		return nil
	}
	resp := minio.ToErrorResponse(err)
	switch resp.Code {
	case "NoSuchKey", "NoSuchObject", "NotFound":
		return ErrObjectNotFound
	case "NoSuchBucket":
		return ErrBucketNotFound
	}
	return fmt.Errorf("%s %q: %w", op, key, err)
}
