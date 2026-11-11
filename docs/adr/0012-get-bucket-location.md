# 0012. GetBucketLocation (real)

- Status: Proposed
- Diagram: docs/diagrams/storage-layout.svg

## Context

Faux3 answers GetBucketLocation with a stub that returns the server's configured region (ADR 0003). The value is read from `FAUX3_REGION`, not from the bucket, so it is documented as a stub rather than a real operation. ROADMAP.md and issue #5 ask for a real implementation that records and returns the bucket's LocationConstraint.

Faux3 accepts exactly one LocationConstraint on CreateBucket: the one equal to `FAUX3_REGION` (ADR 0003). So a bucket's region always equals the server region at the time it was created. The stub returns the right answer today only because that is the single possible value and the config has not changed. It is wrong after `FAUX3_REGION` changes: a bucket created under the old region would report the new one.

## Decision

- Record the region on each bucket when it is created. The store gains a `Region` option (default us-east-1), writes the region into `.bucket.json` alongside the name and creation time, and returns it on `HeadBucket`.
- GetBucketLocation returns the recorded region, not the live config. As in S3, us-east-1 is reported as an empty `LocationConstraint`.
- A bucket written before this field existed (`.bucket.json` with no `region`) reports the store's configured region on load, so existing data keeps working.
- The response wire format does not change: a `LocationConstraint` document in the S3 namespace. Only the source of the value changes, from config to bucket metadata.

## Consequences

- GetBucketLocation is an honest operation instead of a stub. The compatibility row drops the stub note.
- A bucket keeps reporting the region it was created in even after `FAUX3_REGION` changes. Requests to that bucket still authenticate against the current server region (ADR 0006), so a changed region is already a disruptive operation; the recorded location just stops lying about it.
- `.bucket.json` grows one field. The format version (ADR 0004) does not change, because old files load with a sensible default and new files stay valid JSON.

## Alternatives

- Keep the config-backed stub: returns a plausible value but cannot be honest once the region changes, and leaves an operation documented as not real.
- Store the region in a separate sidecar file: more files and renames for one short string that belongs with the rest of the bucket metadata.
