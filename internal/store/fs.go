package store

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	formatVersion   = "1"
	maxKeyBytes     = 1024
	maxUserMetaSize = 2048
	copyBufSize     = 32 << 10
)

// Options configure a filesystem store.
type Options struct {
	Dir           string
	Fsync         bool
	MaxObjectSize int64
	// Region is recorded on each new bucket as its LocationConstraint and
	// reported by GetBucketLocation. Defaults to us-east-1.
	Region string
	Logger *slog.Logger
}

// FS is the filesystem Store. Layout under Dir:
//
//	.faux3/format                         "1\n"
//	.faux3/lock                           exclusive flock, one process per dir
//	.faux3/tmp/                           staging, same filesystem
//	buckets/<b>/.bucket.json
//	buckets/<b>/objects/<h[0:2]>/<h>/{data,meta.json}   h = hex(sha256(key))
type FS struct {
	dir     string
	tmp     string
	fsync   bool
	maxSize int64
	region  string
	log     *slog.Logger
	keys    stripes
	lock    *os.File // exclusive flock on .faux3/lock

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	info    Bucket
	mu      sync.RWMutex // write-held only by DeleteBucket
	deleted bool
	idx     *index
}

var _ Store = (*FS)(nil)

// NotWritableError reports a data dir the process cannot write to.
type NotWritableError struct {
	Dir string
	Err error
}

func (e *NotWritableError) Error() string {
	return fmt.Sprintf("faux3: data dir %s is not writable by uid 65532 (the image's nonroot user). "+
		"For a bind mount, either chown the host dir to 65532 or run the container with "+
		"user: \"<your uid>:<your gid>\" (compose) / --user (docker run). Named volumes work without changes. (%v)",
		e.Dir, e.Err)
}

func (e *NotWritableError) Unwrap() error { return e.Err }

// Open creates the layout if needed, checks the format, runs the recovery
// sweep, and builds the in-memory index.
func Open(opts Options) (*FS, error) {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.MaxObjectSize <= 0 {
		opts.MaxObjectSize = 5 << 30
	}
	if opts.Region == "" {
		opts.Region = "us-east-1"
	}
	s := &FS{
		dir:     opts.Dir,
		tmp:     filepath.Join(opts.Dir, ".faux3", "tmp"),
		fsync:   opts.Fsync,
		maxSize: opts.MaxObjectSize,
		region:  opts.Region,
		log:     opts.Logger,
		buckets: map[string]*bucket{},
	}
	if err := s.initLayout(); err != nil {
		_ = s.Close()
		return nil, err
	}
	if err := s.recover(); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func (s *FS) initLayout() error {
	for _, d := range []string{s.tmp, filepath.Join(s.dir, "buckets")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return wrapWritable(s.dir, err)
		}
	}
	probe := filepath.Join(s.tmp, "probe")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		return wrapWritable(s.dir, err)
	}
	_ = os.Remove(probe)

	lock, err := lockDataDir(s.dir)
	if err != nil {
		return err
	}
	s.lock = lock

	fpath := filepath.Join(s.dir, ".faux3", "format")
	b, err := os.ReadFile(fpath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return s.writeFileAtomic(fpath, []byte(formatVersion+"\n"))
	case err != nil:
		return err
	case strings.TrimSpace(string(b)) != formatVersion:
		return fmt.Errorf("faux3: unsupported data format %q in %s (want %s)", strings.TrimSpace(string(b)), fpath, formatVersion)
	}
	return nil
}

func wrapWritable(dir string, err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return &NotWritableError{Dir: dir, Err: err}
	}
	return err
}

func keyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func (s *FS) bucketDir(name string) string { return filepath.Join(s.dir, "buckets", name) }

func (s *FS) keyDir(bucket, key string) string {
	h := keyHash(key)
	return filepath.Join(s.bucketDir(bucket), "objects", h[:2], h)
}

