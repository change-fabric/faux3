<p align="center"><img src="docs/hero.gif" width="480" alt="Faux3: a lilac crayon scribble draws in, then a line-drawn sailboat settles on the waves and gently rocks"></p>

# Faux3

S3 without the cloud.

One container. One volume. One S3 endpoint. No AWS required.

Faux3 is an MIT-licensed, single-container, S3-compatible object store for local Docker development. It speaks a strict subset of the S3 API over path-style addressing, validates SigV4 on every request, and keeps everything in one data volume.

> Status: pre-alpha. Releases stay on 0.x; expect breaking changes between minor versions. Faux3 is a development tool, never a production store.

## Quick start (under 2 minutes)

```sh
docker run -d --name faux3 -p 127.0.0.1:9000:9000 -v faux3-data:/data ghcr.io/change-fabric/faux3:0.1.0
```

Until v0.1.0 is published to GHCR, build the image from a clone instead:

```sh
git clone https://github.com/change-fabric/faux3 && cd faux3
docker build -t faux3 .
docker run -d --name faux3 -p 127.0.0.1:9000:9000 -v faux3-data:/data faux3
```

The endpoint is `http://localhost:9000`, region `us-east-1`, access key `faux3`, secret key `faux3`. Health check: `curl http://localhost:9000/-/healthz` prints `ok`.

### AWS CLI v2

The CLI needs path-style addressing, and no environment variable sets it, so put it in a config file:

```ini
# ~/.aws/faux3-config
[default]
region = us-east-1
s3 =
  addressing_style = path
  multipart_threshold = 64MB
```

```sh
export AWS_CONFIG_FILE=~/.aws/faux3-config
export AWS_ENDPOINT_URL=http://localhost:9000
export AWS_ACCESS_KEY_ID=faux3
export AWS_SECRET_ACCESS_KEY=faux3
export AWS_DEFAULT_REGION=us-east-1

aws s3 mb s3://demo
echo hello > hello.txt && aws s3 cp hello.txt s3://demo/hello.txt
aws s3 ls s3://demo
```

Without `addressing_style = path` the CLI puts the bucket in the host name (`demo.localhost`), which does not resolve. `multipart_threshold` keeps uploads up to 64 MB on single PutObject requests, since faux3 has no multipart upload.

### AWS SDK for JavaScript v3

```js
import { S3Client, PutObjectCommand } from "@aws-sdk/client-s3";

const s3 = new S3Client({
  endpoint: "http://localhost:9000",
  region: "us-east-1",
  forcePathStyle: true,
  credentials: { accessKeyId: "faux3", secretAccessKey: "faux3" },
});

await s3.send(new PutObjectCommand({ Bucket: "demo", Key: "hello.txt", Body: "hello" }));
```

### Docker Compose

```yaml
services:
  faux3:
    image: ghcr.io/change-fabric/faux3:0.1.0
    ports: ["127.0.0.1:9000:9000"]
    volumes: ["faux3-data:/data"]
    environment:
      FAUX3_CORS_ORIGINS: http://localhost:5173   # only for browser uploads
    healthcheck:
      test: ["CMD", "/faux3", "healthcheck"]
      interval: 2s
      retries: 30
  app:
    build: .
    environment:
      S3_ENDPOINT: http://faux3:9000
    depends_on:
      faux3: { condition: service_healthy }
volumes:
  faux3-data:
```

A runnable version lives in [examples/compose](examples/compose/compose.yaml), with JS scripts in [examples/js](examples/js/README.md). Browser uploads with presigned URLs need `FAUX3_CORS_ORIGINS` set and the presigning client created with `requestChecksumCalculation: "WHEN_REQUIRED"`; see [docs/configuration.md](docs/configuration.md).

## Supported operations

| Operation | Notes |
| --- | --- |
| CreateBucket | Existing bucket returns 409 BucketAlreadyOwnedByYou |
| ListBuckets | |
| HeadBucket | |
| DeleteBucket | Non-empty returns 409 BucketNotEmpty |
| PutObject | Streams to disk; checksums, Content-MD5, user metadata, `If-None-Match: *` |
| GetObject | Single byte ranges, conditionals, `response-*` overrides |
| HeadObject | |
| DeleteObject | |
| DeleteObjects | Batch delete of up to 1000 keys, quiet mode |
| ListObjectsV2 | Prefix, delimiter, pagination, `encoding-type=url` |

Everything else answers 501 NotImplemented, apart from a GetBucketLocation stub for CLI region discovery. Details, client versions tested, and which CLI commands work are in [docs/compatibility.md](docs/compatibility.md). Planned and unplanned operations are tracked in [ROADMAP.md](ROADMAP.md).

## Limitations

No multipart upload, DeleteObjects, or CopyObject, so `aws s3 rm --recursive`, `aws s3 sync --delete`, `aws s3 mv`, and S3-to-S3 `aws s3 cp` fail. One static credential, path-style only, plain http, no IAM, versioning, or encryption. Full list in [docs/limitations.md](docs/limitations.md).

## Configuration

All settings are `FAUX3_*` environment variables (credentials, region, port, CORS origins, fsync, size limit, clock skew, log level). See [docs/configuration.md](docs/configuration.md). The on-disk layout is described in [docs/storage-format.md](docs/storage-format.md); do not edit `/data` by hand.

## Gotcha: clock drift

SigV4 rejects requests more than 15 minutes from the server clock with 403 RequestTimeTooSkewed. Docker Desktop's VM clock can drift after the host sleeps. Restart Docker Desktop (or resync the VM clock) and retry.

## Architecture

A single static Go binary (standard library only) in a distroless image: an HTTP server, a path-style router, a SigV4 verifier, and a filesystem store under `/data` with an in-memory key index. Diagrams live in `docs/diagrams/`, and design decisions are recorded as ADRs in [docs/adr](docs/adr/README.md).

![Faux3 architecture](docs/diagrams/architecture.svg)

Every request takes the same path through the binary:

![Faux3 request pipeline](docs/diagrams/request-pipeline.svg)

## Contributing, security, license

- [CONTRIBUTING.md](CONTRIBUTING.md): Docker-only dev setup, tests, roadmap labels, release policy.
- [AGENTS.md](AGENTS.md): rules for coding agents working in this repo.
- [SECURITY.md](SECURITY.md): reporting vulnerabilities; never expose faux3 to untrusted networks.
- [CHANGELOG.md](CHANGELOG.md)
- License: [MIT](LICENSE)
