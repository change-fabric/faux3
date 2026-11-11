# Low-level s3api commands against faux3.
. /tests/lib.sh
B=$(new_bucket s3api)
trap 'cleanup_bucket "$B"; rm -rf "$TMP"' EXIT

aws s3api create-bucket --bucket "$B" >/dev/null
aws s3api list-buckets --query 'Buckets[].Name' --output text | tr '\t' '\n' | grep -qx "$B"
aws s3api head-bucket --bucket "$B" >/dev/null

# GetBucketLocation returns the recorded region. us-east-1 (the test
# region) is reported as the empty constraint, which the CLI prints as None.
loc=$(aws s3api get-bucket-location --bucket "$B" --query 'LocationConstraint' --output text)
[ "$loc" = None ] || [ -z "$loc" ] || { echo "location: $loc" >&2; exit 1; }

head -c 100000 /dev/urandom >"$TMP/body"
etag=$(aws s3api put-object --bucket "$B" --key "dir/one file" --body "$TMP/body" --content-type application/x-test \
  --metadata color=blue --query ETag --output text)
want=\"$(md5sum <"$TMP/body" | cut -d' ' -f1)\"
[ "$etag" = "$want" ] || { echo "etag $etag want $want" >&2; exit 1; }
aws s3api put-object --bucket "$B" --key "dir/sub/two" --body "$TMP/body" >/dev/null
aws s3api put-object --bucket "$B" --key "top" --body "$TMP/body" >/dev/null

aws s3api get-object --bucket "$B" --key "dir/one file" "$TMP/out" >"$TMP/get.json"
same_file "$TMP/body" "$TMP/out"
grep -q '"ContentType": "application/x-test"' "$TMP/get.json"

head_json=$(aws s3api head-object --bucket "$B" --key "dir/one file")
grep -q '"ContentLength": 100000' <<<"$head_json"
grep -q '"color": "blue"' <<<"$head_json"

keys=$(aws s3api list-objects-v2 --bucket "$B" --prefix dir/ --delimiter / --query 'Contents[].Key' --output text)
[ "$keys" = "dir/one file" ] || { echo "keys: $keys" >&2; exit 1; }
prefixes=$(aws s3api list-objects-v2 --bucket "$B" --prefix dir/ --delimiter / --query 'CommonPrefixes[].Prefix' --output text)
[ "$prefixes" = "dir/sub/" ] || { echo "prefixes: $prefixes" >&2; exit 1; }
count=$(aws s3api list-objects-v2 --bucket "$B" --query 'length(Contents)' --output text)
[ "$count" = 3 ] || { echo "count: $count" >&2; exit 1; }

for k in "dir/one file" dir/sub/two top; do aws s3api delete-object --bucket "$B" --key "$k"; done
expect_fail "Not Found\|404" aws s3api head-object --bucket "$B" --key top
aws s3api delete-bucket --bucket "$B"
expect_fail "Not Found\|404" aws s3api head-bucket --bucket "$B"
