# Roadmap

Faux3 ships a strict set of ten S3 operations (see [docs/compatibility.md](docs/compatibility.md)). Everything else answers 501 NotImplemented. This file is the source of truth for what may come next.

Rules for every entry:

- Every item requires an ADR before implementation (see [docs/adr](docs/adr/README.md)). Open the ADR first, then the code.
- A new operation needs a router entry, a handler, store support, unit tests, AWS SDK for JavaScript v3 and AWS CLI v2 integration tests, a row in docs/compatibility.md, and a CHANGELOG line.
- Coding agents must not implement roadmap items without an accepted ADR and the maintainer's go-ahead (see [AGENTS.md](AGENTS.md)).
- Roadmap issues carry the `roadmap`, `good first issue`, and `help wanted` labels (see [CONTRIBUTING.md](CONTRIBUTING.md)).

## Next

### CopyObject

Server-side copy from one key (or bucket) to another.

- Unblocks: `aws s3 mv`, S3-to-S3 `aws s3 cp`, SDK copy and rename helpers.
- AWS API: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CopyObject.html
- Requires an ADR before implementation.
- Issue: https://github.com/change-fabric/faux3/issues/3

### ListObjects (V1)

The original marker-based listing API.

- Unblocks: older SDKs and tools that still call V1 listing, such as some backup and static-site tools.
- AWS API: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjects.html
- Requires an ADR before implementation.
- Issue: https://github.com/change-fabric/faux3/issues/4

### GetBucketLocation (real)

Today a minimal stub answers this for CLI region discovery. A real implementation would record and return the bucket's LocationConstraint.

- Unblocks: tools that branch on bucket region, and an honest compatibility row instead of a stub.
- AWS API: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketLocation.html
- Requires an ADR before implementation.
- Issue: https://github.com/change-fabric/faux3/issues/5

### Multipart upload

Six operations as one unit: CreateMultipartUpload, UploadPart, CompleteMultipartUpload, AbortMultipartUpload, ListParts, ListMultipartUploads.

- Unblocks: `aws s3 cp` of files over 8 MB without raising `multipart_threshold`, `@aws-sdk/lib-storage` Upload for large bodies, objects over 5 GiB.
- AWS API: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CreateMultipartUpload.html, https://docs.aws.amazon.com/AmazonS3/latest/API/API_UploadPart.html, https://docs.aws.amazon.com/AmazonS3/latest/API/API_CompleteMultipartUpload.html, https://docs.aws.amazon.com/AmazonS3/latest/API/API_AbortMultipartUpload.html, https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListParts.html, https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListMultipartUploads.html
- Requires an ADR before implementation.
- Issue: https://github.com/change-fabric/faux3/issues/6

## Later

### GetObjectTagging and PutObjectTagging

- Unblocks: apps that tag objects, and `x-amz-tagging` on PutObject (501 today).
- AWS API: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObjectTagging.html, https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObjectTagging.html
- Requires an ADR before implementation.

### GetBucketCors and PutBucketCors

Per-bucket CORS set through the API instead of the global `FAUX3_CORS_ORIGINS`.

- Unblocks: infrastructure scripts that configure CORS on startup.
- AWS API: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketCors.html, https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketCors.html
- Requires an ADR before implementation.

### GetObjectAttributes

- Unblocks: SDK checksum and size lookups without a GET.
- AWS API: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObjectAttributes.html
- Requires an ADR before implementation.

### Virtual-host addressing

`bucket.host` style requests, needing wildcard DNS or a configured base domain.

- Unblocks: clients that cannot force path style.
- AWS API: https://docs.aws.amazon.com/AmazonS3/latest/userguide/VirtualHosting.html
- Requires an ADR before implementation.

### Conditional writes

`If-Match` on PutObject (501 today) and related compare-and-swap writes.

- Unblocks: optimistic concurrency patterns used by lock files and state stores.
- AWS API: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html
- Requires an ADR before implementation.

### Docker Hub mirror

Publish the image to Docker Hub as well as GHCR.

- Unblocks: environments that only pull from Docker Hub.
- AWS API: not applicable.
- Requires an ADR before implementation (it changes ADR 0009).

### More SDK test suites

boto3, AWS SDK for Go v2, and AWS SDK for Java v2 suites in Compose.

- Unblocks: confidence for Python, Go, and Java users.
- AWS API: not applicable.
- Requires an ADR before implementation.

### Versioning

Bucket versioning and version-aware GET, HEAD, DELETE, and list.

- Unblocks: apps that rely on object history or delete markers.
- AWS API: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketVersioning.html, https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectVersions.html
- Requires an ADR before implementation.

### Persistent index or SQLite metadata

Only if rebuilding the in-memory index at startup becomes slow at large key counts.

- Unblocks: fast startup with millions of keys.
- AWS API: not applicable.
- Requires an ADR before implementation (it changes ADR 0002 and ADR 0004).

## Not planned

These are out of scope for a local dev store. A proposal would need an ADR that explains why it belongs here.

- IAM, bucket policies, and ACLs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketPolicy.html, https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketAcl.html. Would unblock policy testing, which belongs against real AWS.
- Replication: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketReplication.html
- Lifecycle rules: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketLifecycleConfiguration.html
- Object Lock: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObjectLockConfiguration.html
- Server-side encryption: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketEncryption.html
- Event notifications: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketNotificationConfiguration.html
- Web UI: not applicable.
- SigV4a: https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv.html

Each of these requires an ADR before implementation.
