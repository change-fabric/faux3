package s3api

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/change-fabric/faux3/internal/checksum"
	"github.com/change-fabric/faux3/internal/sigv4"
	"github.com/change-fabric/faux3/internal/store"
)

var testNow = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

var signer = sigv4.Signer{AccessKeyID: "faux3", SecretAccessKey: "faux3", Region: "us-east-1"}

type harness struct {
	t   *testing.T
	srv *Server
}

func newHarness(t *testing.T, cors ...string) *harness {
	t.Helper()
	st, err := store.Open(store.Options{Dir: t.TempDir(), MaxObjectSize: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, srv: &Server{
		Store: st,
		Verifier: &sigv4.Verifier{AccessKeyID: "faux3", SecretAccessKey: "faux3", Region: "us-east-1",
			ClockSkew: 15 * time.Minute, Now: func() time.Time { return testNow }},
		Region:      "us-east-1",
		CORSOrigins: cors,
	}}
}

func hashHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// newReq builds an unsigned request; target is the raw path and query.
func newReq(method, target string, body []byte, hdr ...string) *http.Request {
	r := httptest.NewRequest(method, "http://localhost:9000"+target, bytes.NewReader(body))
	if body == nil {
		r.Body = http.NoBody
		r.ContentLength = 0
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	return r
}

func (h *harness) serve(r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, r)
	return rec
}

// do signs a request with header auth over the sha256 of body.
func (h *harness) do(method, target string, body []byte, hdr ...string) *httptest.ResponseRecorder {
	r := newReq(method, target, body, hdr...)
	signer.SignHeader(r, testNow, hashHex(body))
	return h.serve(r)
}

func (h *harness) expect(rec *httptest.ResponseRecorder, status int, code string) {
	h.t.Helper()
	if rec.Code != status {
		h.t.Fatalf("status %d, want %d; body %s", rec.Code, status, rec.Body)
	}
	if code != "" && !strings.Contains(rec.Body.String(), "<Code>"+code+"</Code>") {
		h.t.Fatalf("body missing code %s: %s", code, rec.Body)
	}
	if rec.Header().Get("x-amz-request-id") == "" || rec.Header().Get("x-amz-id-2") == "" {
		h.t.Fatalf("missing request id headers")
	}
}

func TestHealthUnauthenticated(t *testing.T) {
	h := newHarness(t)
	rec := h.serve(newReq("GET", "/-/healthz", nil))
	if rec.Code != 200 || rec.Body.String() != "ok" {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
}

func TestBucketLifecycle(t *testing.T) {
	h := newHarness(t)
	h.expect(h.do("PUT", "/alpha", nil), 200, "")
	h.expect(h.do("PUT", "/beta", []byte(`<CreateBucketConfiguration><LocationConstraint>us-east-1</LocationConstraint></CreateBucketConfiguration>`)), 200, "")
	h.expect(h.do("PUT", "/alpha", nil), 409, "BucketAlreadyOwnedByYou")
	h.expect(h.do("PUT", "/Bad_Name", nil), 400, "InvalidBucketName")
	h.expect(h.do("PUT", "/gamma", []byte(`<CreateBucketConfiguration><LocationConstraint>eu-west-1</LocationConstraint></CreateBucketConfiguration>`)), 400, "InvalidLocationConstraint")
	h.expect(h.do("PUT", "/gamma", []byte(`<not xml`)), 400, "MalformedXML")

	rec := h.do("GET", "/", nil)
	h.expect(rec, 200, "")
	b := rec.Body.String()
	if !strings.Contains(b, `<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`) ||
		strings.Index(b, "<Name>alpha</Name>") > strings.Index(b, "<Name>beta</Name>") ||
		!strings.Contains(b, "<DisplayName>faux3</DisplayName>") || !strings.Contains(b, "Z</CreationDate>") {
		t.Fatalf("list buckets: %s", b)
	}

	rec = h.do("HEAD", "/alpha", nil)
	h.expect(rec, 200, "")
	if rec.Header().Get("x-amz-bucket-region") != "us-east-1" {
		t.Fatal("missing x-amz-bucket-region")
	}
	rec = h.do("HEAD", "/missing", nil)
	h.expect(rec, 404, "")
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD error has body: %s", rec.Body)
	}

	rec = h.do("GET", "/alpha?location", nil)
	h.expect(rec, 200, "")
	if !strings.Contains(rec.Body.String(), `<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></LocationConstraint>`) {
		t.Fatalf("location: %s", rec.Body)
	}
	h.expect(h.do("GET", "/missing?location", nil), 404, "NoSuchBucket")

	h.expect(h.do("PUT", "/alpha/k", []byte("x")), 200, "")
	h.expect(h.do("DELETE", "/alpha", nil), 409, "BucketNotEmpty")
	h.expect(h.do("DELETE", "/alpha/k", nil), 204, "")
	h.expect(h.do("DELETE", "/alpha", nil), 204, "")
	h.expect(h.do("DELETE", "/alpha", nil), 404, "NoSuchBucket")
	h.expect(h.do("GET", "/alpha?list-type=2", nil), 404, "NoSuchBucket")
}

func TestObjectRoundTrip(t *testing.T) {
	h := newHarness(t)
	h.expect(h.do("PUT", "/bkt", nil), 200, "")
	body := make([]byte, 256)
	for i := range body {
		body[i] = byte(i)
	}
	md := md5.Sum(body)
	rec := h.do("PUT", "/bkt/dir/obj.bin", body,
		"Content-Type", "application/x-test", "Cache-Control", "no-cache", "Content-Disposition", "attachment",
		"Content-Language", "en", "Expires", "Thu, 01 Jan 2099 00:00:00 GMT", "X-Amz-Meta-Color", "blue",
		"Content-Md5", base64.StdEncoding.EncodeToString(md[:]), "X-Amz-Storage-Class", "GLACIER", "X-Amz-Acl", "private")
	h.expect(rec, 200, "")
	etag := `"` + hex.EncodeToString(md[:]) + `"`
	if rec.Header().Get("ETag") != etag {
		t.Fatalf("etag %q want %q", rec.Header().Get("ETag"), etag)
	}

	rec = h.do("GET", "/bkt/dir/obj.bin", nil)
	h.expect(rec, 200, "")
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Fatal("body mismatch")
	}
	want := map[string]string{"Content-Type": "application/x-test", "Cache-Control": "no-cache",
		"Content-Disposition": "attachment", "Content-Language": "en",
		"ETag": etag, "Accept-Ranges": "bytes", "Content-Length": "256"}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q want %q", k, got, v)
		}
	}
	// Regression: user metadata header names go out lowercase, as S3 sends
	// them; the AWS CLI otherwise reports the key as "Color".
	//lint:ignore SA1008 the test asserts the raw lowercase key the server writes
	if got := rec.Header()["x-amz-meta-color"]; len(got) != 1 || got[0] != "blue" {
		t.Errorf("x-amz-meta-color = %q, header map %v", got, rec.Header())
	}
	if _, ok := rec.Header()["X-Amz-Meta-Color"]; ok {
		t.Error("user metadata sent with a canonicalized header name")
	}
	if _, err := http.ParseTime(rec.Header().Get("Last-Modified")); err != nil {
		t.Errorf("Last-Modified: %v", err)
	}

	rec = h.do("HEAD", "/bkt/dir/obj.bin", nil)
	h.expect(rec, 200, "")
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "256" || rec.Header().Get("ETag") != etag {
		t.Fatalf("head: %v %q", rec.Header(), rec.Body)
	}

	rec = h.do("GET", "/bkt/dir/obj.bin?response-content-type=text%2Fplain&response-cache-control=max-age%3D1&x-id=GetObject", nil)
	h.expect(rec, 200, "")
	if rec.Header().Get("Content-Type") != "text/plain" || rec.Header().Get("Cache-Control") != "max-age=1" {
		t.Fatalf("overrides: %v", rec.Header())
	}

	h.expect(h.do("DELETE", "/bkt/dir/obj.bin", nil), 204, "")
	h.expect(h.do("DELETE", "/bkt/dir/obj.bin", nil), 204, "")
	h.expect(h.do("GET", "/bkt/dir/obj.bin", nil), 404, "NoSuchKey")
	rec = h.do("HEAD", "/bkt/dir/obj.bin", nil)
	h.expect(rec, 404, "")
	if rec.Body.Len() != 0 {
		t.Fatal("HEAD 404 has a body")
	}
	h.expect(h.do("DELETE", "/nope/k", nil), 404, "NoSuchBucket")
	h.expect(h.do("PUT", "/nope/k", []byte("x")), 404, "NoSuchBucket")
}

