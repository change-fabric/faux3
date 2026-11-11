# Compatibility

Faux3 implements nine S3 operations over path-style addressing (`http://host:9000/<bucket>/<key>`). Every other operation answers 501 NotImplemented with an S3 error document. Operations that may come later, and what each would unblock, are listed in [ROADMAP.md](../ROADMAP.md).

## Operations

| Operation | AWS reference | Supported request parameters and headers | Known differences |
| --- | --- | --- | --- |
| CreateBucket | [API_CreateBucket](https://docs.aws.amazon.com/AmazonS3/latest/API/API_CreateBucket.html) | Optional CreateBucketConfiguration with LocationConstraint equal to `FAUX3_REGION` | Existing bucket returns 409 BucketAlreadyOwnedByYou (us-east-1 S3 returns 200). Any other LocationConstraint returns 400 InvalidLocationConstraint. Canned ACL `private` is ignored; others return 501. |
| ListBuckets | [API_ListBuckets](https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListBuckets.html) | none | No pagination parameters; all buckets in one response. Owner is a fixed `faux3` identity. |
| HeadBucket | [API_HeadBucket](https://docs.aws.amazon.com/AmazonS3/latest/API/API_HeadBucket.html) | none | Reports `x-amz-bucket-region` as `FAUX3_REGION`. |
| DeleteBucket | [API_DeleteBucket](https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucket.html) | none | Non-empty bucket returns 409 BucketNotEmpty. |
| PutObject | [API_PutObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html) | Content-Length or `x-amz-decoded-content-length`, Content-Type, Content-Encoding, Content-Disposition, Cache-Control, Content-Language, Expires, `x-amz-meta-*`, Content-MD5, `x-amz-checksum-*` (crc32, crc32c, crc64nvme, sha1, sha256), `x-amz-sdk-checksum-algorithm`, `If-None-Match: *`, aws-chunked bodies | Storage class is accepted and ignored; objects report STANDARD. `x-amz-tagging`, `If-Match`, non-private ACLs, and `x-amz-server-side-encryption*` return 501. Max size `FAUX3_MAX_OBJECT_SIZE` (5 GiB). |
| GetObject | [API_GetObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html) | Range (single range), If-Match, If-None-Match, If-Modified-Since, If-Unmodified-Since, `response-*` overrides, `x-amz-checksum-mode: ENABLED` | Multi-range requests are served as 200 with the full body. No partNumber or versionId. |
| HeadObject | [API_HeadObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_HeadObject.html) | Same as GetObject | Errors carry no body, as in S3. |
| DeleteObject | [API_DeleteObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteObject.html) | none | Deleting a missing key returns 204, as in S3. |
| ListObjectsV2 | [API_ListObjectsV2](https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectsV2.html) | prefix, delimiter, max-keys (capped at 1000), continuation-token, start-after, fetch-owner, encoding-type=url | Keys sort by byte order. Continuation tokens are opaque base64url of the last key. |
| GetBucketLocation | [API_GetBucketLocation](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketLocation.html) | `?location` | Returns the region recorded for the bucket when it was created. us-east-1 is reported as an empty LocationConstraint, as in S3. |

## Authentication

Every request is SigV4-signed with the single static credential, except `GET /-/healthz` and CORS preflight (`OPTIONS`, only when `FAUX3_CORS_ORIGINS` is set). There is no bypass. The credential scope region must equal `FAUX3_REGION`, or the request fails with 400 AuthorizationHeaderMalformed.

| Mode | Reference | Supported |
| --- | --- | --- |
| Authorization header | [sig-v4-authenticating-requests](https://docs.aws.amazon.com/AmazonS3/latest/API/sig-v4-authenticating-requests.html) | Yes |
| Presigned URL (query string) | [sigv4-query-string-auth](https://docs.aws.amazon.com/AmazonS3/latest/API/sigv4-query-string-auth.html) | Yes, including query-hoisted checksum parameters |
| Streaming signed chunks (`STREAMING-AWS4-HMAC-SHA256-PAYLOAD`) | [sigv4-streaming](https://docs.aws.amazon.com/AmazonS3/latest/API/sigv4-streaming.html) | Yes |
| Streaming with trailers (signed and unsigned) | [sigv4-streaming-trailers](https://docs.aws.amazon.com/AmazonS3/latest/API/sigv4-streaming-trailers.html) | Yes |
| `UNSIGNED-PAYLOAD` | [sig-v4-authenticating-requests](https://docs.aws.amazon.com/AmazonS3/latest/API/sig-v4-authenticating-requests.html) | Yes |
| SigV4a | | No |

Object integrity follows [checking-object-integrity](https://docs.aws.amazon.com/AmazonS3/latest/userguide/checking-object-integrity.html): Content-MD5 and all `x-amz-checksum-*` algorithms are verified on upload; a mismatch returns 400 BadDigest and the old object is untouched. Stored checksums are returned on GET and HEAD when the request sends `x-amz-checksum-mode: ENABLED`.

## Clients tested

From the wire capture in [notes/wire-capture.md](notes/wire-capture.md) and the Compose integration suite (`scripts/integration.sh`):

| Client | Version | Notes |
| --- | --- | --- |
| AWS SDK for JavaScript v3 (`@aws-sdk/client-s3`, `lib-storage`, `s3-request-presigner`) | 3.1165.0 on Node 22 | Needs `forcePathStyle: true`. Sends CRC32 checksums by default. |
| AWS CLI v2 | 2.39.7 | Needs `addressing_style = path` in a config file. Sends CRC64NVME by default. |

## AWS CLI high-level commands

| Command | Result | Reason and workaround |
| --- | --- | --- |
| `aws s3 mb`, `aws s3 rb` | Works | `rb --force` fails once it needs DeleteObjects; empty the bucket with `s3api delete-object` first. |
| `aws s3 ls` | Works | ListBuckets and ListObjectsV2. |
| `aws s3 cp` local to S3, S3 to local | Works up to `multipart_threshold` | Files over the threshold (8 MB by default) use multipart and fail with 501. Raise `multipart_threshold` in the CLI config (for example `64MB`). |
| `aws s3 cp` S3 to S3 | Breaks | Needs CopyObject. Download and re-upload instead. |
| `aws s3 mv` | Breaks | Needs CopyObject. |
| `aws s3 rm` single key | Works | DeleteObject. |
| `aws s3 rm --recursive` | Breaks | Needs DeleteObjects. Loop over keys with `aws s3api delete-object`. |
| `aws s3 sync` | Works without `--delete` | `--delete` needs DeleteObjects. |
| `aws s3 presign` | Works | Presigned GET. |
| `aws s3api` for the nine operations | Works | |
