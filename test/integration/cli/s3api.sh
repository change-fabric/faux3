# Low-level s3api commands against faux3.
. /tests/lib.sh
B=$(new_bucket s3api)
trap 'cleanup_bucket "$B"; rm -rf "$TMP"' EXIT

aws s3api create-bucket --bucket "$B" >/dev/null
aws s3api list-buckets --query 'Buckets[].Name' --output text | tr '\t' '\n' | grep -qx "$B"
aws s3api head-bucket --bucket "$B" >/dev/null

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

# ListObjects V1 (s3api list-objects) returns the same view as V2.
v1keys=$(aws s3api list-objects --bucket "$B" --prefix dir/ --delimiter / --query 'Contents[].Key' --output text)
[ "$v1keys" = "dir/one file" ] || { echo "v1 keys: $v1keys" >&2; exit 1; }
v1prefixes=$(aws s3api list-objects --bucket "$B" --prefix dir/ --delimiter / --query 'CommonPrefixes[].Prefix' --output text)
[ "$v1prefixes" = "dir/sub/" ] || { echo "v1 prefixes: $v1prefixes" >&2; exit 1; }
v1count=$(aws s3api list-objects --bucket "$B" --query 'length(Contents)' --output text)
[ "$v1count" = 3 ] || { echo "v1 count: $v1count" >&2; exit 1; }

for k in "dir/one file" dir/sub/two top; do aws s3api delete-object --bucket "$B" --key "$k"; done
expect_fail "Not Found\|404" aws s3api head-object --bucket "$B" --key top
aws s3api delete-bucket --bucket "$B"
expect_fail "Not Found\|404" aws s3api head-bucket --bucket "$B"