func TestPutErrors(t *testing.T) {
	h := newHarness(t)
	h.expect(h.do("PUT", "/bkt", nil), 200, "")

	// Payload hash mismatch.
	r := newReq("PUT", "/bkt/k", []byte("hello"))
	signer.SignHeader(r, testNow, hashHex([]byte("other")))
	h.expect(h.serve(r), 400, "XAmzContentSHA256Mismatch")

	h.expect(h.do("PUT", "/bkt/k", []byte("hello"), "Content-Md5", base64.StdEncoding.EncodeToString(make([]byte, 16))), 400, "BadDigest")
	h.expect(h.do("PUT", "/bkt/k", []byte("hello"), "Content-Md5", "nope"), 400, "InvalidDigest")
	h.expect(h.do("PUT", "/bkt/k", []byte("hello"), "X-Amz-Checksum-Crc32", "AAAAAA=="), 400, "BadDigest")
	h.expect(h.do("PUT", "/bkt/k", []byte("hello"), "X-Amz-Checksum-Crc32", "AAAAAA==", "X-Amz-Checksum-Sha1", "AAAA"), 400, "InvalidRequest")
	h.expect(h.do("PUT", "/bkt/"+strings.Repeat("k", 1025), []byte("x")), 400, "KeyTooLongError")
	h.expect(h.do("PUT", "/bkt/%FF", []byte("x")), 400, "InvalidArgument")
	h.expect(h.do("PUT", "/bkt/k", []byte("x"), "X-Amz-Meta-Big", strings.Repeat("v", 2100)), 400, "MetadataTooLarge")
	h.expect(h.do("PUT", "/bkt/big", make([]byte, 1<<20+1)), 400, "EntityTooLarge")

	// Missing Content-Length.
	r = newReq("PUT", "/bkt/k", []byte("abc"))
	r.ContentLength = -1
	signer.SignHeader(r, testNow, sigv4.UnsignedPayload)
	h.expect(h.serve(r), 411, "MissingContentLength")

	// Body shorter than Content-Length.
	r = newReq("PUT", "/bkt/k", []byte("abc"))
	r.ContentLength = 10
	signer.SignHeader(r, testNow, sigv4.UnsignedPayload)
	h.expect(h.serve(r), 400, "IncompleteBody")

	// A failed put leaves the old object intact.
	h.expect(h.do("PUT", "/bkt/keep", []byte("old")), 200, "")
	h.expect(h.do("PUT", "/bkt/keep", []byte("new"), "X-Amz-Checksum-Crc32", "AAAAAA=="), 400, "BadDigest")
	if rec := h.do("GET", "/bkt/keep", nil); rec.Body.String() != "old" {
		t.Fatalf("old object lost: %q", rec.Body)
	}

	// If-None-Match: * conditional put.
	h.expect(h.do("PUT", "/bkt/once", []byte("1"), "If-None-Match", "*"), 200, "")
	h.expect(h.do("PUT", "/bkt/once", []byte("2"), "If-None-Match", "*"), 412, "PreconditionFailed")
}

