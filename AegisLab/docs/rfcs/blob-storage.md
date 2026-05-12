# RFC: Blob storage (`module/blob` + `cmd/aegis-blob`)

- Status: **Draft**
- Owners: aegis-backend
- Stakeholders: dataset, user (avatars), evaluation (report export),
  injection (artifacts), notification (rich payload assets), frontend
  (direct upload via presigned URL)
- Related: `module/notification` RFC, `module/sso` (issues service
  tokens), `infra/etcd` (configuration source)

---

## Summary

Add a first-class blob storage service to the platform. Ship it as
`module/blob` (in-process producer surface), `module/blobclient`
(local + remote dual-mode SDK matching the notification pattern), and
`cmd/aegis-blob` (standalone microservice, default `:8085`). Default
driver is `localfs`; production drivers (RustFS-compatible S3, MinIO,
AWS S3, Aliyun OSS) plug in behind one `Driver` interface.

Frontend uploads go **straight to the storage backend** via short-lived
presigned URLs — blob service issues the signature, never proxies the
bytes.

## Motivation

We currently have no abstraction for object storage. Today this is
ad-hoc per module:

| Need | Current state |
| --- | --- |
| Dataset artifacts | written to PVC inside the workload pod, opaque to other services |
| User avatars | not implemented; `users.avatar_url` would be free-text today |
| Evaluation report export | not implemented; would dump to pod local FS |
| Injection bundle attachments | not implemented |
| Notification rich payload | RFC explicitly punts |

Every one of these would otherwise re-invent: bucket selection,
authn for upload, retention, ACL, presigning, error handling. Pulling
it into one module + one binary makes them additive.

This RFC mirrors the shape of `module/notification` so the team
already knows the pattern: thin core, plugin-driven extension points,
in-process + remote dual-mode SDK, standalone binary deferrable until
load demands it.

## Non-goals

