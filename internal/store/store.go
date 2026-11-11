// Package store is the faux3 object store: buckets and objects kept on
// the local filesystem with an in-memory index for listing.
package store

import (
	"context"
	"errors"
	"io"
	"os"
	"time"
)

// Errors returned by the store. The s3api package maps each one to its
// S3 error code.
var (
	ErrNoSuchBucket       = errors.New("store: no such bucket")
	ErrNoSuchKey          = errors.New("store: no such key")
	ErrBucketNotEmpty     = errors.New("store: bucket not empty")
	ErrBucketExists       = errors.New("store: bucket already exists")
	ErrInvalidBucketName  = errors.New("store: invalid bucket name")
	ErrKeyTooLong         = errors.New("store: key too long")
	ErrInvalidKey         = errors.New("store: invalid key")
	ErrMetadataTooLarge   = errors.New("store: user metadata too large")
	ErrEntityTooLarge     = errors.New("store: entity too large")
	ErrIncompleteBody     = errors.New("store: body shorter or longer than content length")
	ErrPreconditionFailed = errors.New("store: object already exists")
	ErrInvalidArgument    = errors.New("store: invalid argument")
)

// Meta is the object metadata persisted in meta.json beside the data file.
// Key is authoritative: keys are never derived from file names.
type Meta struct {
	Key                string            `json:"key"`
	Size               int64             `json:"size"`
	ETag               string            `json:"etag"`
	ContentType        string            `json:"contentType,omitempty"`
	ContentEncoding    string            `json:"contentEncoding,omitempty"`
	ContentDisposition string            `json:"contentDisposition,omitempty"`
	CacheControl       string            `json:"cacheControl,omitempty"`
	ContentLanguage    string            `json:"contentLanguage,omitempty"`
	Expires            string            `json:"expires,omitempty"`
	UserMetadata       map[string]string `json:"userMetadata,omitempty"`
	Checksums          map[string]string `json:"checksums,omitempty"`
	LastModified       time.Time         `json:"lastModified"`
}

// Bucket describes one bucket.
type Bucket struct {
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
}

// PutInput is the request side of PutObject.
type PutInput struct {
	// Body is streamed to disk; it is never buffered whole.
	Body io.Reader
	// ContentLength is the expected decoded length, or -1 if unknown.
	ContentLength int64
	// Meta carries the caller-supplied headers. The store fills Key, Size,
	// ETag, and LastModified. It is written to disk after Verify returns,
	// so Verify may record checksums in it.
	Meta *Meta
	// Verify runs after Body is fully consumed and before commit. A
	// non-nil error discards the upload and leaves any old object intact.
	Verify func() error
	// IfNoneMatch rejects the put with ErrPreconditionFailed when the key
	// already exists (If-None-Match: *).
	IfNoneMatch bool
}

// ListParams are the ListObjectsV2 inputs.
type ListParams struct {
	Prefix            string
	Delimiter         string
	MaxKeys           int
	ContinuationToken string
	StartAfter        string
}

// ListResult is the output shared by ListObjectsV2 and ListObjects V1.
type ListResult struct {
	Objects        []Meta
	CommonPrefixes []string
	IsTruncated    bool
	// NextContinuationToken is the opaque V2 resume token for the last
	// returned key or common prefix. Empty unless IsTruncated.
	NextContinuationToken string
	// NextMarker is the same resume point in raw form, for the V1 marker.
	// Empty unless IsTruncated.
	NextMarker string
}

// Store is the storage backend used by the S3 handlers.
type Store interface {
	CreateBucket(name string) error
	DeleteBucket(name string) error
	HeadBucket(name string) (Bucket, error)
	ListBuckets() []Bucket
	PutObject(ctx context.Context, bucket, key string, in PutInput) (Meta, error)
	// GetObject returns an open data file the caller must close. The file
	// stays readable even if the object is overwritten or deleted.
	GetObject(bucket, key string) (*os.File, Meta, error)
	HeadObject(bucket, key string) (Meta, error)
	DeleteObject(bucket, key string) error
	List(bucket string, p ListParams) (ListResult, error)
}