func TestChecksums(t *testing.T) {
	h := newHarness(t)
	h.expect(h.do("PUT", "/bkt", nil), 200, "")
	body := []byte("123456789")
	for _, a := range checksum.Algorithms {
		key := "/bkt/" + string(a)
		sum := checksum.Sum(a, body)
		rec := h.do("PUT", key, body, checksum.HeaderName(a), sum)
		h.expect(rec, 200, "")
		if rec.Header().Get(checksum.HeaderName(a)) != sum {
			t.Fatalf("%s: put did not echo checksum", a)
		}
		rec = h.do("GET", key, nil, "X-Amz-Checksum-Mode", "ENABLED")
		if rec.Header().Get(checksum.HeaderName(a)) != sum || rec.Header().Get("x-amz-checksum-type") != "FULL_OBJECT" {
			t.Fatalf("%s: get headers %v", a, rec.Header())
		}
		if rec = h.do("GET", key, nil); rec.Header().Get(checksum.HeaderName(a)) != "" {
			t.Fatalf("%s: checksum returned without checksum mode", a)
		}
		rec = h.do("GET", key, nil, "X-Amz-Checksum-Mode", "ENABLED", "Range", "bytes=0-1")
		if rec.Code != 206 || rec.Header().Get(checksum.HeaderName(a)) != "" {
			t.Fatalf("%s: ranged get %d %v", a, rec.Code, rec.Header())
		}
	}
	// SDK-requested algorithm without a value is computed and stored.
	h.expect(h.do("PUT", "/bkt/sdk", body, "X-Amz-Sdk-Checksum-Algorithm", "CRC32C"), 200, "")
	rec := h.do("HEAD", "/bkt/sdk", nil, "X-Amz-Checksum-Mode", "ENABLED")
	if rec.Header().Get("x-amz-checksum-crc32c") != checksum.Sum(checksum.CRC32C, body) {
		t.Fatalf("sdk alg: %v", rec.Header())
	}
}

