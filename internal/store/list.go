package store

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
)

const maxListKeys = 1000

// EncodeToken returns the continuation token for a resume point.
func EncodeToken(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func decodeToken(tok string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil || len(b) == 0 {
		return "", fmt.Errorf("%w: the continuation token provided is incorrect", ErrInvalidArgument)
	}
	return string(b), nil
}

// successor returns the smallest string greater than every string with
// prefix p, and false if none exists (p is all 0xFF bytes).
func successor(p string) (string, bool) {
	b := []byte(p)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 0xFF {
			b[i]++
			return string(b[:i+1]), true
		}
	}
	return "", false
}

// isCommonPrefix reports whether s has the shape of a common prefix:
// prefix + text with no delimiter + delimiter.
func isCommonPrefix(s, prefix, delim string) bool {
	if delim == "" || !strings.HasPrefix(s, prefix) {
		return false
	}
	rest := s[len(prefix):]
	i := strings.Index(rest, delim)
	return i >= 0 && i+len(delim) == len(rest)
}

// startAfter returns the exclusive resume point: the later of StartAfter
// and the decoded continuation token.
func startAfter(p ListParams) (string, error) {
	start := p.StartAfter
	if p.ContinuationToken != "" {
		tok, err := decodeToken(p.ContinuationToken)
		if err != nil {
			return "", err
		}
		start = max(start, tok)
	}
	return start, nil
}

// List implements ListObjectsV2 over the bucket's sorted index using
// plain byte order.
func (s *FS) List(bucketName string, p ListParams) (ListResult, error) {
	if p.MaxKeys < 0 {
		return ListResult{}, fmt.Errorf("%w: max-keys must be non-negative", ErrInvalidArgument)
	}
	if p.MaxKeys > maxListKeys {
		p.MaxKeys = maxListKeys
	}
	start, err := startAfter(p)
	if err != nil {
		return ListResult{}, err
	}
	b, err := s.rlockBucket(bucketName)
	if err != nil {
		return ListResult{}, err
	}
	defer b.mu.RUnlock()
	ix := b.idx
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	var res ListResult
	if p.MaxKeys == 0 {
		return res, nil
	}
	keys := ix.keys
	firstGE := func(s string) int { return sort.SearchStrings(keys, s) }
	i := firstGE(p.Prefix)
	if start != "" {
		j := sort.Search(len(keys), func(n int) bool { return keys[n] > start })
		if isCommonPrefix(start, p.Prefix, p.Delimiter) {
			if succ, ok := successor(start); ok {
				j = max(j, firstGE(succ))
			} else {
				j = len(keys)
			}
		}
		i = max(i, j)
	}
	count := 0
	last := ""
	for i < len(keys) {
		k := keys[i]
		if !strings.HasPrefix(k, p.Prefix) {
			break
		}
		if p.Delimiter != "" {
			rest := k[len(p.Prefix):]
			if d := strings.Index(rest, p.Delimiter); d >= 0 {
				if count == p.MaxKeys {
					res.IsTruncated = true
					break
				}
				cp := p.Prefix + rest[:d+len(p.Delimiter)]
				res.CommonPrefixes = append(res.CommonPrefixes, cp)
				count++
				last = cp
				succ, ok := successor(cp)
				if !ok {
					break
				}
				i = firstGE(succ)
				continue
			}
		}
		if count == p.MaxKeys {
			res.IsTruncated = true
			break
		}
		res.Objects = append(res.Objects, ix.metaLocked(k))
		count++
		last = k
		i++
	}
	if res.IsTruncated {
		res.NextContinuationToken = EncodeToken(last)
		res.NextMarker = last
	}
	return res, nil
}
