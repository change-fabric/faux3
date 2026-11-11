# AGENTS.md

Rules for coding agents (and humans) working in this repository.

## Mission

Faux3 is a single-container, S3-compatible object store for local Docker development: one container, one volume, one S3 endpoint, no AWS required. It implements ten operations with strict SigV4 and nothing else.

## Non-goals

No multipart upload, IAM, ACLs, policies, versioning, lifecycle, object lock, SSE, events, web UI, HA, virtual-host addressing, SigV4a, or TLS, and no new S3 operations, without a new ADR. See [docs/limitations.md](docs/limitations.md).

## Roadmap

[ROADMAP.md](ROADMAP.md) lists candidate operations and features. Do not implement a roadmap item without an accepted ADR and the maintainer's explicit go-ahead.

## Host doctrine

Never install go, node, or aws on the host. Everything runs in containers:

```sh
scripts/lint.sh          # gofmt, go vet, staticcheck
scripts/test.sh          # go unit tests with -race
scripts/integration.sh   # SDK and CLI suites in Docker Compose
docker build -t faux3:dev .
scripts/go.sh <args>     # any other go command
```

## Commits

While the project is local-only, commit straight to `main`, early and often, one small coherent step per commit. Never push or tag while local-only.

## Versioning

Versions stay 0.x. Never create, propose, or automate a v1.0.0 tag; that is the maintainer's explicit choice only (ADR 0009). Keep the `## [Unreleased]` section of CHANGELOG.md current with every user-visible change.

## Compatibility contract

- Any change to S3 behavior needs AWS SDK for JavaScript v3 and AWS CLI v2 integration tests.
- Every compatibility bug gets a regression test.
- Errors are S3 XML documents (`<Error><Code/><Message/><Resource/><RequestId/><HostId/>`), with BucketName and Key where relevant. HEAD errors have no body. Unsupported operations return 501 NotImplemented.

## Key invariants

- The authoritative key is the `key` field in `meta.json`. Never derive paths from keys, or keys from paths.
- Never buffer a whole request or response body; stream.
- Temporary files stay under `/data/.faux3/tmp` on the data filesystem, so renames are atomic.
- Secrets are never logged.

## Adding an operation

1. An ADR in docs/adr, accepted by the maintainer.
2. Router entry and query allowlist in `internal/s3api/router.go`.
3. Handler in `internal/s3api`.
4. Store support in `internal/store`.
5. Unit tests.
6. SDK test in `test/integration/node`.
7. CLI test in `test/integration/cli`.
8. Row in docs/compatibility.md, entry moved in ROADMAP.md.
9. CHANGELOG.md line under Unreleased.

## Environment variables

| Variable | Default |
| --- | --- |
| FAUX3_ACCESS_KEY_ID | faux3 |
| FAUX3_SECRET_ACCESS_KEY | faux3 |
| FAUX3_REGION | us-east-1 |
| FAUX3_DATA_DIR | /data |
| FAUX3_PORT | 9000 |
| FAUX3_CORS_ORIGINS | unset (CORS off) |
| FAUX3_FSYNC | true |
| FAUX3_MAX_OBJECT_SIZE | 5368709120 |
| FAUX3_CLOCK_SKEW | 15m |
| FAUX3_LOG_LEVEL | info |

Details in [docs/configuration.md](docs/configuration.md).

## ADRs and diagrams

ADRs live in [docs/adr](docs/adr/README.md). Diagrams live in `docs/diagrams/`:

- [architecture.svg](docs/diagrams/architecture.svg): clients, the container, and the data volume.
- [request-pipeline.svg](docs/diagrams/request-pipeline.svg): middleware to response for every request.
- [put-object-sequence.svg](docs/diagrams/put-object-sequence.svg): the atomic PutObject write.
- [storage-layout.svg](docs/diagrams/storage-layout.svg): the on-disk layout under `/data`.

When the architecture changes, update the affected diagrams with /cf:diagram.

## Prose rules

Repository prose is plain ASCII: no em-dashes, unicode bullets, ellipsis characters, or smart quotes. No AI attribution footers in repository docs.