func TestRangesAndConditionals(t *testing.T) {
	h := newHarness(t)
	h.expect(h.do("PUT", "/bkt", nil), 200, "")
	rec := h.do("PUT", "/bkt/k", []byte("0123456789"))
	etag := rec.Header().Get("ETag")

	cases := []struct {
		hdr    []string
		status int
		body   string
		code   string
	}{
		{[]string{"Range", "bytes=2-4"}, 206, "234", ""},
		{[]string{"Range", "bytes=-3"}, 206, "789", ""},
		{[]string{"Range", "bytes=7-"}, 206, "789", ""},
		{[]string{"Range", "bytes=5-100"}, 206, "56789", ""},
		{[]string{"Range", "bytes=10-"}, 416, "", "InvalidRange"},
		{[]string{"Range", "bytes=-0"}, 416, "", "InvalidRange"},
		{[]string{"Range", "bytes=0-1,4-5"}, 200, "0123456789", ""},
		{[]string{"Range", "items=1-2"}, 200, "0123456789", ""},
		{[]string{"If-None-Match", etag}, 304, "", ""},
		{[]string{"If-Match", etag}, 200, "0123456789", ""},
		{[]string{"If-Match", `"nope"`}, 412, "", "PreconditionFailed"},
		{[]string{"If-Unmodified-Since", "Mon, 01 Jan 2001 00:00:00 GMT"}, 412, "", "PreconditionFailed"},
		{[]string{"If-Modified-Since", "Mon, 01 Jan 2099 00:00:00 GMT"}, 304, "", ""},
	}
	for _, c := range cases {
		rec := h.do("GET", "/bkt/k", nil, c.hdr...)
		if rec.Code != c.status || (c.body != "" && rec.Body.String() != c.body) ||
			(c.code != "" && !strings.Contains(rec.Body.String(), c.code)) {
			t.Errorf("%v: %d %q", c.hdr, rec.Code, rec.Body)
		}
	}
	rec = h.do("HEAD", "/bkt/k", nil, "If-Match", `"nope"`)
	if rec.Code != 412 || rec.Body.Len() != 0 {
		t.Fatalf("HEAD 412: %d %q", rec.Code, rec.Body)
	}
	h.expect(h.do("PUT", "/bkt/empty", []byte{}), 200, "")
	h.expect(h.do("GET", "/bkt/empty", nil, "Range", "bytes=0-0"), 416, "InvalidRange")
	if rec := h.do("GET", "/bkt/empty", nil); rec.Code != 200 || rec.Body.Len() != 0 {
		t.Fatalf("empty get: %d", rec.Code)
	}
}