func randName(prefix string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

func (s *FS) syncDir(dir string) error {
	if !s.fsync {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// writeFileAtomic writes via a temp file and rename in the target dir.
func (s *FS) writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp-" + randName("")
	err := s.writeNewFile(tmp, data)
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return s.syncDir(filepath.Dir(path))
}

// bucketByName returns the live bucket entry, or nil.
func (s *FS) bucketByName(name string) *bucket {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buckets[name]
}

// rlockBucket returns the bucket with its read lock held, or ErrNoSuchBucket.
func (s *FS) rlockBucket(name string) (*bucket, error) {
	b := s.bucketByName(name)
	if b == nil {
		return nil, ErrNoSuchBucket
	}
	b.mu.RLock()
	if b.deleted {
		b.mu.RUnlock()
		return nil, ErrNoSuchBucket
	}
	return b, nil
}

// CreateBucket creates a bucket, or returns ErrBucketExists.
func (s *FS) CreateBucket(name string) error {
	if !ValidBucketName(name) {
		return ErrInvalidBucketName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.buckets[name]; ok {
		return ErrBucketExists
	}
	info := Bucket{Name: name, Created: time.Now().UTC(), Region: s.region}
	dir := s.bucketDir(name)
	if err := os.MkdirAll(filepath.Join(dir, "objects"), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	if err := s.writeFileAtomic(filepath.Join(dir, ".bucket.json"), data); err != nil {
		return err
	}
	if err := s.syncDir(filepath.Dir(dir)); err != nil {
		return err
	}
	s.buckets[name] = &bucket{info: info, idx: newIndex()}
	return nil
}

// DeleteBucket removes an empty bucket.
func (s *FS) DeleteBucket(name string) error {
	b := s.bucketByName(name)
	if b == nil {
		return ErrNoSuchBucket
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.deleted {
		return ErrNoSuchBucket
	}
	if b.idx.len() > 0 {
		return ErrBucketNotEmpty
	}
	// Move aside first so a crash never leaves a half-deleted bucket.
	trash := filepath.Join(s.tmp, randName("bucket-"))
	if err := os.Rename(s.bucketDir(name), trash); err != nil {
		return err
	}
	b.deleted = true
	s.mu.Lock()
	delete(s.buckets, name)
	s.mu.Unlock()
	_ = os.RemoveAll(trash)
	return nil
}

// HeadBucket returns bucket info or ErrNoSuchBucket.
func (s *FS) HeadBucket(name string) (Bucket, error) {
	b, err := s.rlockBucket(name)
	if err != nil {
		return Bucket{}, err
	}
	defer b.mu.RUnlock()
	return b.info, nil
}

// ListBuckets returns every bucket sorted by name.
func (s *FS) ListBuckets() []Bucket {
	s.mu.Lock()
	out := make([]Bucket, 0, len(s.buckets))
	for _, b := range s.buckets {
		out = append(out, b.info)
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func userMetaSize(m map[string]string) int {
	n := 0
	for k, v := range m {
		n += len(k) + len(v)
	}
	return n
}

// PutObject streams the body into a staging dir without holding the
// bucket lock, verifies it, then swaps it into place under the bucket read
// lock and the key's write lock. A DeleteBucket that lands mid-upload wins:
// the stage is discarded and the put fails with ErrNoSuchBucket.
func (s *FS) PutObject(ctx context.Context, bucketName, key string, in PutInput) (Meta, error) {
	if err := ValidateKey(key); err != nil {
		return Meta{}, err
	}
	meta := in.Meta
	if meta == nil {
		meta = &Meta{}
	}
	if userMetaSize(meta.UserMetadata) > maxUserMetaSize {
		return Meta{}, ErrMetadataTooLarge
	}
	if in.ContentLength > s.maxSize {
		return Meta{}, ErrEntityTooLarge
	}
	// Fail fast on a missing bucket before reading the body.
	if _, err := s.HeadBucket(bucketName); err != nil {
		return Meta{}, err
	}

	stage := filepath.Join(s.tmp, randName("put-"))
	if err := os.Mkdir(stage, 0o755); err != nil {
		return Meta{}, err
	}
	// After a successful commit the stage path no longer exists, so this
	// only removes an abandoned stage.
	defer os.RemoveAll(stage)

	size, etag, err := s.writeData(ctx, filepath.Join(stage, "data"), in.Body)
	if err != nil {
		return Meta{}, err
	}
	if size > s.maxSize {
		return Meta{}, ErrEntityTooLarge
	}
	if in.ContentLength >= 0 && size != in.ContentLength {
		return Meta{}, ErrIncompleteBody
	}
	if in.Verify != nil {
		if err := in.Verify(); err != nil {
			return Meta{}, err
		}
	}
	meta.Key = key
	meta.Size = size
	meta.ETag = etag
	meta.LastModified = time.Now().UTC()
	if meta.ContentType == "" {
		meta.ContentType = "application/octet-stream"
	}
	mdata, err := json.Marshal(meta)
	if err != nil {
		return Meta{}, err
	}
	if err := s.writeNewFile(filepath.Join(stage, "meta.json"), mdata); err != nil {
		return Meta{}, err
	}
	if err := s.syncDir(stage); err != nil {
		return Meta{}, err
	}

	stored := *meta
	old, err := s.commit(bucketName, stage, &stored, in.IfNoneMatch)
	if old != "" {
		_ = os.RemoveAll(old)
	}
	if err != nil {
		return Meta{}, err
	}
	return stored, nil
}

// commit swaps a finished stage into place. It holds the bucket read lock,
// so DeleteBucket cannot move the bucket dir mid-swap, and re-checks that
// the bucket still exists. It returns the displaced old dir, if any, for
// the caller to remove after the locks are released.
func (s *FS) commit(bucketName, stage string, m *Meta, ifNoneMatch bool) (old string, err error) {
	b, err := s.rlockBucket(bucketName)
	if err != nil {
		return "", err
	}
	defer b.mu.RUnlock()
	kl := s.keys.lock(bucketName, m.Key)
	kl.Lock()
	defer kl.Unlock()

	if _, exists := b.idx.get(m.Key); exists && ifNoneMatch {
		return "", ErrPreconditionFailed
	}
	dst := s.keyDir(bucketName, m.Key)
	if _, err := os.Lstat(dst); err == nil {
		old = filepath.Join(s.tmp, randName("old-"))
		if err := os.Rename(dst, old); err != nil {
			return "", err
		}
	}
	err = os.MkdirAll(filepath.Dir(dst), 0o755)
	if err == nil {
		err = os.Rename(stage, dst)
	}
	if err != nil {
		if old != "" {
			_ = os.Rename(old, dst)
		}
		return "", err
	}
	b.idx.put(m)
	return old, s.syncDir(filepath.Dir(dst))
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func (s *FS) writeData(ctx context.Context, path string, body io.Reader) (int64, string, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	if body == nil {
		body = strings.NewReader("")
	}
	h := md5.New()
	src := io.LimitReader(ctxReader{ctx: ctx, r: body}, s.maxSize+1)
	n, err := io.CopyBuffer(io.MultiWriter(f, h), src, make([]byte, copyBufSize))
	if err != nil {
		return 0, "", err
	}
	if s.fsync {
		if err := f.Sync(); err != nil {
			return 0, "", err
		}
	}
	if err := f.Close(); err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// writeNewFile creates path exclusively and writes data, fsyncing when
// enabled.
func (s *FS) writeNewFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil && s.fsync {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// GetObject opens the data file under the key read lock; the returned fd
// survives a later overwrite or delete.
func (s *FS) GetObject(bucketName, key string) (*os.File, Meta, error) {
	return s.lookup(bucketName, key, true)
}

// HeadObject returns the object's metadata.
func (s *FS) HeadObject(bucketName, key string) (Meta, error) {
	_, m, err := s.lookup(bucketName, key, false)
	return m, err
}

// lookup finds a key under the bucket and key read locks and, when open is
// set, opens its data file.
func (s *FS) lookup(bucketName, key string, open bool) (*os.File, Meta, error) {
	b, err := s.rlockBucket(bucketName)
	if err != nil {
		return nil, Meta{}, err
	}
	defer b.mu.RUnlock()
	kl := s.keys.lock(bucketName, key)
	kl.RLock()
	defer kl.RUnlock()
	m, ok := b.idx.get(key)
	if !ok {
		return nil, Meta{}, ErrNoSuchKey
	}
	if !open {
		return nil, m, nil
	}
	f, err := os.Open(filepath.Join(s.keyDir(bucketName, key), "data"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, Meta{}, ErrNoSuchKey
		}
		return nil, Meta{}, err
	}
	return f, m, nil
}

// DeleteObject removes a key; deleting a missing key succeeds.
func (s *FS) DeleteObject(bucketName, key string) error {
	b, err := s.rlockBucket(bucketName)
	if err != nil {
		return err
	}
	defer b.mu.RUnlock()
	kl := s.keys.lock(bucketName, key)
	kl.Lock()
	if _, ok := b.idx.get(key); !ok {
		kl.Unlock()
		return nil
	}
	trash := filepath.Join(s.tmp, randName("del-"))
	dst := s.keyDir(bucketName, key)
	if err := os.Rename(dst, trash); err != nil && !errors.Is(err, fs.ErrNotExist) {
		kl.Unlock()
		return err
	}
	b.idx.remove(key)
	syncErr := s.syncDir(filepath.Dir(dst))
	kl.Unlock()
	_ = os.RemoveAll(trash)
	return syncErr
}
