package s3api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/change-fabric/faux3/internal/s3err"
	"github.com/change-fabric/faux3/internal/store"
)

func (s *Server) listObjectsV2(w http.ResponseWriter, r *http.Request, req *request) {
	q := req.query
	p := store.ListParams{
		Prefix:            q.Get("prefix"),
		Delimiter:         q.Get("delimiter"),
		MaxKeys:           1000,
		ContinuationToken: q.Get("continuation-token"),
		StartAfter:        q.Get("start-after"),
	}
	if q.Has("max-keys") {
		n, err := strconv.Atoi(q.Get("max-keys"))
		if err != nil || n < 0 {
			writeError(w, r, s3err.New("InvalidArgument", "Provided max-keys not an integer or within integer range").
				With("ArgumentName", "max-keys").With("ArgumentValue", q.Get("max-keys")), req.bucket, "")
			return
		}
		p.MaxKeys = min(n, 1000)
	}
	enc := q.Get("encoding-type")
	if enc != "" && enc != "url" {
		writeError(w, r, s3err.New("InvalidArgument", "Invalid Encoding Method specified in Request").
			With("ArgumentName", "encoding-type").With("ArgumentValue", enc), req.bucket, "")
		return
	}
	if q.Has("continuation-token") && p.ContinuationToken == "" {
		writeError(w, r, s3err.New("InvalidArgument", "The continuation token provided is incorrect").
			With("ArgumentName", "continuation-token"), req.bucket, "")
		return
	}
	res, err := s.Store.List(req.bucket, p)
	if err != nil {
		writeError(w, r, err, req.bucket, "")
		return
	}
	e := func(v string) string {
		if enc == "url" {
			return urlEncode(v)
		}
		return v
	}
	fetchOwner, _ := strconv.ParseBool(q.Get("fetch-owner"))
	out := listBucketResult{
		Xmlns:                 s3Namespace,
		Name:                  req.bucket,
		Prefix:                e(p.Prefix),
		Delimiter:             e(p.Delimiter),
		MaxKeys:               p.MaxKeys,
		KeyCount:              len(res.Objects) + len(res.CommonPrefixes),
		IsTruncated:           res.IsTruncated,
		ContinuationToken:     p.ContinuationToken,
		NextContinuationToken: res.NextContinuationToken,
		StartAfter:            e(p.StartAfter),
		EncodingType:          enc,
	}
	for _, m := range res.Objects {
		o := objectEntry{Key: e(m.Key), LastModified: isoFormat(m.LastModified), ETag: quoteETag(m.ETag),
			Size: m.Size, StorageClass: "STANDARD"}
		if fetchOwner {
			o.Owner = &theOwner
		}
		out.Contents = append(out.Contents, o)
	}
	for _, cp := range res.CommonPrefixes {
		out.CommonPrefixes = append(out.CommonPrefixes, commonPrefix{Prefix: e(cp)})
	}
	writeXML(w, http.StatusOK, out)
}

// listObjects serves ListObjects (V1): the marker-based listing. It
// reuses the same store listing as ListObjectsV2, mapping the request
// marker to the store's exclusive StartAfter resume point.
func (s *Server) listObjects(w http.ResponseWriter, r *http.Request, req *request) {
	q := req.query
	marker := q.Get("marker")
	p := store.ListParams{
		Prefix:     q.Get("prefix"),
		Delimiter:  q.Get("delimiter"),
		MaxKeys:    1000,
		StartAfter: marker,
	}
	if q.Has("max-keys") {
		n, err := strconv.Atoi(q.Get("max-keys"))
		if err != nil || n < 0 {
			writeError(w, r, s3err.New("InvalidArgument", "Provided max-keys not an integer or within integer range").
				With("ArgumentName", "max-keys").With("ArgumentValue", q.Get("max-keys")), req.bucket, "")
			return
		}
		p.MaxKeys = min(n, 1000)
	}
	enc := q.Get("encoding-type")
	if enc != "" && enc != "url" {
		writeError(w, r, s3err.New("InvalidArgument", "Invalid Encoding Method specified in Request").
			With("ArgumentName", "encoding-type").With("ArgumentValue", enc), req.bucket, "")
		return
	}
	res, err := s.Store.List(req.bucket, p)
	if err != nil {
		writeError(w, r, err, req.bucket, "")
		return
	}
	e := func(v string) string {
		if enc == "url" {
			return urlEncode(v)
		}
		return v
	}
	out := listBucketResultV1{
		Xmlns:        s3Namespace,
		Name:         req.bucket,
		Prefix:       e(p.Prefix),
		Marker:       e(marker),
		MaxKeys:      p.MaxKeys,
		Delimiter:    e(p.Delimiter),
		EncodingType: enc,
		IsTruncated:  res.IsTruncated,
	}
	// S3 returns NextMarker only when a delimiter is set. Without one, the
	// caller resumes from the last key in the response.
	if res.IsTruncated && p.Delimiter != "" {
		out.NextMarker = e(res.NextMarker)
	}
	for _, m := range res.Objects {
		// V1 always carries the object owner; V2 gates it behind fetch-owner.
		out.Contents = append(out.Contents, objectEntry{
			Key: e(m.Key), LastModified: isoFormat(m.LastModified), ETag: quoteETag(m.ETag),
			Size: m.Size, StorageClass: "STANDARD", Owner: &theOwner,
		})
	}
	for _, cp := range res.CommonPrefixes {
		out.CommonPrefixes = append(out.CommonPrefixes, commonPrefix{Prefix: e(cp)})
	}
	writeXML(w, http.StatusOK, out)
}

// urlEncode encodes a listing value the way S3 does for
// encoding-type=url: form encoding (space as +) with slashes kept.
func urlEncode(v string) string {
	return strings.ReplaceAll(url.QueryEscape(v), "%2F", "/")
}