func TestPathEdgeCases(t *testing.T) {
	h := newHarness(t)
	h.expect(h.do("PUT", "/bkt", nil), 200, "")
	keys := map[string]string{
		"/bkt/a/../b":       "a/../b",
		"/bkt/a//b":         "a//b",
		"/bkt//leading":     "/leading",
		"/bkt/a%2Fb":        "a/b",
		"/bkt/sp%20ace":     "sp ace",
		"/bkt/plus+sign":    "plus+sign",
		"/bkt/pct%2525":     "pct%25",
		"/bkt/trailing/":    "trailing/",
		"/bkt/caf%C3%A9":    "caf\u00e9",
		"/bkt/..":           "..",
		"/bkt/%F0%9F%98%80": "\U0001F600",
	}
	for path, key := range keys {
		h.expect(h.do("PUT", path, []byte(key)), 200, "")
	}
	rec := h.do("GET", "/bkt?list-type=2", nil)
	for _, key := range keys {
		if !strings.Contains(rec.Body.String(), "<Key>"+key+"</Key>") {
			t.Errorf("listing missing %q: %s", key, rec.Body)
		}
	}
	for path, key := range keys {
		if rec := h.do("GET", path, nil); rec.Code != 200 || rec.Body.String() != key {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body)
		}
	}
	// a/b and a%2Fb name the same key.
	if rec := h.do("GET", "/bkt/a/b", nil); rec.Body.String() != "a/b" {
		t.Errorf("a/b: %q", rec.Body)
	}
}

func TestGetBucketLocationRegion(t *testing.T) {
	st, err := store.Open(store.Options{Dir: t.TempDir(), MaxObjectSize: 1 << 20, Region: "eu-west-1"})
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		Store: st,
		Verifier: &sigv4.Verifier{AccessKeyID: "faux3", SecretAccessKey: "faux3", Region: "eu-west-1",
			ClockSkew: 15 * time.Minute, Now: func() time.Time { return testNow }},
		Region: "eu-west-1",
	}
	eu := sigv4.Signer{AccessKeyID: "faux3", SecretAccessKey: "faux3", Region: "eu-west-1"}
	do := func(method, target string, body []byte) *httptest.ResponseRecorder {
		r := newReq(method, target, body)
		eu.SignHeader(r, testNow, hashHex(body))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, r)
		return rec
	}
	if rec := do("PUT", "/eu-bucket", nil); rec.Code != 200 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	rec := do("GET", "/eu-bucket?location", nil)
	if rec.Code != 200 ||
		!strings.Contains(rec.Body.String(), `<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">eu-west-1</LocationConstraint>`) {
		t.Fatalf("location: %d %s", rec.Code, rec.Body)
	}
}

