# aegisctl page reference

This page documents the `page` noun-group in `aegisctl`. These commands
front the aegis pages module: small static-content bundles (a single
markdown file or a directory of markdown + assets) that the server
publishes at `/p/<slug>`.

All commands inherit the global flags from `aegisctl --help`: `--server`,
`--token`, `--output`, `--quiet`, `--non-interactive`, `--dry-run`, etc.

## Visibility

`page push` and `page get` surface a `visibility` field with three values:

| Value             | Meaning                                                         |
| ----------------- | --------------------------------------------------------------- |
| `public_listed`   | Indexed in `aegisctl page ls --public` and reachable by URL.    |
| `public_unlisted` | Reachable by URL only; never indexed.                           |
| `private`         | Only the owner can fetch via `/p/<slug>` (auth-gated).          |

The CLI validates the value client-side before sending; an unknown
visibility exits `ExitCodeUsage`(2).

## Frontmatter defaults

`page push` parses the top of the source markdown for a one-block YAML-ish
header and uses the `slug:` and `title:` keys as fallbacks when the
respective CLI flag is absent:

```markdown
---
slug: release-notes
title: Release notes for v1.2.3
---

# Release notes for v1.2.3
...
```

For a directory push, the frontmatter is read from `index.md` at the root
(or `index.markdown`). For a single-file push the file itself is parsed.
If frontmatter doesn't contain a `title:`, the CLI falls back to the
first `# H1` heading; otherwise the title defaults to empty (server
keeps its own default).

CLI flags always win over frontmatter; frontmatter wins over filename-
derived defaults.

## Path handling on push

`page push <dir-or-file>` walks one of two paths:

- **Single file** — the argument must end in `.md` / `.markdown`. The
  multipart filename is the basename.
- **Directory** — every regular file under the root is included
  (forward-slashed, relative to the root, as the multipart filename).
  Symlinks are refused. Dotfiles (`.git`, `.DS_Store`, …) are skipped.
  At least one `.md` must be present somewhere in the tree.

Client-side guardrails (matching server limits):

| Constraint        | Limit       |
| ----------------- | ----------- |
| Per-file size     | 10 MiB      |
| Total upload size | 50 MiB      |
| File count        | 200         |

Paths that would escape the upload root (`..`, absolute components after
relative conversion) are rejected before any HTTP traffic.

## page

### `aegisctl page push <dir-or-file> [--slug X] [--visibility V] [--title T] [--dry-run]`

Creates a new page site. The slug defaults to (in order) the `--slug`
flag, the frontmatter `slug:` key, the filename stem (or directory
basename) lowercased and dash-normalised. Visibility defaults to the
server's own policy when omitted.

`--dry-run` prints the resolved slug / title / visibility and the full
list of files that would be uploaded — no network IO.

Successful push prints `id`, `slug`, `title`, `visibility`, `file_count`,
`size_bytes`, and the absolute share URL. With `--output json` the same
fields are emitted as a single JSON object.

Slug collisions (server `400 slug_taken`) exit `ExitCodeConflict`(8).

### `aegisctl page ls [--mine|--public] [--limit N --offset M]`

Lists page sites. `--mine` (the default) lists your own; `--public`
switches to publicly visible pages. The two flags are mutually
exclusive.

Pagination: when `--limit` is **not** explicitly passed, the CLI
auto-follows pages of 20 up to a soft cap of 200 items; pass `--limit`
to switch to a single explicit window.

Table output columns: `SLUG | TITLE | VISIBILITY | FILES | SIZE |
UPDATED`. `--output json` and `--output ndjson` emit the full
`PagesPageSiteResponse` records.

### `aegisctl page get <slug-or-id> [--output table|json]`

Shows one page in detail. The argument can be a positive integer (id)
or a slug; slugs are resolved by listing both `mine` and `public` and
matching client-side (no dedicated slug-lookup endpoint exists yet).

JSON output includes the file list (`path`, `size_bytes`); table output
prints a `Files:` block when present.

### `aegisctl page rm <slug-or-id> [--yes|--force] [--dry-run]`

Deletes a site. Refuses to run without `--yes` / `--force` /
`--non-interactive` unless stdin is a TTY and the operator confirms
interactively. `--dry-run` prints the planned delete without touching
the server. Exit `ExitCodeNotFound`(7) when the slug doesn't resolve;
`ExitCodeUsage`(2) when interactive confirmation is required but
unavailable.

### `aegisctl page open <slug>`

Looks the slug up server-side (so typos exit 7 instead of opening a
404 in your browser) and launches the platform's default browser at
`<server>/p/<slug>`:

| OS      | Command                |
| ------- | ---------------------- |
| Linux   | `xdg-open <url>`       |
| macOS   | `open <url>`           |
| Windows | `cmd.exe /c start "" <url>` |

Refused under `--non-interactive` (exit `ExitCodeUsage`(2)) — opening
a browser is the opposite of headless.

## SDK / backend coupling

| Verb        | Mechanism                                                       |
| ----------- | --------------------------------------------------------------- |
| `page push` | Raw multipart `POST /api/v2/pages` — the generated SDK's `PagesAPIService.PagesCreate` accepts only a single `*os.File` with the OS basename as the part filename, which doesn't match the multi-file + site-relative-path contract. The CLI builds the multipart body directly with the same transport / TLS / Authorization plumbing as `newAPIClient()`. |
| `page ls`   | `PagesAPI.PagesListMine` / `PagesAPI.PagesListPublic`           |
| `page get`  | `PagesAPI.PagesDetail` (after a slug→id resolver scan)          |
| `page rm`   | `PagesAPI.PagesDelete`                                          |
| `page open` | Resolve via `PagesDetail`, then OS-specific browser launcher    |

The typed `PagesAPI.PagesReplace` and `PagesAPI.PagesUpdate` methods
exist in the SDK but aren't surfaced as verbs yet; track that in a
follow-up if/when an `update` or `replace` verb is needed.

## Exit codes

Standard `aegisctl` codes apply. The page-specific mappings are:

| Situation                                     | Exit code |
| --------------------------------------------- | --------- |
| Slug already taken (HTTP `400 slug_taken`)    | 8 (conflict) |
| Invalid `--visibility`                        | 2 (usage)    |
| Slug doesn't resolve in `get`/`rm`/`open`     | 7 (not found) |
| Per-file or total size cap exceeded client-side | 1 (unexpected) |
| `page open` invoked with `--non-interactive`  | 2 (usage)    |

## See also

- `aegisctl share --help` — short-code `/s/<code>` file-share links
- `aegisctl blob --help` — bucket-level CRUD
- `aegisctl-blob.md`, `aegisctl-tls.md`
