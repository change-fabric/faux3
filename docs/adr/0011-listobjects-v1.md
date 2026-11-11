# 0011. ListObjects (V1)

- Status: Proposed
- Diagram: docs/diagrams/request-pipeline.svg

## Context

Faux3 implements ListObjectsV2 but returns 501 NotImplemented for ListObjects V1 (ADR 0003). V1 is the original marker-based listing API. Some older SDKs and tools, such as a few backup and static-site uploaders, still call it, and a `GET` on a bucket with no `list-type` query parameter is a V1 request. ROADMAP.md tracks it and issue #4 asks for it.

V1 and V2 return almost the same listing. They differ in how a caller resumes a truncated response:

- V2 uses `continuation-token` (opaque) and `start-after`, and reports `KeyCount` and `NextContinuationToken`.
- V1 uses `marker` (the caller-visible last key) and reports `NextMarker`. V1 has no `KeyCount`.

The store already lists keys in byte order with prefix, delimiter, and an exclusive resume point (`StartAfter`), so V1 needs no new storage behavior.

## Decision

- Implement ListObjects V1 as a `GET` on a bucket with no `list-type` query parameter. Keep V2 on `list-type=2`.
- A `list-type` other than `2` returns 400 InvalidArgument (`Invalid List Type`), which is what AWS does. This replaces the former 501 for `list-type=1`.
- Accept `prefix`, `delimiter`, `marker`, `max-keys` (capped at 1000), and `encoding-type=url`. `marker` is the exclusive resume point, mapped to the store's `StartAfter`.
- Return a `ListBucketResult` with `Name`, `Prefix`, `Marker`, `MaxKeys`, `Delimiter`, `IsTruncated`, `EncodingType`, `Contents`, and `CommonPrefixes`. Emit `NextMarker` only when a delimiter is set and the response is truncated, matching AWS: without a delimiter the caller resumes from the last returned key.
- Reuse the existing store listing. Expose the raw resume key on the store result so the V1 handler can fill `NextMarker` without decoding the V2 token.

## Consequences

- `aws s3 ls`, `aws s3api list-objects`, and SDK V1 listing work against faux3.
- Keys still sort by byte order, as in ListObjectsV2.
- `GET /<bucket>?list-type=1` now returns 400 instead of 501. That is a behavior change for a request no correct client sends, and it matches AWS.
- The compatibility table grows a row and the 501 list loses ListObjects V1.

## Alternatives

- Keep returning 501 for V1 and tell callers to use V2: breaks tools that only speak V1, which is the whole reason the operation is on the roadmap.
- Share one handler for V1 and V2 with branches on `list-type`: the request and response shapes differ enough (marker vs tokens, no KeyCount) that one handler per version stays clearer.