func TestListObjectsV2(t *testing.T) {
	h := newHarness(t)
	h.expect(h.do("PUT", "/bkt", nil), 200, "")
	for _, k := range []string{"a", "dir/x", "dir/y", "dir/sub/z", "e f", "z"} {
		h.expect(h.do("PUT", "/bkt/"+strings.ReplaceAll(k, " ", "%20"), []byte("v")), 200, "")
	}
	rec := h.do("GET", "/bkt?list-type=2&delimiter=%2F&max-keys=2", nil)
	b := rec.Body.String()
	if !strings.Contains(b, "<KeyCount>2</KeyCount>") || !strings.Contains(b, "<IsTruncated>true</IsTruncated>") ||
		!strings.Contains(b, "<Prefix>dir/</Prefix>") || !strings.Contains(b, "<StorageClass>STANDARD</StorageClass>") {
		t.Fatalf("page 1: %s", b)
	}
	i := strings.Index(b, "<NextContinuationToken>")
	tok := b[i+len("<NextContinuationToken>") : strings.Index(b, "</NextContinuationToken>")]
	rec = h.do("GET", "/bkt?list-type=2&delimiter=%2F&continuation-token="+tok, nil)
	b = rec.Body.String()
	if !strings.Contains(b, "<Key>e f</Key>") || !strings.Contains(b, "<Key>z</Key>") || strings.Contains(b, "dir/") ||
		!strings.Contains(b, "<ContinuationToken>"+tok+"</ContinuationToken>") {
		t.Fatalf("page 2: %s", b)
	}

	rec = h.do("GET", "/bkt?list-type=2&prefix=dir%2F&encoding-type=url&fetch-owner=true&start-after=dir%2Fsub%2Fz", nil)
	b = rec.Body.String()
	if !strings.Contains(b, "<Key>dir/x</Key>") || strings.Contains(b, "dir/sub/z</Key>") ||
		!strings.Contains(b, "<EncodingType>url</EncodingType>") || !strings.Contains(b, "<Owner><ID>") {
		t.Fatalf("prefix: %s", b)
	}
	rec = h.do("GET", "/bkt?list-type=2&encoding-type=url&prefix=e", nil)
	if !strings.Contains(rec.Body.String(), "<Key>e+f</Key>") {
		t.Fatalf("url encoding: %s", rec.Body)
	}
	rec = h.do("GET", "/bkt?list-type=2&max-keys=0", nil)
	if !strings.Contains(rec.Body.String(), "<KeyCount>0</KeyCount>") || !strings.Contains(rec.Body.String(), "<IsTruncated>false</IsTruncated>") {
		t.Fatalf("max-keys 0: %s", rec.Body)
	}
	rec = h.do("GET", "/bkt?list-type=2&max-keys=5000", nil)
	if !strings.Contains(rec.Body.String(), "<MaxKeys>1000</MaxKeys>") {
		t.Fatalf("max-keys cap: %s", rec.Body)
	}
	h.expect(h.do("GET", "/bkt?list-type=2&max-keys=-1", nil), 400, "InvalidArgument")
	h.expect(h.do("GET", "/bkt?list-type=2&max-keys=x", nil), 400, "InvalidArgument")
	h.expect(h.do("GET", "/bkt?list-type=2&encoding-type=bogus", nil), 400, "InvalidArgument")
	h.expect(h.do("GET", "/bkt?list-type=2&continuation-token=%21%21", nil), 400, "InvalidArgument")
}

func TestNotImplementedMatrix(t *testing.T) {
	h := newHarness(t)
	h.expect(h.do("PUT", "/bkt", nil), 200, "")
	h.expect(h.do("PUT", "/bkt/k", []byte("x")), 200, "")
	subresources := []string{"acl", "versioning", "tagging", "uploads", "delete", "policy", "cors", "lifecycle",
		"website", "logging", "notification", "replication", "encryption", "object-lock", "retention",
		"legal-hold", "torrent", "versions", "uploadId=1", "partNumber=1", "attributes", "restore", "select",
		"accelerate", "analytics", "inventory", "metrics", "ownershipControls", "publicAccessBlock",
		"requestPayment", "intelligent-tiering", "versionId=1"}
	for _, sub := range subresources {
		for _, target := range []string{"/bkt?" + sub, "/bkt/k?" + sub} {
			for _, m := range []string{"GET", "PUT", "DELETE"} {
				rec := h.do(m, target, nil)
				if rec.Code != 501 || !strings.Contains(rec.Body.String(), "NotImplemented") {
					t.Errorf("%s %s: %d", m, target, rec.Code)
				}
			}
		}
	}
	for _, hdr := range [][]string{
		{"X-Amz-Copy-Source", "/bkt/k"},
		{"X-Amz-Server-Side-Encryption", "AES256"},
		{"X-Amz-Server-Side-Encryption-Customer-Algorithm", "AES256"},
		{"X-Amz-Tagging", "a=b"},
		{"X-Amz-Acl", "public-read"},
		{"If-Match", `"abc"`},
		{"If-None-Match", `"abc"`},
	} {
		h.expect(h.do("PUT", "/bkt/k2", []byte("x"), hdr...), 501, "NotImplemented")
	}
	h.expect(h.do("POST", "/bkt?delete", nil), 501, "NotImplemented")
	h.expect(h.do("POST", "/bkt/k?uploads", nil), 501, "NotImplemented")
	h.expect(h.do("GET", "/bkt", nil), 501, "NotImplemented")
	h.expect(h.do("GET", "/bkt?list-type=1", nil), 501, "NotImplemented")
	h.expect(h.do("GET", "/bkt?response-content-type=x", nil), 501, "NotImplemented")
	h.expect(h.do("PATCH", "/bkt/k", nil), 405, "MethodNotAllowed")
	h.expect(h.do("PUT", "/", nil), 405, "MethodNotAllowed")

	// Unauthenticated probes get 403, never 501.
	h.expect(h.serve(newReq("GET", "/bkt?acl", nil)), 403, "AccessDenied")
}

