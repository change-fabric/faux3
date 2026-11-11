# DeleteObjects: the batch API and the high-level commands that need it.
. /tests/lib.sh
B=$(new_bucket delete)
trap 'cleanup_bucket "$B"; rm -rf "$TMP"' EXIT

aws s3api create-bucket --bucket "$B" >/dev/null
for k in a b c d; do echo x | aws s3 cp - "s3://$B/$k" --only-show-errors; done

# Batch delete by API: a and b, plus a missing key, which still succeeds.
deleted=$(aws s3api delete-objects --bucket "$B" \
  --delete 'Objects=[{Key=a},{Key=b},{Key=gone}],Quiet=false' \
  --query 'Deleted[].Key' --output text | tr '\t' '\n' | sort | tr '\n' ' ')
[ "$deleted" = "a b gone " ] || { echo "deleted: $deleted" >&2; exit 1; }
expect_fail "404\|Not Found" aws s3api head-object --bucket "$B" --key a

# aws s3 rm --recursive uses DeleteObjects to clear what remains (c and d).
aws s3 rm "s3://$B" --recursive --only-show-errors
remaining=$(aws s3api list-objects-v2 --bucket "$B" --query 'Contents[].Key' --output text)
[ -z "$remaining" ] || [ "$remaining" = None ] || { echo "remaining: $remaining" >&2; exit 1; }

aws s3api delete-bucket --bucket "$B"
expect_fail "Not Found\|404" aws s3api head-bucket --bucket "$B"
