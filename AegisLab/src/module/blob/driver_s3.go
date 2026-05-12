package blob

import (
	"context"
	"io"
)

// S3Driver is the placeholder for the S3-compatible driver
// (RustFS/MinIO/AWS/Aliyun OSS). Phase A (this PR) keeps it as a stub
// that returns ErrDriverNotImplemented for every operation so the
// binary compiles with `driver = "s3"` buckets declared but cannot
// silently swallow producer calls. Phase B (see RFC) drops in the real
// implementation behind this same interface — no caller changes.
//
// TODO(blob/s3): implement using aws-sdk-go-v2 once Phase B starts.
type S3Driver struct {
	cfg BucketConfig
}

// NewS3Driver returns the stub driver; it doesn't error on
// construction so the boot path can compose a registry of mixed
// localfs + s3 buckets in dev today.
func NewS3Driver(cfg BucketConfig) (*S3Driver, error) {
	return &S3Driver{cfg: cfg}, nil
}

func (d *S3Driver) Name() string { return "s3" }

func (d *S3Driver) PresignPut(_ context.Context, _ string, _ PutOpts) (*PresignedRequest, error) {
	return nil, ErrDriverNotImplemented
}

func (d *S3Driver) PresignGet(_ context.Context, _ string, _ GetOpts) (*PresignedRequest, error) {
	return nil, ErrDriverNotImplemented
}

func (d *S3Driver) Put(_ context.Context, _ string, _ io.Reader, _ PutOpts) (*ObjectMeta, error) {
	return nil, ErrDriverNotImplemented
}

func (d *S3Driver) Get(_ context.Context, _ string) (io.ReadCloser, *ObjectMeta, error) {
	return nil, nil, ErrDriverNotImplemented
}

func (d *S3Driver) Stat(_ context.Context, _ string) (*ObjectMeta, error) {
	return nil, ErrDriverNotImplemented
}

func (d *S3Driver) Delete(_ context.Context, _ string) error {
	return ErrDriverNotImplemented
}

func (d *S3Driver) List(_ context.Context, _, _ string, _ int) (*ListResult, error) {
	return nil, ErrDriverNotImplemented
}