func TestAuthErrors(t *testing.T) {
	h := newHarness(t)
	h.expect(h.serve(newReq("GET", "/", nil)), 403, "AccessDenied")

	r := newReq("GET", "/", nil)
	bad := signer
	bad.SecretAccessKey = "wrong"
	bad.SignHeader(r, testNow, sigv4.EmptySHA256)
	rec := h.serve(r)
	h.expect(rec, 403, "SignatureDoesNotMatch")
	if !strings.Contains(rec.Body.String(), "<CanonicalRequest>") {
		t.Fatal("missing CanonicalRequest debug element")
	}

	r = newReq("GET", "/", nil)
	bad = signer
	bad.AccessKeyID = "someone"
	bad.SignHeader(r, testNow, sigv4.EmptySHA256)
	h.expect(h.serve(r), 403, "InvalidAccessKeyId")

	r = newReq("GET", "/", nil)
	bad = signer
	bad.Region = "eu-west-1"
	bad.SignHeader(r, testNow, sigv4.EmptySHA256)
	h.expect(h.serve(r), 400, "AuthorizationHeaderMalformed")

	r = newReq("GET", "/", nil)
	signer.SignHeader(r, testNow.Add(-time.Hour), sigv4.EmptySHA256)
	h.expect(h.serve(r), 403, "RequestTimeTooSkewed")

	r = newReq("GET", "/", nil, "X-Amz-Meta-A", "1")
	signer.SignHeader(r, testNow, sigv4.EmptySHA256)
	r.Header.Set("X-Amz-Meta-A", "tampered")
	h.expect(h.serve(r), 403, "SignatureDoesNotMatch")

	r = newReq("GET", "/", nil)
	r.Header.Set("Authorization", "AWS4-HMAC-SHA256 garbage")
	h.expect(h.serve(r), 400, "AuthorizationHeaderMalformed")

	r = newReq("GET", "/?X-Amz-Algorithm=AWS4-HMAC-SHA256", nil)
	h.expect(h.serve(r), 400, "AuthorizationQueryParametersError")
}

func TestPresigned(t *testing.T) {
	h := newHarness(t)
	h.expect(h.do("PUT", "/bkt", nil), 200, "")

	r := newReq("PUT", "/bkt/p", []byte("presigned"))
	signer.Presign(r, testNow, time.Hour)
	h.expect(h.serve(r), 200, "")

	r = newReq("GET", "/bkt/p?response-content-disposition=inline", nil)
	signer.Presign(r, testNow, time.Hour)
	rec := h.serve(r)
	h.expect(rec, 200, "")
	if rec.Body.String() != "presigned" || rec.Header().Get("Content-Disposition") != "inline" {
		t.Fatalf("presigned get: %q %v", rec.Body, rec.Header())
	}

	r = newReq("GET", "/bkt/p", nil)
	signer.Presign(r, testNow.Add(-2*time.Hour), time.Hour)
	h.expect(h.serve(r), 403, "AccessDenied")

	r = newReq("GET", "/bkt/p", nil)
	signer.Presign(r, testNow, time.Hour)
	r.URL.RawQuery = strings.Replace(r.URL.RawQuery, "X-Amz-Signature=", "X-Amz-Signature=0", 1)
	r.URL.RawQuery = r.URL.RawQuery[:len(r.URL.RawQuery)-1]
	h.expect(h.serve(r), 403, "SignatureDoesNotMatch")

	// Hoisted checksum query params are verified against the body.
	body := []byte("hoisted")
	r = newReq("PUT", "/bkt/h?x-amz-checksum-crc32="+strings.ReplaceAll(checksum.Sum(checksum.CRC32, body), "=", "%3D"), body)
	signer.Presign(r, testNow, time.Hour)
	h.expect(h.serve(r), 200, "")
	r = newReq("PUT", "/bkt/h?x-amz-checksum-crc32=AAAAAA%3D%3D", body)
	signer.Presign(r, testNow, time.Hour)
	h.expect(h.serve(r), 400, "BadDigest")
}

