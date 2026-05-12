package blob

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LocalFSDriver stores bytes on the local filesystem under Root. The
// presign methods do not return a URL the frontend can hit directly
// (there is no S3-style signature support in the OS) — instead they
// mint an HMAC-signed token URL that points back at the blob handler's
// /raw/:token endpoint, which decodes the token and streams from / to
// disk. Same UX as S3 presign, no driver-specific frontend code.
type LocalFSDriver struct {
	cfg        BucketConfig
	signingKey []byte
	// publicBaseURL is empty in v1; the handler builds absolute URLs
	// from c.Request.Host. Tokens themselves carry every claim the
	// handler needs to authorise the request.
}

// NewLocalFSDriver constructs the localfs driver and ensures the root
// directory exists.
func NewLocalFSDriver(cfg BucketConfig, signingKey []byte) (*LocalFSDriver, error) {
	if cfg.Root == "" {
		return nil, fmt.Errorf("localfs driver requires root")
	}
	if len(signingKey) == 0 {
		return nil, fmt.Errorf("localfs driver requires a non-empty signing key (blob.signing_key)")
	}
	if err := os.MkdirAll(cfg.Root, 0o755); err != nil {
		return nil, fmt.Errorf("create localfs root %q: %w", cfg.Root, err)
	}
	return &LocalFSDriver{cfg: cfg, signingKey: signingKey}, nil
}

func (d *LocalFSDriver) Name() string { return "localfs" }

// Token is the signed payload encoded into /raw/:token URLs. The
// handler decodes and verifies it before serving / accepting bytes.
type Token struct {
	Bucket string    `json:"b"`
	Key    string    `json:"k"`
	Op     Operation `json:"o"`
	Exp    int64     `json:"e"`
}

// EncodeToken returns "<base64url(payload)>.<base64url(hmac)>".
func EncodeToken(signingKey []byte, t Token) (string, error) {
	body, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, signingKey)
	mac.Write([]byte(enc))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return enc + "." + sig, nil
}

// DecodeToken verifies the HMAC, decodes the payload and rejects
// expired tokens.
func DecodeToken(signingKey []byte, raw string) (*Token, error) {
	parts := strings.SplitN(raw, ".", 2)
	if len(parts) != 2 {
		return nil, ErrTokenInvalid
	}
	mac := hmac.New(sha256.New, signingKey)
	mac.Write([]byte(parts[0]))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return nil, ErrTokenInvalid
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrTokenInvalid
	}
	var t Token
	if err := json.Unmarshal(body, &t); err != nil {
		return nil, ErrTokenInvalid
	}
	if time.Now().Unix() > t.Exp {
		return nil, ErrTokenInvalid
	}
	return &t, nil
}

func (d *LocalFSDriver) presign(op Operation, key string, ttl time.Duration) (*PresignedRequest, error) {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	exp := time.Now().Add(ttl)
	tok, err := EncodeToken(d.signingKey, Token{
		Bucket: d.cfg.Name, Key: key, Op: op, Exp: exp.Unix(),
	})
	if err != nil {
		return nil, err
	}
	method := "PUT"
	if op == OpGet {
		method = "GET"
	}
	return &PresignedRequest{
		Method:  method,
		URL:     "/api/v2/blob/raw/" + url.PathEscape(tok),
		Headers: map[string]string{},
		Expires: exp,
	}, nil
}

func (d *LocalFSDriver) PresignPut(_ context.Context, key string, opts PutOpts) (*PresignedRequest, error) {
	req, err := d.presign(OpPut, key, opts.TTL)
	if err != nil {
		return nil, err
	}
	if opts.ContentType != "" {
		req.Headers["Content-Type"] = opts.ContentType
	}
	return req, nil
}

func (d *LocalFSDriver) PresignGet(_ context.Context, key string, opts GetOpts) (*PresignedRequest, error) {
	return d.presign(OpGet, key, opts.TTL)
}

func (d *LocalFSDriver) resolve(key string) (string, error) {
	clean := filepath.Clean("/" + key)
	if strings.Contains(clean, "..") {
		return "", fmt.Errorf("invalid key %q", key)
	}
	return filepath.Join(d.cfg.Root, clean), nil
}

func (d *LocalFSDriver) Put(_ context.Context, key string, r io.Reader, opts PutOpts) (*ObjectMeta, error) {
	path, err := d.resolve(key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	n, err := io.Copy(f, r)
	if err != nil {
		return nil, err
	}
	ct := opts.ContentType
	if ct == "" {
		ct = mime.TypeByExtension(filepath.Ext(path))
	}
	return &ObjectMeta{
		Key:         key,
		Size:        n,
		ContentType: ct,
		UpdatedAt:   time.Now(),
	}, nil
}

func (d *LocalFSDriver) Get(_ context.Context, key string) (io.ReadCloser, *ObjectMeta, error) {
	path, err := d.resolve(key)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, ErrObjectNotFound
		}
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, &ObjectMeta{
		Key:         key,
		Size:        info.Size(),
		ContentType: mime.TypeByExtension(filepath.Ext(path)),
		UpdatedAt:   info.ModTime(),
	}, nil
}

func (d *LocalFSDriver) Stat(_ context.Context, key string) (*ObjectMeta, error) {
	path, err := d.resolve(key)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrObjectNotFound
		}
		return nil, err
	}
	return &ObjectMeta{
		Key:         key,
		Size:        info.Size(),
		ContentType: mime.TypeByExtension(filepath.Ext(path)),
		UpdatedAt:   info.ModTime(),
	}, nil
}

func (d *LocalFSDriver) Delete(_ context.Context, key string) error {
	path, err := d.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return ErrObjectNotFound
		}
		return err
	}
	return nil
}

func (d *LocalFSDriver) List(_ context.Context, prefix, cursor string, limit int) (*ListResult, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	root := d.cfg.Root
	if prefix != "" {
		clean := filepath.Clean("/" + prefix)
		root = filepath.Join(d.cfg.Root, clean)
	}
	var items []ObjectMeta
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(d.cfg.Root, path)
		if relErr != nil {
			return relErr
		}
		key := filepath.ToSlash(rel)
		if cursor != "" && key <= cursor {
			return nil
		}
		items = append(items, ObjectMeta{
			Key: key, Size: info.Size(), UpdatedAt: info.ModTime(),
			ContentType: mime.TypeByExtension(filepath.Ext(path)),
		})
		if len(items) >= limit {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	res := &ListResult{Items: items}
	if len(items) == limit {
		res.NextCursor = items[len(items)-1].Key
	}
	return res, nil
}
