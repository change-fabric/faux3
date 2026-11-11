# Limitations

Faux3 is a local development tool. It is not for production, shared environments, or untrusted networks.

## Non-goals

Multipart upload, IAM, ACLs, and bucket policies, versioning, lifecycle rules, object lock, server-side encryption, event notifications, a web UI, distributed or HA operation, virtual-host addressing, SigV4a, and TLS. Adding any of these needs a new ADR first; see [ROADMAP.md](../ROADMAP.md).

## 501 policy

Only ten operations are implemented (see [compatibility.md](compatibility.md)). Any other subresource, any other POST (multipart uploads), ListObjects V1, CopyObject, tagging, non-private canned ACLs, `If-Match` on PUT, and `x-amz-server-side-encryption*` headers return 501 NotImplemented. Faux3 fails loudly instead of silently ignoring a feature your code depends on. GetBucketLocation answers with a stub so the AWS CLI can discover the region; it is not advertised as supported.

## AWS CLI commands that break

- `aws s3 mv` and S3-to-S3 `aws s3 cp`: need CopyObject.
- `aws s3 cp` of files over 8 MB: the CLI switches to multipart. Set `multipart_threshold = 64MB` (or higher) under `s3 =` in the CLI config file.
- `@aws-sdk/lib-storage` Upload of bodies larger than one part: multipart. Use PutObject, or keep bodies under the part size.

## Other differences from S3

- One static credential (`FAUX3_ACCESS_KEY_ID` and `FAUX3_SECRET_ACCESS_KEY`). No users, roles, or session tokens.
- Path-style addressing only. Clients must force path style.
- Plain http only, no TLS.
- Storage class is accepted and ignored; every object reports STANDARD.
- CreateBucket on an existing bucket returns 409 BucketAlreadyOwnedByYou, even in us-east-1.
- Multi-range GET requests are served as 200 with the whole object.
- Requests more than `FAUX3_CLOCK_SKEW` (15 minutes) from the server clock fail with RequestTimeTooSkewed.
- One faux3 process per data directory. The key index lives in memory and is rebuilt at startup, so startup time grows with the number of keys.
- Maximum object size is `FAUX3_MAX_OBJECT_SIZE` (5 GiB by default).