func TestStreamingSignedPut(t *testing.T) {
	h := newHarness(t)
	h.expect(h.do("PUT", "/bkt", nil), 200, "")
	data := bytes.Repeat([]byte("s"), 70000)
	r := newReq("PUT", "/bkt/chunked", nil, "Content-Encoding", "aws-chunked,gzip",
		"X-Amz-Decoded-Content-Length", strconv.Itoa(len(data)))
	seed := signer.SignHeader(r, testNow, sigv4.StreamingSigned)
	amzDate := testNow.Format(sigv4.TimeFormat)
	scope := sigv4.Scope(amzDate[:8], "us-east-1")
	key := sigv4.SigningKey("faux3", amzDate[:8], "us-east-1")
	var buf bytes.Buffer
	prev := seed
	for _, chunk := range [][]byte{data[:65536], data[65536:], nil} {
		sig := sigv4.ChunkSignature(key, amzDate, scope, prev, chunk)
		buf.WriteString(strconv.FormatInt(int64(len(chunk)), 16) + ";chunk-signature=" + sig + "\r\n")
		buf.Write(chunk)
		buf.WriteString("\r\n")
		prev = sig
	}
	r.Body = io.NopCloser(bytes.NewReader(buf.Bytes()))
	r.ContentLength = int64(buf.Len())
	h.expect(h.serve(r), 200, "")
	rec := h.do("GET", "/bkt/chunked", nil)
	if !bytes.Equal(rec.Body.Bytes(), data) || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("chunked round trip: len %d enc %q", rec.Body.Len(), rec.Header().Get("Content-Encoding"))
	}
}

func TestCORS(t *testing.T) {
	off := newHarness(t)
	pre := func(origin string) *http.Request {
		return newReq("OPTIONS", "/bkt/k", nil, "Origin", origin, "Access-Control-Request-Method", "PUT",
			"Access-Control-Request-Headers", "content-type")
	}
	off.expect(off.serve(pre("http://example.test")), 403, "AccessDenied")
	rec := off.do("PUT", "/bkt", nil, "Origin", "http://example.test")
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS header with CORS disabled")
	}

	on := newHarness(t, "http://example.test")
	rec = on.serve(pre("http://example.test"))
	on.expect(rec, 200, "")
	for k, v := range map[string]string{"Access-Control-Allow-Origin": "http://example.test",
		"Access-Control-Allow-Methods": "GET, PUT, HEAD, DELETE, POST", "Access-Control-Allow-Headers": "content-type",
		"Access-Control-Max-Age": "3000", "Vary": "Origin"} {
		if rec.Header().Get(k) != v {
			t.Errorf("%s = %q", k, rec.Header().Get(k))
		}
	}
	on.expect(on.serve(pre("http://evil.test")), 403, "AccessDenied")
	rec = on.do("PUT", "/bkt", nil, "Origin", "http://example.test")
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://example.test" ||
		!strings.Contains(rec.Header().Get("Access-Control-Expose-Headers"), "ETag") {
		t.Fatalf("actual CORS headers: %v", rec.Header())
	}
	// Errors also carry CORS headers so browsers can read them.
	rec = on.serve(newReq("GET", "/bkt", nil, "Origin", "http://example.test"))
	if rec.Code != 403 || rec.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Fatalf("error CORS: %d %v", rec.Code, rec.Header())
	}

	star := newHarness(t, "*")
	if rec := star.serve(pre("http://any.test")); rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("wildcard: %d %v", rec.Code, rec.Header())
	}
}

func TestPanicRecovery(t *testing.T) {
	h := newHarness(t)
	h.srv.Store = nil
	rec := h.do("GET", "/", nil)
	h.expect(rec, 500, "InternalError")
}
