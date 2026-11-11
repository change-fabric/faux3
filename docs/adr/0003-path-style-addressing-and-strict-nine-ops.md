# 0003. Path-style addressing and a strict set of nine operations

- Status: Accepted (amended by ADR 0013, which adds DeleteObjects)
- Diagram: docs/diagrams/request-pipeline.svg

## Context

S3 has hundreds of operations. Silently accepting ones faux3 does not really implement would let code pass locally and fail in AWS. Virtual-host addressing needs wildcard DNS, which localhost lacks.

## Decision

- Path-style addressing only.
- Implement exactly nine operations: CreateBucket, ListBuckets, HeadBucket, DeleteBucket, PutObject, GetObject, HeadObject, DeleteObject, ListObjectsV2.
- Everything else, including any unknown subresource and any POST, returns 501 NotImplemented with an S3 error document.
- GetBucketLocation answers with a minimal stub returning the configured region, because the AWS CLI may call it for region discovery. It is documented as a stub, not an advertised operation.

## Consequences

These AWS CLI commands fail: `aws s3 rm --recursive` and `aws s3 sync --delete` (DeleteObjects), `aws s3 mv` and S3-to-S3 `aws s3 cp` (CopyObject), and `aws s3 cp` of files over 8 MB (multipart; raise `multipart_threshold`). They are documented in docs/limitations.md, and the missing operations are tracked in ROADMAP.md. Adding an operation needs a new ADR.

## Alternatives

- Add DeleteObjects, ListObjects V1, and a real GetBucketLocation now: more surface before the core is proven.
- Add CopyObject now: same.
- Accept and ignore unknown operations: hides real incompatibilities.
