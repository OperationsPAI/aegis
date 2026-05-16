# Pages API

The `pages` module hosts gist / GitHub-Pages-style static sites. Markdown is
rendered server-side with GitHub styling; other files (images, CSS, JSON, …)
stream through unchanged. Each site lives in blob storage under the
`aegis-pages` bucket at the prefix `{site_uuid}/`.

- Management API: `/api/v2/pages/*` — JSON, JWT human-user auth.
- Public SSR: `/p/:slug/*filepath` — HTML for `.md`, raw for everything else.
- Static assets (vendored CSS): `/static/pages/*`.

## Data model

`page_sites` (GORM):

| column      | type     | notes                                                |
| ----------- | -------- | ---------------------------------------------------- |
| id          | int64    | primary key                                          |
| site_uuid   | string36 | immutable, used as the blob prefix                   |
| owner_id    | int      | user id of the creator                               |
| slug        | string128| unique, `^[a-z0-9][a-z0-9-]{0,62}$`                  |
| visibility  | string32 | `public_listed` \| `public_unlisted` \| `private`    |
| title       | string256| display name                                         |
| size_bytes  | int64    | denormalised total of all file sizes                 |
| file_count  | int32    | number of objects under `{site_uuid}/`               |
| created_at  | timestamp| auto                                                 |
| updated_at  | timestamp| auto                                                 |

Site files are not tracked in the DB — the blob `List` call under the site
prefix is the source of truth. The DB only stores per-site metadata.

## Visibility model

- `public_listed`   — listed in `GET /api/v2/pages/public`, served at `/p/:slug` without auth.
- `public_unlisted` — not listed, but anyone with the link can read.
- `private`         — owner only. Anonymous SSR requests redirect to
  `/auth/login?return_to=<full path>`; logged-in non-owners get a 404 (to
  avoid leaking existence).

## Endpoints

All management endpoints use `JWTAuth + RequireHumanUserAuth` unless noted.
Bodies and responses use the platform-wide `dto.GenericResponse[T]` envelope.

### `POST /api/v2/pages` — create site

`multipart/form-data`. Text fields and file parts:

| field      | required | notes                                                         |
| ---------- | -------- | ------------------------------------------------------------- |
| slug       | no       | auto-derived from title or first filename if absent           |
| visibility | no       | default `public_unlisted`                                     |
| title      | no       | display name                                                  |
| files      | yes      | one or more file parts; each part's filename is the rel path  |

At least one `.md` file is required. File-part filenames must be relative
paths (no leading `/`, no `..`).

Response (201):

```json
{
  "code": 201,
  "message": "page site created",
  "data": {
    "id": 17,
    "slug": "release-notes-1-0",
    "visibility": "public_unlisted",
    "title": "Release Notes 1.0",
    "owner_id": 42,
    "size_bytes": 2048,
    "file_count": 3,
    "created_at": "2026-05-15T10:30:00Z",
    "updated_at": "2026-05-15T10:30:00Z",
    "url": "/p/release-notes-1-0"
  }
}
```

### `POST /api/v2/pages/{id}/upload` — replace all files

Same multipart shape as create. Owner only. The current prefix is listed
and every key deleted, then the new payload is written. The site row is
re-counted on success. Returns 200 + the same `PageSiteResponse`.

### `PATCH /api/v2/pages/{id}` — update metadata

Owner only. Body:

```json
{ "slug": "new-slug", "visibility": "public_listed", "title": "New Title" }
```

All fields optional. Slug changes go through the uniqueness check.

### `DELETE /api/v2/pages/{id}` — delete site

Owner only. Lists the blob prefix, deletes every object, then removes the
DB row. Returns 204.

### `GET /api/v2/pages` — my sites

Lists the caller's sites, newest first. Query: `?limit=&offset=`.

```json
{ "data": { "items": [ /* PageSiteResponse[] */ ] } }
```

### `GET /api/v2/pages/public` — public listing

`OptionalJWTAuth`. Returns only sites where `visibility = public_listed`.

### `GET /api/v2/pages/{id}` — detail

