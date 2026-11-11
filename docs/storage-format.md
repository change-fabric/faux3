# Storage format

Do not edit `/data` by hand. The layout is an implementation detail, versioned by a format file, and not meant to be browsed. Use an S3 client to read and write objects.

## Layout

```
/data/.faux3/format                                   "1\n"
/data/.faux3/lock                                     exclusive flock, one process per data dir
/data/.faux3/tmp/                                     staging, same filesystem
/data/buckets/<bucket>/.bucket.json                   {"name": ..., "created": RFC3339Nano, "region": ...}
/data/buckets/<bucket>/objects/<h[0:2]>/<h>/data      object bytes
/data/buckets/<bucket>/objects/<h[0:2]>/<h>/meta.json object metadata
```

`h` is the lowercase hex SHA-256 of the key bytes. Keys are never derived from paths: the authoritative key is the `key` field in `meta.json`. Hashed directories avoid collisions on case-insensitive or Unicode-normalizing filesystems (macOS bind mounts), conflicts between keys like `a` and `a/b`, and file name length limits.

![Storage layout](diagrams/storage-layout.svg)

## meta.json

| Field | Type | Meaning |
| --- | --- | --- |
| `key` | string | The object key, authoritative |
| `size` | number | Body length in bytes |
| `etag` | string | Hex MD5 of the body (served quoted) |
| `contentType` | string | Defaults to `application/octet-stream` |
| `contentEncoding` | string | With `aws-chunked` removed |
| `contentDisposition` | string | |
| `cacheControl` | string | |
| `contentLanguage` | string | |
| `expires` | string | |
| `userMetadata` | object | `x-amz-meta-*` names, lowercased, without the prefix |
| `checksums` | object | Algorithm name to base64 checksum |
| `lastModified` | string | RFC3339Nano, UTC |

## Format version

`.faux3/format` holds `1`. A faux3 binary refuses to start on any other value. A future layout change bumps the number and needs an ADR.

## Writes

Each PutObject streams into a fresh directory under `.faux3/tmp/`, verifies size and checksums, fsyncs (when `FAUX3_FSYNC=true`), writes `meta.json`, then swaps the directory into place with renames under a per-key lock. Readers see the old object or the new one, never a mix. See [ADR 0005](adr/0005-atomic-writes-and-recovery.md).

## Recovery at startup

1. Everything in `.faux3/tmp/` is removed.
2. Any key directory without `meta.json` is removed.
3. A `meta.json` whose key hashes to a different directory is logged as a warning and skipped.
4. The in-memory index is rebuilt from every `meta.json`. Startup time grows with the number of keys.

## Single instance

At startup faux3 takes an exclusive, non-blocking `flock` on `.faux3/lock`. A second faux3 process pointed at the same data directory fails to get the lock, prints that the directory is in use by another faux3 process, and exits with status 1. The kernel releases the lock when the process exits, including on SIGKILL, so a crash never leaves a stale lock. The lock file itself is empty and safe to leave in place.
