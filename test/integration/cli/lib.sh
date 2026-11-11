# Helpers sourced by the CLI test scripts. Not a test itself.

# new_bucket prints a fresh, unique bucket name with the given prefix.
new_bucket() {
  printf '%s-%s\n' "$1" "$(head -c 4 /dev/urandom | od -An -tx1 | tr -d ' \n')"
}

# same_file fails unless both files have identical sha256 digests.
same_file() {
  local a b
  a=$(sha256sum <"$1" | cut -d' ' -f1)
  b=$(sha256sum <"$2" | cut -d' ' -f1)
  if [ "$a" != "$b" ]; then
    echo "content differs: $1 ($a) vs $2 ($b)" >&2
    return 1
  fi
}

# expect_fail runs a command that must fail with output matching a pattern.
expect_fail() {
  local pattern=$1 out
  shift
  if out=$("$@" 2>&1); then
    echo "expected failure but succeeded: $*" >&2
    return 1
  fi
  if ! grep -q -- "$pattern" <<<"$out"; then
    echo "expected '$pattern' in output of $*:" >&2
    echo "$out" >&2
    return 1
  fi
}

# cleanup_bucket empties and removes a bucket, ignoring errors. It deletes
# keys one at a time to stay simple; delete.sh exercises batch DeleteObjects.
# It runs from EXIT traps, so it turns off errexit to keep the script's own
# exit status.
cleanup_bucket() {
  set +e +o pipefail
  local b=$1 k
  aws s3api list-objects-v2 --bucket "$b" --query 'Contents[].Key' --output text 2>/dev/null |
    tr '\t' '\n' | while IFS= read -r k; do
      [ -n "$k" ] && [ "$k" != None ] && aws s3api delete-object --bucket "$b" --key "$k" >/dev/null 2>&1
    done
  aws s3api delete-bucket --bucket "$b" >/dev/null 2>&1 || true
}

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
