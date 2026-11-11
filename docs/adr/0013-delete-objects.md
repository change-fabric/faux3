# 0013. DeleteObjects

- Status: Proposed
- Diagram: docs/diagrams/request-pipeline.svg

## Context

Faux3 returns 501 NotImplemented for every POST, including DeleteObjects (ADR 0003). DeleteObjects is the batch delete of up to 1000 keys in one `POST /<bucket>?delete` with an XML body. Without it, `aws s3 rm --recursive`, `aws s3 sync --delete`, and SDK bulk cleanup in test teardown all fail. ROADMAP.md and issue #2 track it.

It is the first supported POST, so the router has to let a POST through the pipeline instead of rejecting it up front, while still returning 501 for the POSTs faux3 does not implement (multipart uploads).

The store already has a single-key DeleteObject that treats a missing key as success, which is exactly the per-key semantics DeleteObjects needs.

## Decision

- Route `POST /<bucket>?delete` to a new handler. Every other POST, and `?delete` on any other method or on an object key, still returns 501 NotImplemented. The `?delete` subresource is allowed only for this one case.
- Parse the `Delete` document: an optional `Quiet` flag and 1 to 1000 `Object` entries, each with a `Key` and an ignored `VersionId` (faux3 has no versioning). The body is read with a 2 MiB cap and its SigV4 payload is verified, the same way CreateBucket reads its configuration document.
- Delete each key with the existing store DeleteObject. A missing key is a success, as in S3 and as in single DeleteObject.
- Return 200 with a `DeleteResult`: a `Deleted` entry per key, or in quiet mode only `Error` entries. A key that fails becomes an `Error` with its code and message instead of stopping the batch.
- A missing bucket fails the whole request with 404 NoSuchBucket, matching S3. A malformed body, an empty `Delete`, or more than 1000 keys returns 400 MalformedXML.

## Consequences

- `aws s3 rm --recursive`, `aws s3 sync --delete`, `rb --force`, and SDK batch delete work against faux3.
- The router now accepts POST into the pipeline. The 501 for unimplemented POSTs moves from the method gate to the routing step, so multipart POSTs still answer 501.
- No new storage behavior: DeleteObjects loops over DeleteObject. Deletes are not transactional across the batch, which matches S3, where each key succeeds or fails on its own.
- Content-MD5 is not required. S3 asks for it, but faux3 already authenticates the body through SigV4, as it does for CreateBucket, so a separate digest header is not needed.

## Alternatives

- Keep returning 501 and tell users to loop over single DeleteObject: that is the current workaround, and it is slow and breaks the common `aws s3 rm --recursive` and `sync --delete` paths.
- Require Content-MD5 to mirror S3 exactly: adds a second integrity check on top of SigV4 for no real safety on a local store, and the SDKs that send it still work without the server enforcing it.