`OptionalJWTAuth`. Returns `PageSiteResponse` plus `files: [{path, size_bytes}]`.
Private sites are restricted to the owner — non-owners (or anonymous
callers) get 404.

## Public SSR

### `GET /p/:slug` and `GET /p/:slug/*filepath`

`OptionalJWTAuth`. Resolution:

1. Lookup slug → site. Missing slug → 404 plain-text.
2. If `visibility = private`:
    - anonymous → 302 `/auth/login?return_to=<full path>`
    - non-owner → 404
3. Clean filepath: percent-decode, strip leading `/`, reject any `..`
   segment. Empty or trailing-slash paths get `index.md` appended.
4. The blob key is `{site_uuid}/{cleaned_path}`.
5. `.md` (case-insensitive) → render to HTML; everything else → stream
   raw with `Content-Type` from the blob meta or extension fallback.

### Markdown rendering

`goldmark` with the following extensions:

- GFM, Footnote, DefinitionList, Typographer, Linkify
- `goldmark-meta` (YAML frontmatter)
- `goldmark-highlighting` (Chroma, `github` style)

Frontmatter `title` overrides the DB site title in the `<title>` element
but does not change the heading on the page.

### Link rewriting

Walking the AST, every `Link` / `Image` destination is rewritten when it
is relative. Untouched:

- `http://` / `https://`
- protocol-relative `//`
- `mailto:` / `tel:`
- fragment-only `#anchor`
- absolute-path `/foo/bar`

Otherwise the URL is resolved against the current document's directory
and prefixed with `/p/{slug}/`. So inside `docs/index.md`, the markdown
`[next](other.md)` produces `<a href="/p/{slug}/docs/other.md">`.

### Static asset routes

The two vendored CSS files (`github-markdown.css`, `chroma-github.css`)
are embedded in the binary and served at `/static/pages/*`. Used by the
SSR layout template.

## Limits

- `MaxFileSize`  = 10 MiB per individual file (HTTP 413 on excess)
- `MaxTotalSize` = 50 MiB per site (HTTP 413)
- `MaxFiles`     = 200 objects per site (HTTP 413)
- Slug regex: `^[a-z0-9][a-z0-9-]{0,62}$` (HTTP 400 on miss)

## Error codes

| HTTP | message                | meaning                                           |
| ---- | ---------------------- | ------------------------------------------------- |
| 400  | `invalid_slug`         | slug failed regex                                 |
| 400  | `slug_taken`           | slug already registered                           |
| 400  | `invalid_visibility`   | not one of the three accepted values              |
| 400  | `no_files`             | upload had zero files or no `.md`                 |
| 400  | `path_traversal`       | a filename was absolute or contained `..`         |
| 403  | `forbidden`            | caller is not the owner                           |
| 404  | `page site not found`  | slug or id unknown                                |
| 413  | `file_too_large`       | one file exceeded `MaxFileSize`                   |
| 413  | `total_too_large`      | aggregate exceeded `MaxTotalSize`                 |
| 413  | `too_many_files`       | upload had more than `MaxFiles` parts             |

## Permissions

The module registers three rules; SSO is the source of truth for who
holds them. The handler does not consult them today — owner-equality is
enforced directly — but they are advertised so downstream tooling and
admin role grants can target them:

- `pages:read:own`     — read own pages
- `pages:update:own`   — create / modify own pages
- `pages:manage:all`   — admin override (planned: bypass owner check)

## Bucket configuration

The bucket name is `aegis-pages`. The module does **not** auto-create the
bucket. Operators must declare it once via the existing `blob` module's
config or the `POST /api/v2/blob/buckets` endpoint. Example TOML:

```toml
[blob.buckets.aegis-pages]
driver = "s3"
endpoint = "..."
bucket = "aegis-pages"
public_read = false
```

If the bucket is missing the first create call returns 500 with the
underlying blob-not-found error.

## Out of scope for v1

- Nested sidebar tree (current nav is a flat alpha-sorted list).
- Per-page ACL beyond `public_listed / public_unlisted / private`.
- Custom domains.
- Asset deduplication across sites.
- Pre-rendering / caching (every request re-parses the markdown).