- CDN front-cache. Out of scope; downstream consumers add CDN per
  bucket if they need it (avatar bucket would; dataset wouldn't).
- Object-level full-text search / metadata indexing. We store enough
  metadata for "find my objects by entity_kind/entity_id"; anything
  richer goes through a dedicated search service.
- Versioning / WORM. Drivers may support it; the platform doesn't
  expose it in v1.
- Multipart-upload-resume across server restarts. v1 lets drivers
  handle it natively (S3 multipart) but the service doesn't persist
  upload state.

## Proposed design

### Six roles (mirrors notification skeleton)

```
Ingestion → Authorization → Routing (bucket) → Driver → Lifecycle → Observability
```

1. **Ingestion** — `PresignPut` / `Put` (small inline) / `Stat` /
   `Get` (small inline) / `PresignGet` / `Delete` over HTTP and via
   the in-process `Client` SDK.
2. **Authorization** — JWT verified by the same `ssoclient` everyone
   uses. Per-bucket policy: who can write, who can read, max object
   size, allowed content-types.
3. **Routing** — bucket name → driver instance. Buckets are config-
   driven (TOML in v1, optionally promoted to DB later).
4. **Driver** — pluggable interface; concretes: `localfs`, `s3`. RustFS
   reuses `s3` driver with custom endpoint.
5. **Lifecycle** — TTL / retention / archive. Hourly job lists
   metadata rows, deletes expired objects via driver.
6. **Observability** — `objects_uploaded`, `bytes_in/out`, p95 presign
   latency, driver error rate (per bucket).

### Driver interface

```go
package blob

type Driver interface {
    Name() string
    PresignPut(ctx context.Context, key string, opts PutOpts) (PresignedRequest, error)
    PresignGet(ctx context.Context, key string, opts GetOpts) (PresignedRequest, error)
    Put(ctx context.Context, key string, r io.Reader, opts PutOpts) (*ObjectMeta, error)
    Get(ctx context.Context, key string) (io.ReadCloser, *ObjectMeta, error)
    Stat(ctx context.Context, key string) (*ObjectMeta, error)
    Delete(ctx context.Context, key string) error
    List(ctx context.Context, prefix string, cursor string, limit int) (ListResult, error)
}

type PresignedRequest struct {
    Method  string            // PUT or GET
    URL     string            // signed URL — frontend hits this directly
    Headers map[string]string // headers client must send (Content-Type, x-amz-*)
    Expires time.Time
}

type PutOpts struct {
    ContentType   string
    ContentLength int64
    Metadata      map[string]string // arbitrary x-amz-meta-* equivalents
    CacheControl  string
}

type ObjectMeta struct {
    Key         string
    Size        int64
    ContentType string
    ETag        string
    UpdatedAt   time.Time
    Metadata    map[string]string
}
```

`localfs` driver presigning: sign a JWT containing `{key, op, exp}`
and return a URL pointed at our own `/v2/blob/raw/:key?token=…`
handler — same UX as S3 presigning, no driver-specific frontend code.

### Bucket model

Buckets are declared in config:

```toml
[blob.buckets.dataset-artifacts]
driver = "s3"
endpoint = "http://rustfs:9000"
region = "us-east-1"
access_key_env = "RUSTFS_ACCESS_KEY"
secret_key_env = "RUSTFS_SECRET_KEY"
bucket = "aegis-dataset"
max_object_bytes = 5_368_709_120        # 5 GiB
allowed_content_types = ["application/x-tar", "application/zip", "application/octet-stream"]
public_read = false
retention_days = 365

[blob.buckets.user-avatars]
driver = "s3"
endpoint = "http://rustfs:9000"
bucket = "aegis-avatars"
max_object_bytes = 5_242_880            # 5 MiB
allowed_content_types = ["image/png", "image/jpeg", "image/webp"]
public_read = true
cdn_base = "https://cdn.aegis.example/avatars"

[blob.buckets.scratch]
driver = "localfs"
root = "/var/lib/aegis/blob/scratch"
retention_days = 7
```

DB-driven buckets (admin UI for "create a bucket") is a v2 follow-up
and explicitly NOT in v1. Config-driven matches GitOps and lets dev
boxes work without any infra.

Producers reference buckets by **stable name**, never by driver
specifics. The producer signature is `client.PresignPut(ctx, bucket,
PutReq{...})` — no S3 endpoint leaking out.

### Data model

```sql
CREATE TABLE blob_objects (
  id              BIGINT       PRIMARY KEY AUTO_INCREMENT,
  bucket          VARCHAR(64)  NOT NULL,
  storage_key     VARCHAR(512) NOT NULL,  -- the driver-side key
  size_bytes      BIGINT       NOT NULL,
  content_type    VARCHAR(128) NOT NULL,
  etag            VARCHAR(128),
  uploaded_by     INT,                    -- users.id, null for service writes
  entity_kind     VARCHAR(64),            -- e.g. dataset, injection, user
  entity_id       VARCHAR(128),
  metadata        JSON,
  created_at      DATETIME(3)  NOT NULL,
  expires_at      DATETIME(3),            -- driven by bucket.retention_days
  deleted_at      DATETIME(3),
  INDEX idx_bucket_key (bucket, storage_key),
  INDEX idx_entity   (entity_kind, entity_id),
  INDEX idx_uploader (uploaded_by, created_at DESC)
);
```

The DB stores **metadata only** — bytes live in the driver. This
table is what the lifecycle worker walks, what audit/list endpoints
hit, and what enforces per-user quota.

### HTTP surface

Mounted at `/api/v2/blob/*`. JWT required, audience = `portal` or
service-token. Bucket-level RBAC enforced before driver call.

| Method & path | Description |
| --- | --- |
| `POST /buckets/:bucket/presign-put` | Issue presigned PUT. Body: `{key?, content_type, content_length, metadata?}`. Server fills `key` with `{entity_kind}/{ulid}` if absent. Returns `PresignedRequest` + metadata row id. |
| `POST /buckets/:bucket/presign-get` | Issue presigned GET. Body: `{key}`. |
| `GET /buckets/:bucket/objects/:key` | Inline GET (small objects; redirect to presigned URL above `inline_max_bytes`). |
| `HEAD /buckets/:bucket/objects/:key` | Stat. |
| `DELETE /buckets/:bucket/objects/:key` | Soft delete (sets `deleted_at`, removes via driver async). |
| `GET /buckets/:bucket/objects` | List by entity. Query: `entity_kind`, `entity_id`, `cursor`, `limit`. |
| `GET /raw/:token` | Localfs driver only — serves the presigned URL target. Verifies JWT-style token, streams the file. Never enabled when all buckets are S3. |
| `POST /v1/events:object-uploaded` | Internal callback from frontend (or driver event listener) confirming an upload completed. Updates `etag` + `size_bytes` from the driver. |

Response shape:

```json
{
  "object_id": 4711,
  "bucket": "dataset-artifacts",
  "key": "dataset/01HXY2KQ4F0F9B0D5K8N5N7B6A.tar",
  "size_bytes": null,
  "presigned": {
    "method": "PUT",
    "url": "http://rustfs:9000/aegis-dataset/dataset/01HXY...?X-Amz-Algorithm=...",
    "headers": {"Content-Type": "application/x-tar"},
    "expires_at": "2026-05-12T03:14:05Z"
  }
}
```

### Producer SDK (`module/blobclient`)

```go
package blobclient

type Client interface {
    PresignPut(ctx context.Context, bucket string, req PresignPutReq) (*PresignResult, error)
    PresignGet(ctx context.Context, bucket, key string) (*PresignResult, error)
    Stat(ctx context.Context, bucket, key string) (*ObjectMeta, error)
    Delete(ctx context.Context, bucket, key string) error
    // small-payload helpers, optional
    PutBytes(ctx context.Context, bucket, key string, body []byte, opts PutOpts) (*ObjectMeta, error)
    GetBytes(ctx context.Context, bucket, key string) ([]byte, *ObjectMeta, error)
}
```

Two implementations:

- `LocalClient` — calls `module/blob` in-process; used by the monolith
  and by any service that already imports `module/blob`.
- `RemoteClient` — HTTP client to `aegis-blob`; carries a service
  token from `ssoclient`. Used when caller is in a service that does
  not embed `module/blob` (e.g. an independent dataset microservice).

Selected at wiring time by config:

```toml
[blob.client]
mode = "local"   # or "remote"
endpoint = "http://aegis-blob:8085"
```

Identical to the `notificationclient` pattern, by design.

### Standalone binary

`cmd/aegis-blob` mirrors `cmd/aegis-notify`:

```
package main
// cobra entry, default port :8085, --conf <dir>
fx.New(blob.Options(conf, port)).Run()
```

`app/blob/options.go` composes:

- `app.BaseOptions(conf)` + `ObserveOptions` + `DataOptions`
- `module/auth` (JWT verifier — same service tokens as aegis-notify)
- `module/user` (uploader display name lookup)
- `module/blob`
- `httpapi.Module` + `/healthz` decorator

Buckets that use the `s3` driver get their credentials from env vars
referenced by config (see bucket TOML above). RustFS endpoint is just
another S3 endpoint; the driver code does not branch on RustFS.

### Authorization model

- JWT must be valid and audience-allowed.
- Each bucket carries an ACL: `write_roles`, `read_roles`,
  `public_read`. Service tokens get a default `service` role.
- For private buckets, presigned GET requires the caller to either
  have read access or own the object (`uploaded_by == ctx.UserID`).
- Per-user quota (sum of `size_bytes` where `deleted_at IS NULL`)
  enforced before issuing a presigned PUT.

### Failure modes

- Driver `PresignPut` fails → metadata row not created; client gets
  502. (Metadata is written **after** the presign succeeds, so we
  don't leave orphans referencing presign failures.)
- Frontend never calls back `/v1/events:object-uploaded` → object
  exists in driver but metadata row is `size_bytes=null`. Hourly
  reconcile job calls `driver.Stat`, fills in or deletes orphan.
- Driver outage → presign endpoint returns 503; producers retry with
  backoff (idempotent because key generation is content-based when
  caller doesn't provide one).

## Migration plan

1. **Phase A (this PR)**: land `module/blob` (driver iface + localfs +
   s3 stub) + `module/blobclient` (local mode only) + `cmd/aegis-blob`
   skeleton + metadata table migration. Default bucket `scratch`
   (localfs). No producers wired yet.
2. **Phase B**: complete `s3` driver, deploy RustFS in Helm chart,
   declare `dataset-artifacts` + `user-avatars` buckets.
3. **Phase C**: wire first real producer — `module/user` writes
   avatars; frontend gets the upload flow. Use this to harden the
   presign+confirm round trip.
4. **Phase D**: migrate dataset writes through `module/blob`.

## Alternatives considered

- **Skip the abstraction, every module writes to S3 directly.** Tried
  this pattern in prior projects — every consumer reinvents bucket
  policy, presigning, retention. Inconsistent and risky.
- **Use a managed product (Cloudflare R2 SDK directly, etc.).** No
  abstraction means switching providers requires changing every
  caller. Driver iface costs ~200 lines and buys provider freedom.
- **Skip presigned URLs, proxy all bytes.** Simpler auth but the blob
  service becomes the bandwidth bottleneck. RustFS deployment exists
  precisely so we can offload bytes to it.
- **DB-driven buckets in v1.** Adds an admin UI and migration story
  with zero current need. Config-driven is the right v1.

## Open questions

1. **Where do driver credentials live?** Proposal: bucket config
   references env-var names, not literal secrets. Helm/compose
   populates the env from sealed secrets. Same model as the SSO
   private-key path.
2. **Localfs presign token signing key.** Reuse SSO's RSA keypair?
   Or a dedicated HMAC key? Proposal: dedicated HMAC, narrower
   blast radius if leaked.
3. **Object-key generation.** Random ULID under
   `{entity_kind}/{ulid}` is the v1 default. Some callers will want
   deterministic keys (avatars by user id, for de-dupe). Solve via
   `PresignPutReq.Key` override.
4. **Inline GET threshold.** 64 KiB? 1 MiB? Set per bucket; default
   64 KiB.

## Acceptance criteria

- `aegis-blob serve --port 8085` boots against a localfs scratch
  bucket and an S3 bucket pointed at a local MinIO instance.
- Frontend can: (1) call `POST /api/v2/blob/buckets/user-avatars/
  presign-put`, (2) PUT the bytes directly to the returned URL,
  (3) call back `:object-uploaded`, (4) reference the object by id
  in `users.avatar_object_id`.
- 1k presign calls/sec on a single replica without driver errors.
- `pnpm check` on the console stays green after adding `blobclient`
  to `apps/console`.
