# aegisctl blob / bucket reference

This page documents the `blob` and `bucket` noun-groups in `aegisctl`. These
commands give the CLI an mc-comparable file/blob surface against the
aegis-blob microservice; the lower-level CRUD that complements the existing
`aegisctl share` short-code workflow.

All commands inherit the global flags from `aegisctl --help`: `--server`,
`--token`, `--output`, `--quiet`, `--non-interactive`, `--dry-run`, etc.

## Path syntax

All remote paths use the canonical `<bucket>:<key>` form (mc-style):

| Input                                | Interpreted as                     |
| ------------------------------------ | ---------------------------------- |
| `aegis-pages:my-site/index.md`       | Remote object                      |
| `aegis-pages:`                       | Bucket root (for `ls` / `rm -r`)   |
| `aegis-pages:k:with:colons`          | Remote object (only first `:` splits) |
| `./foo.md`, `/abs/path`, `~/path`    | Local path (tilde is expanded)     |
| `foo.md`, `relative/path`            | Local path                         |
| `-`                                  | Stdin (cp src) or stdout (cp dst)  |
| `C:\Windows\path`                    | Local — `C` fails the bucket regex |

Bucket names must match `^[a-z0-9][a-z0-9._-]{1,62}$`. Anything else parses
as a local path; if the local file doesn't exist either, the caller surfaces
a "no such file or directory" diagnostic from `os.Stat`.

## blob

### `aegisctl blob ls <bucket>[:<prefix>]`

Paginates the storage driver and prints `KEY | SIZE | UPDATED_AT |
CONTENT_TYPE`. Use `--output ndjson` for piping; `--max N` to cap.

### `aegisctl blob stat <bucket>:<key>`

Single-object metadata: size, content-type, etag, last-modified, user
metadata. Exits 7 if the object is absent.

### `aegisctl blob cat <bucket>:<key>`

Streams the body to stdout. Refuses to write a binary content-type to a TTY
unless `--force` is passed (mc-style guard).

### `aegisctl blob cp <src> <dst> [-r] [--if-not-exists] [--content-type TYPE]`

Local→Remote uploads via presigned PUT (the SDK has no direct PutObject;
the CLI requests a signed URL and PUTs to it). Remote→Local streams via the
inline-GET route. Remote→Remote uses the server-side `/copy` endpoint when
both sides share a bucket, otherwise falls back to download+upload.

`--dry-run` prints a `would_copy` plan (JSON when `--output json`, plain
lines otherwise).

### `aegisctl blob mv <src> <dst>`

`cp` then delete src. Same-bucket remote→remote uses the server's atomic
copy+delete; everything else is _not_ atomic and is documented as such in
the command help.

### `aegisctl blob rm <bucket>:<key> [-r] [--yes]`

Single-object delete by default; `-r` required for prefixes. Recursive
delete uses the server's batch-delete endpoint. Confirms before destructive
operations unless `--yes` / `--force` / `--non-interactive`.

### `aegisctl blob find <bucket>[:<prefix>] [--name GLOB] [--max-depth N]`

Server-side list, client-side filter. `--name` accepts a `path.Match` glob
applied to the basename. Default output is ndjson; `--output table` for
human reading.

### `aegisctl blob mirror <src> <dst> [-r] [--delete] [--dry-run]`

One-way sync. With `--delete`, removes objects in dst that aren't in src
(prompts unless `--yes`). Local↔Remote and Remote→Remote supported;
Local→Local is refused (use rsync).

### `aegisctl blob presign <bucket>:<key> [--ttl 1h] [--method get|put]`

Prints the presigned URL to stdout; expires_at + method to stderr or as
JSON. URL is auth-free for the lifetime of the signature.

## bucket

### `aegisctl bucket ls`

Lists buckets registered with the service: `NAME | DRIVER |
MAX_OBJECT_BYTES | PUBLIC`.

### `aegisctl bucket create <name> --driver <localfs|s3> [...]`

Provisions a new bucket. Driver-specific flags: `--root` (localfs),
`--endpoint` / `--region` / `--bucket` (s3). Optional: `--public`,
`--max-object-bytes`, `--retention-days`, `--lifecycle <file.json>` (parsed
for validity but not yet consumed by the backend).

### `aegisctl bucket get <name>`

Shows config for one bucket. Looks the bucket up in the list response;
errors 7 if absent.

### `aegisctl bucket rm <name> [--force]`

**Currently stubbed.** The aegislab backend does not yet expose a
bucket-delete endpoint (`DELETE /api/v2/blob/buckets/:name`). The command
shape is in place so callers can write scripts against it, but invocation
returns a server-side error until the backend handler ships. Tracking
needed in a separate task.

## SDK / backend coupling

Wired to generated SDK methods where possible:

| Verb              | Mechanism                                                              |
| ----------------- | ---------------------------------------------------------------------- |
| `blob ls`         | `BlobAPI.BlobListObjects` (paginated)                                  |
| `blob stat`       | `BlobAPI.BlobStat`                                                     |
| `blob cat`        | `client.Client.GetRaw` (inline-GET route, streams without buffering)   |
| `blob cp` upload  | `BlobAPI.BlobPresignPut` + raw HTTP `PUT` to signed URL                |
| `blob cp` dl      | `client.Client.GetRaw` (inline-GET)                                    |
| `blob cp` srv-cpy | Hand-written `POST /api/v2/blob/buckets/:b/copy` (SDK lacks endpoint)  |
| `blob mv`         | Same as cp, with `delete_src` flag (within bucket) or cp+rm fallback   |
| `blob rm` single  | `BlobAPI.BlobDelete`                                                   |
| `blob rm` -r      | Hand-written `POST /api/v2/blob/buckets/:b/delete-batch`               |
| `blob find`       | `BlobAPI.BlobListObjects` + client-side glob                           |
| `blob mirror`     | Composes `cp` + `rm` primitives based on diff                          |
| `blob presign`    | `BlobAPI.BlobPresignGet` / `BlobAPI.BlobPresignPut`                    |
| `bucket ls`       | `BlobAPI.BlobListBuckets`                                              |
| `bucket create`   | Hand-written `POST /api/v2/blob/buckets` (SDK lacks endpoint)          |
| `bucket get`      | `BlobAPI.BlobListBuckets` + client-side filter                         |
| `bucket rm`       | Stubbed — backend handler missing                                      |

## Exit codes

The standard `aegisctl` exit-code contract applies. Notable cases:

- `2` — usage / argument validation, including `--method` typo, missing
  `--driver`, `--dry-run` on read commands, Local→Local cp/mv/mirror.
- `3` — 401/403 from the server.
- `7` — 404, including stat/cat/get for missing objects or buckets.
- `8` — 409, including bucket-already-exists on `bucket create`.
- `10` — 5xx.
- `11` — decode failure (rare; surfaces under high-mismatch SDK drift).

## TODOs surfaced during this work

- The aegis-blob server lacks a bucket-delete handler. `bucket rm` is wired
  but always fails until the backend ships `DELETE /api/v2/blob/buckets/:name`.
- The aegis-blob server has `CreateBucket` and `CopyObject` handlers that
  are not exposed by the generated Go SDK. The CLI falls back to
  hand-written `client.Client.Post` for both. The next SDK regen should
  pick them up; the CLI can switch to the typed SDK calls in a follow-up.
- `--lifecycle <file.json>` is parsed for JSON validity but not posted —
  the bucket-create request body has no lifecycle field on the server side
  today.
- `blob mirror` decides "missing in dst" by key-name only — it does not
  compare etag/size yet. Acceptable for prod-pages-style write-then-mirror
  flows, but should grow into a content-hash compare for general use.
