// Package s3api is the faux3 S3 HTTP API: routing, authentication,
// handlers for the ten supported operations, and CORS.
package s3api

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/change-fabric/faux3/internal/s3err"
	"github.com/change-fabric/faux3/internal/store"
)

func notImplemented(what string) *s3err.Error {
	return s3err.New("NotImplemented", "A header or operation you provided implies functionality that is not implemented: "+what)
}

// apiError maps store errors and transport errors to S3 errors. Values
// that are already *s3err.Error pass through unchanged.
func apiError(err error, bucket, key string) *s3err.Error {
	var e *s3err.Error
	if errors.As(err, &e) {
		return e
	}
	switch {
	case errors.Is(err, store.ErrNoSuchBucket):
		return s3err.New("NoSuchBucket", "The specified bucket does not exist").With("BucketName", bucket)
	case errors.Is(err, store.ErrNoSuchKey):
		return s3err.New("NoSuchKey", "The specified key does not exist.").With("Key", key)
	case errors.Is(err, store.ErrBucketNotEmpty):
		return s3err.New("BucketNotEmpty", "The bucket you tried to delete is not empty").With("BucketName", bucket)
	case errors.Is(err, store.ErrBucketExists):
		return s3err.New("BucketAlreadyOwnedByYou", "Your previous request to create the named bucket succeeded and you already own it.").With("BucketName", bucket)
	case errors.Is(err, store.ErrInvalidBucketName):
		return s3err.New("InvalidBucketName", "The specified bucket is not valid.").With("BucketName", bucket)
	case errors.Is(err, store.ErrKeyTooLong):
		return s3err.New("KeyTooLongError", "Your key is too long")
	case errors.Is(err, store.ErrInvalidKey):
		return s3err.New("InvalidArgument", "Object keys must be 1 to 1024 bytes of valid UTF-8")
	case errors.Is(err, store.ErrMetadataTooLarge):
		return s3err.New("MetadataTooLarge", "Your metadata headers exceed the maximum allowed metadata size")
	case errors.Is(err, store.ErrEntityTooLarge):
		return s3err.New("EntityTooLarge", "Your proposed upload exceeds the maximum allowed size")
	case errors.Is(err, store.ErrIncompleteBody), errors.Is(err, io.ErrUnexpectedEOF):
		return s3err.New("IncompleteBody", "You did not provide the number of bytes specified by the Content-Length HTTP header")
	case errors.Is(err, store.ErrPreconditionFailed):
		return s3err.New("PreconditionFailed", "At least one of the pre-conditions you specified did not hold").With("Condition", "If-None-Match")
	case errors.Is(err, store.ErrInvalidArgument):
		msg := strings.TrimPrefix(err.Error(), store.ErrInvalidArgument.Error()+": ")
		return s3err.New("InvalidArgument", msg)
	}
	return s3err.As(err)
}

// writeError sends err as an S3 error document (status only for HEAD).
func writeError(w http.ResponseWriter, r *http.Request, err error, bucket, key string) {
	e := apiError(err, bucket, key)
	if st, ok := w.(*statusWriter); ok {
		st.code = e.Code
	}
	s3err.Write(w, r, e, w.Header().Get("x-amz-request-id"))
}
