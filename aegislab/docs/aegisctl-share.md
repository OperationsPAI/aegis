# aegisctl share

`aegisctl share` produces ephemeral, public `/s/<code>` links for a
single file in a blob bucket. The link auto-expires (TTL + max-views
cap) and can be revoked at any time.

## Upload (preferred: presigned PUT)

`aegisctl share upload <file>` drives the three-step presigned-PUT flow
by default:

  1. **POST `/api/v2/share/init`** — body `{filename, size, content_type,
     ttl_seconds, max_views}`. Server validates per-user quota + the
     per-file size cap (`share.max_upload_bytes`), reserves a short
     code in `pending` lifecycle, and returns
     `{code, presigned_put_url, expires_at, max_size, commit_url}`. The
     PUT URL is good for 15 minutes.
  2. **`PUT <presigned_put_url>`** — the client streams the file body
     directly to the object store. The bytes never touch the aegislab
     process. `aegisctl share upload` computes a streaming SHA-256 over
     the body during this leg and prints a stderr progress line every
     ~5 seconds. Disable the hash with `--no-sha256` for a marginal
     speedup.
  3. **POST `/api/v2/share/<code>/commit`** — body
     `{size, content_type, sha256}` (all optional). Server calls
     `Stat()` on the backend to confirm the PUT actually landed, checks
     for size mismatch / oversize, then flips lifecycle from `pending`
     to `live`. Idempotent on retry.

Why this exists: the deprecated streaming-multipart upload tunnels the
body through aegislab and the edge proxy. On slow / lossy international
links (e.g. byte-cluster from outside CN) the buffered hop chokes the
transfer — a 17.9 MB APK measured 7m54s, ~38 KB/s. Presigned PUT lets
the bytes ride the object store's own bandwidth (and a future
CDN / Global-Accelerator) directly.

## Legacy multipart

`aegisctl share upload <file> --legacy` falls back to the original
`POST /api/v2/share/upload` multipart path through the typed SDK. The
endpoint is **deprecated** but kept for small-file debugging and for
SDK clients that haven't been regenerated yet.

## Other subcommands

  * `aegisctl share list` — your own active links.
  * `aegisctl share revoke <code>` — drop a link + its underlying blob.
  * `aegisctl share download <code>` — pull a `/s/<code>` to disk.

## Lifecycle states

`share_links.lifecycle_state` is one of:

  * `pending` — `init` succeeded, the PUT hasn't been committed yet.
    The link is invisible to `/s/<code>` (returns 410 Gone).
  * `live` — the row is fully valid and serves `/s/<code>`. New rows
    created through the deprecated multipart path are written directly
    as `live`.

Pending rows linger until their TTL — a periodic GC sweep can be added
later; for v1 they're orphaned harmlessly until expiry.
