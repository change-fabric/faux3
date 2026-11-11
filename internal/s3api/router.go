package s3api

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/change-fabric/faux3/internal/s3err"
	"github.com/change-fabric/faux3/internal/sigv4"
	"github.com/change-fabric/faux3/internal/store"
)

const healthPath = "/-/healthz"

// Server is the top-level faux3 HTTP handler.
type Server struct {
	Store    store.Store
	Verifier *sigv4.Verifier
	// Region is reported by HeadBucket and GetBucketLocation and is the
	// only accepted CreateBucket LocationConstraint.
	Region string
	// CORSOrigins enables CORS when non-empty; "*" allows any origin.
	CORSOrigins []string
	Log         *slog.Logger

	once    sync.Once
	handler http.Handler
}

// ServeHTTP runs the request pipeline: ids and logging, health, CORS,
// path parsing, SigV4, the 501 allowlist, then the operation handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.once.Do(func() { s.handler = s.withMiddleware(s.route) })
	s.handler.ServeHTTP(w, r)
}

// request is one parsed S3 request.
type request struct {
	bucket string
	key    string
	query  url.Values
	ver    *sigv4.Verified
}

// listQueryKeys are the query parameters accepted on bucket and object
// requests beyond the presign and response-* families.
var listQueryKeys = map[string]bool{
	"list-type": true, "prefix": true, "delimiter": true, "max-keys": true,
	"continuation-token": true, "start-after": true, "fetch-owner": true,
	"encoding-type": true, "x-id": true, "location": true,
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) string {
	if r.URL.Path == healthPath && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok")
		return "Health"
	}
	if r.Method == http.MethodOptions {
		s.preflight(w, r)
		return "Preflight"
	}
	s.addCORSHeaders(w, r)

	req, err := parsePath(r.URL.EscapedPath())
	if err != nil {
		writeError(w, r, err, "", "")
		return "Invalid"
	}
	req.ver, err = s.Verifier.Verify(r)
	if err != nil {
		writeError(w, r, err, req.bucket, req.key)
		return "Auth"
	}
	req.query = r.URL.Query()
	op, h, err := s.resolve(r, req)
	if err != nil {
		writeError(w, r, err, req.bucket, req.key)
		return op
	}
	h(w, r, req)
	return op
}

// parsePath splits the escaped path into bucket and key without any
// cleaning: "..", "//", and encoded slashes are preserved in the key.
func parsePath(escaped string) (*request, error) {
	if !strings.HasPrefix(escaped, "/") {
		return nil, s3err.New("InvalidArgument", "The request path must start with /")
	}
	rest := escaped[1:]
	rawBucket, rawKey, _ := strings.Cut(rest, "/")
	bucket, err := url.PathUnescape(rawBucket)
	if err != nil {
		return nil, s3err.New("InvalidArgument", "Could not decode the bucket name")
	}
	key, err := url.PathUnescape(rawKey)
	if err != nil {
		return nil, s3err.New("InvalidArgument", "Could not decode the object key")
	}
	return &request{bucket: bucket, key: key}, nil
}

type handlerFunc func(w http.ResponseWriter, r *http.Request, req *request)

// resolve picks the operation, rejecting unsupported methods, query
// subresources, and headers.
func (s *Server) resolve(r *http.Request, req *request) (string, handlerFunc, error) {
	switch r.Method {
	case http.MethodGet, http.MethodPut, http.MethodHead, http.MethodDelete, http.MethodPost:
	default:
		return "Unsupported", nil, s3err.New("MethodNotAllowed", "The specified method is not allowed against this resource.").
			With("Method", r.Method).With("ResourceType", resourceType(req))
	}
	objectGet := req.key != "" && (r.Method == http.MethodGet || r.Method == http.MethodHead)
	for k := range req.query {
		lk := strings.ToLower(k)
		switch {
		case strings.HasPrefix(lk, "x-amz-server-side-encryption"):
			return "Unsupported", nil, notImplemented("server-side encryption")
		case strings.HasPrefix(lk, "x-amz-"):
		case strings.HasPrefix(k, "response-") && objectGet:
		case k == "delete" && r.Method == http.MethodPost && req.key == "":
		case listQueryKeys[k]:
		default:
			return "Unsupported", nil, notImplemented("the ?" + k + " subresource")
		}
	}
	if err := checkHeaders(r, req); err != nil {
		return "Unsupported", nil, err
	}

	if r.Method == http.MethodPost {
		if req.bucket != "" && req.key == "" && req.query.Has("delete") {
			return "DeleteObjects", s.deleteObjects, nil
		}
		return "Unsupported", nil, notImplemented("POST requests (multipart uploads) are not supported")
	}

	if req.bucket == "" {
		if r.Method == http.MethodGet {
			return "ListBuckets", s.listBuckets, nil
		}
		return "Unsupported", nil, s3err.New("MethodNotAllowed", "The specified method is not allowed against this resource.").
			With("Method", r.Method).With("ResourceType", "SERVICE")
	}
	if req.key == "" {
		switch r.Method {
		case http.MethodPut:
			return "CreateBucket", s.createBucket, nil
		case http.MethodHead:
			return "HeadBucket", s.headBucket, nil
		case http.MethodDelete:
			return "DeleteBucket", s.deleteBucket, nil
		}
		if req.query.Has("location") {
			return "GetBucketLocation", s.getBucketLocation, nil
		}
		if req.query.Get("list-type") != "2" {
			return "ListObjects", nil, notImplemented("ListObjects (V1); use ListObjectsV2 (list-type=2)")
		}
		return "ListObjectsV2", s.listObjectsV2, nil
	}
	switch r.Method {
	case http.MethodPut:
		return "PutObject", s.putObject, nil
	case http.MethodGet:
		return "GetObject", s.getObject, nil
	case http.MethodHead:
		return "HeadObject", s.getObject, nil
	}
	return "DeleteObject", s.deleteObject, nil
}

func resourceType(req *request) string {
	switch {
	case req.bucket == "":
		return "SERVICE"
	case req.key == "":
		return "BUCKET"
	}
	return "OBJECT"
}

// checkHeaders rejects request headers that imply unsupported features.
func checkHeaders(r *http.Request, req *request) error {
	for k := range r.Header {
		if strings.HasPrefix(strings.ToLower(k), "x-amz-server-side-encryption") {
			return notImplemented("server-side encryption")
		}
	}
	if r.Header.Get("X-Amz-Copy-Source") != "" {
		return notImplemented("CopyObject (x-amz-copy-source)")
	}
	if r.Header.Get("X-Amz-Tagging") != "" {
		return notImplemented("object tagging (x-amz-tagging)")
	}
	if acl := r.Header.Get("X-Amz-Acl"); acl != "" && acl != "private" {
		return notImplemented("canned ACL " + acl)
	}
	if r.Method == http.MethodPut && req.key != "" {
		if r.Header.Get("If-Match") != "" {
			return notImplemented("If-Match on PutObject")
		}
		if v := r.Header.Get("If-None-Match"); v != "" && v != "*" {
			return notImplemented("If-None-Match other than * on PutObject")
		}
	}
	return nil
}
