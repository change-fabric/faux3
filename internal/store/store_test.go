package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openStore(t *testing.T, dir string) *FS {
	t.Helper()
	s, err := Open(Options{Dir: dir, Fsync: false, MaxObjectSize: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newBucket(t *testing.T) (*FS, string) {
	t.Helper()
	dir := t.TempDir()
	s := openStore(t, dir)
	if err := s.CreateBucket("bkt"); err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func put(t *testing.T, s *FS, key, body string) Meta {
	t.Helper()
	m, err := s.PutObject(context.Background(), "bkt", key, PutInput{
		Body: strings.NewReader(body), ContentLength: int64(len(body)),
		Meta: &Meta{ContentType: "text/plain", UserMetadata: map[string]string{"a": "b"}},
	})
	if err != nil {
		t.Fatalf("put %q: %v", key, err)
	}
	return m
}

func get(t *testing.T, s *FS, key string) string {
	t.Helper()
	f, _, err := s.GetObject("bkt", key)
	if err != nil {
		t.Fatalf("get %q: %v", key, err)
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func tmpEntries(t *testing.T, dir string) int {
	t.Helper()
	e, err := os.ReadDir(filepath.Join(dir, ".faux3", "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	return len(e)
}

func TestRoundTripAndReopen(t *testing.T) {
	s, dir := newBucket(t)
	m := put(t, s, "dir/file.txt", "hello")
	if m.ETag != "5d41402abc4b2a76b9719d911017c592" || m.Size != 5 {
		t.Fatalf("meta = %+v", m)
	}
	if got := get(t, s, "dir/file.txt"); got != "hello" {
		t.Fatalf("got %q", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2 := openStore(t, dir)
	h, err := s2.HeadObject("bkt", "dir/file.txt")
	if err != nil || h.ContentType != "text/plain" || h.UserMetadata["a"] != "b" || h.ETag != m.ETag {
		t.Fatalf("head after reopen = %+v, %v", h, err)
	}
	if bs := s2.ListBuckets(); len(bs) != 1 || bs[0].Name != "bkt" {
		t.Fatalf("buckets = %+v", bs)
	}
}

func TestUnusualKeys(t *testing.T) {
	s, _ := newBucket(t)
	keys := []string{"a b", "unicode-\u00e9\u4e16", "plus+", "pct%20", "..", "a//b", "/lead", "../../etc", strings.Repeat("k", 1024)}
	for _, k := range keys {
		put(t, s, k, "v:"+k)
	}
	for _, k := range keys {
		if got := get(t, s, k); got != "v:"+k {
			t.Fatalf("key %q: got %q", k, got)
		}
	}
	if _, err := s.PutObject(context.Background(), "bkt", strings.Repeat("k", 1025), PutInput{Body: strings.NewReader(""), ContentLength: -1}); !errors.Is(err, ErrKeyTooLong) {
		t.Fatalf("long key err = %v", err)
	}
	if _, err := s.PutObject(context.Background(), "bkt", "\xff", PutInput{Body: strings.NewReader(""), ContentLength: -1}); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("bad utf8 err = %v", err)
	}
}

func TestOverwrite(t *testing.T) {
	s, dir := newBucket(t)
	put(t, s, "k", "one")
	put(t, s, "k", "two-longer")
	if got := get(t, s, "k"); got != "two-longer" {
		t.Fatalf("got %q", got)
	}
	if n := tmpEntries(t, dir); n != 0 {
		t.Fatalf("tmp has %d entries", n)
	}
}

func TestFailedVerifyKeepsOldObject(t *testing.T) {
	s, dir := newBucket(t)
	put(t, s, "k", "original")
	bad := errors.New("bad digest")
	_, err := s.PutObject(context.Background(), "bkt", "k", PutInput{
		Body: strings.NewReader("replacement"), ContentLength: -1,
		Verify: func() error { return bad },
	})
	if !errors.Is(err, bad) {
		t.Fatalf("err = %v", err)
	}
	if got := get(t, s, "k"); got != "original" {
		t.Fatalf("got %q", got)
	}
	if n := tmpEntries(t, dir); n != 0 {
		t.Fatalf("tmp has %d entries", n)
	}
}

func TestPutErrors(t *testing.T) {
	s, _ := newBucket(t)
	ctx := context.Background()
	if _, err := s.PutObject(ctx, "bkt", "k", PutInput{Body: strings.NewReader("abc"), ContentLength: 5}); !errors.Is(err, ErrIncompleteBody) {
		t.Fatalf("incomplete err = %v", err)
	}
	big := bytes.NewReader(make([]byte, 1<<20+1))
	if _, err := s.PutObject(ctx, "bkt", "k", PutInput{Body: big, ContentLength: -1}); !errors.Is(err, ErrEntityTooLarge) {
		t.Fatalf("too large err = %v", err)
	}
	md := &Meta{UserMetadata: map[string]string{"x": strings.Repeat("v", 2048)}}
	if _, err := s.PutObject(ctx, "bkt", "k", PutInput{Body: strings.NewReader(""), ContentLength: 0, Meta: md}); !errors.Is(err, ErrMetadataTooLarge) {
		t.Fatalf("metadata err = %v", err)
	}
	if _, err := s.PutObject(ctx, "nope", "k", PutInput{Body: strings.NewReader(""), ContentLength: 0}); !errors.Is(err, ErrNoSuchBucket) {
		t.Fatalf("no bucket err = %v", err)
	}
	put(t, s, "exists", "x")
	if _, err := s.PutObject(ctx, "bkt", "exists", PutInput{Body: strings.NewReader("y"), ContentLength: 1, IfNoneMatch: true}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("if-none-match err = %v", err)
	}
	if _, err := s.PutObject(ctx, "bkt", "fresh", PutInput{Body: strings.NewReader("y"), ContentLength: 1, IfNoneMatch: true}); err != nil {
		t.Fatalf("if-none-match fresh err = %v", err)
	}
}

func TestDeleteObject(t *testing.T) {
	s, _ := newBucket(t)
	put(t, s, "k", "v")
	f, _, err := s.GetObject("bkt", "k")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := s.DeleteObject("bkt", "k"); err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(f); string(b) != "v" {
		t.Fatalf("open fd read %q after delete", b)
	}
	if _, err := s.HeadObject("bkt", "k"); !errors.Is(err, ErrNoSuchKey) {
		t.Fatalf("head err = %v", err)
	}
	if err := s.DeleteObject("bkt", "missing"); err != nil {
		t.Fatalf("delete missing = %v", err)
	}
	if err := s.DeleteObject("nope", "k"); !errors.Is(err, ErrNoSuchBucket) {
		t.Fatalf("delete no bucket = %v", err)
	}
}

func TestBuckets(t *testing.T) {
	s, dir := newBucket(t)
	if err := s.CreateBucket("bkt"); !errors.Is(err, ErrBucketExists) {
		t.Fatalf("dup err = %v", err)
	}
	for _, n := range []string{"ab", "Bad", "a..b", "192.168.1.1", "xn--x", "x-s3alias", "-ab", strings.Repeat("a", 64)} {
		if err := s.CreateBucket(n); !errors.Is(err, ErrInvalidBucketName) {
			t.Fatalf("%q err = %v", n, err)
		}
	}
	put(t, s, "k", "v")
	if err := s.DeleteBucket("bkt"); !errors.Is(err, ErrBucketNotEmpty) {
		t.Fatalf("non-empty delete = %v", err)
	}
	if err := s.DeleteObject("bkt", "k"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBucket("bkt"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HeadBucket("bkt"); !errors.Is(err, ErrNoSuchBucket) {
		t.Fatalf("head deleted = %v", err)
	}
	if err := s.DeleteBucket("bkt"); !errors.Is(err, ErrNoSuchBucket) {
		t.Fatalf("delete twice = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "buckets", "bkt")); !os.IsNotExist(err) {
		t.Fatalf("bucket dir remains: %v", err)
	}
	if err := s.CreateBucket("bkt"); err != nil {
		t.Fatalf("recreate = %v", err)
	}
}

func TestBucketRegionRecordedAndReopened(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(Options{Dir: dir, Region: "eu-west-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateBucket("bkt"); err != nil {
		t.Fatal(err)
	}
	b, err := s.HeadBucket("bkt")
	if err != nil || b.Region != "eu-west-1" {
		t.Fatalf("head = %+v, %v", b, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// A different configured region does not rewrite the recorded one.
	s2, err := Open(Options{Dir: dir, Region: "us-west-2"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	if b, err := s2.HeadBucket("bkt"); err != nil || b.Region != "eu-west-1" {
		t.Fatalf("head after reopen = %+v, %v", b, err)
	}
}

func TestBucketRegionLegacyDefault(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(Options{Dir: dir, Region: "eu-west-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateBucket("bkt"); err != nil {
		t.Fatal(err)
	}
	// Rewrite .bucket.json without a region, as faux3 0.1.0 wrote it.
	bj := filepath.Join(dir, "buckets", "bkt", ".bucket.json")
	raw, err := os.ReadFile(bj)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "region")
	legacy, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bj, legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// A bucket with no recorded region falls back to the configured region.
	s2, err := Open(Options{Dir: dir, Region: "ap-south-1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	if b, err := s2.HeadBucket("bkt"); err != nil || b.Region != "ap-south-1" {
		t.Fatalf("legacy head = %+v, %v", b, err)
	}
}

func TestCaseDistinctKeys(t *testing.T) {
	s, _ := newBucket(t)
	put(t, s, "A", "upper")
	put(t, s, "a", "lower")
	if keyHash("A") == keyHash("a") {
		t.Fatal("hash collision")
	}
	if get(t, s, "A") != "upper" || get(t, s, "a") != "lower" {
		t.Fatal("case keys collided")
	}
}

func TestRecoverySweep(t *testing.T) {
	s, dir := newBucket(t)
	put(t, s, "keep", "v")
	stray := filepath.Join(dir, ".faux3", "tmp", "put-crashed")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stray, "data"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	orphan := s.keyDir("bkt", "orphan")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "data"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// meta.json in a dir that does not match its key hash is skipped.
	wrong := filepath.Join(dir, "buckets", "bkt", "objects", "00", strings.Repeat("0", 64))
	if err := os.MkdirAll(wrong, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wrong, "meta.json"), []byte(`{"key":"misplaced"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2 := openStore(t, dir)
	if n := tmpEntries(t, dir); n != 0 {
		t.Fatalf("tmp has %d entries", n)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan key dir remains: %v", err)
	}
	if _, err := s2.HeadObject("bkt", "misplaced"); !errors.Is(err, ErrNoSuchKey) {
		t.Fatalf("misplaced indexed: %v", err)
	}
	if get(t, s2, "keep") != "v" {
		t.Fatal("kept object lost")
	}
}

func TestFormatMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := openStore(t, dir).Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".faux3", "format"), []byte("2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Options{Dir: dir}); err == nil || !strings.Contains(err.Error(), "unsupported data format") {
		t.Fatalf("err = %v", err)
	}
}

func TestNotWritable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	_, err := Open(Options{Dir: filepath.Join(dir, "data")})
	var nw *NotWritableError
	if !errors.As(err, &nw) || !strings.Contains(err.Error(), "uid 65532") || !strings.Contains(err.Error(), "user:") {
		t.Fatalf("err = %v", err)
	}
}

// TestConcurrentPutGet writes uniform bodies of distinct bytes to one key
// while readers check that every read is a single uniform body.
func TestConcurrentPutGet(t *testing.T) {
	s, _ := newBucket(t)
	const size = 64 << 10
	put(t, s, "hot", strings.Repeat("a", size))
	var wg sync.WaitGroup
	stop := make(chan struct{})
	errc := make(chan error, 16)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				c := byte('a' + (w*25+i)%26)
				body := bytes.Repeat([]byte{c}, size)
				if _, err := s.PutObject(context.Background(), "bkt", "hot", PutInput{Body: bytes.NewReader(body), ContentLength: size}); err != nil {
					errc <- err
					return
				}
			}
		}(w)
	}
	var rg sync.WaitGroup
	for r := 0; r < 4; r++ {
		rg.Add(1)
		go func() {
			defer rg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				f, m, err := s.GetObject("bkt", "hot")
				if err != nil {
					errc <- err
					return
				}
				b, err := io.ReadAll(f)
				f.Close()
				if err != nil {
					errc <- err
					return
				}
				if int64(len(b)) != m.Size || len(b) != size || !bytes.Equal(b, bytes.Repeat(b[:1], size)) {
					errc <- errors.New("torn read")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	rg.Wait()
	close(errc)
	for err := range errc {
		t.Fatal(err)
	}
}

func TestSingleInstanceLock(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	_, err := Open(Options{Dir: dir})
	if !errors.Is(err, ErrDataDirLocked) || !strings.Contains(err.Error(), "another faux3 process") {
		t.Fatalf("second open err = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatalf("open after close: %v", err)
	}
	_ = s2.Close()
}

// gateReader signals on first Read, then blocks until released.
type gateReader struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	body    io.Reader
}

func (g *gateReader) Read(p []byte) (int, error) {
	g.once.Do(func() { close(g.started) })
	<-g.release
	return g.body.Read(p)
}

// TestDeleteBucketDuringSlowPut checks that a slow upload does not hold
// the bucket lock: DeleteBucket and HeadBucket complete while the body is
// still streaming, and the upload then fails cleanly.
func TestDeleteBucketDuringSlowPut(t *testing.T) {
	s, dir := newBucket(t)
	g := &gateReader{started: make(chan struct{}), release: make(chan struct{}), body: strings.NewReader("slow")}
	putErr := make(chan error, 1)
	go func() {
		_, err := s.PutObject(context.Background(), "bkt", "k", PutInput{Body: g, ContentLength: 4})
		putErr <- err
	}()
	<-g.started

	done := make(chan error, 2)
	go func() { done <- s.DeleteBucket("bkt") }()
	go func() {
		_, err := s.HeadBucket("bkt")
		if err != nil && !errors.Is(err, ErrNoSuchBucket) {
			done <- err
			return
		}
		done <- nil
	}()
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("DeleteBucket or HeadBucket blocked behind a slow upload")
		}
	}
	if _, err := s.HeadBucket("bkt"); !errors.Is(err, ErrNoSuchBucket) {
		t.Fatalf("head after delete = %v", err)
	}

	close(g.release)
	if err := <-putErr; !errors.Is(err, ErrNoSuchBucket) {
		t.Fatalf("put after bucket delete = %v", err)
	}
	if n := tmpEntries(t, dir); n != 0 {
		t.Fatalf("tmp has %d entries", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "buckets", "bkt")); !os.IsNotExist(err) {
		t.Fatalf("bucket dir resurrected: %v", err)
	}
}
