package s3api

import (
	"encoding/xml"
	"io"
	"net/http"
	"strings"

	"github.com/change-fabric/faux3/internal/s3err"
)

// maxConfigBody caps the CreateBucket configuration document.
const maxConfigBody = 64 << 10

// DeleteObjects limits: S3 accepts up to 1000 keys per request. maxDeleteBody
// bounds the request document, which at 1000 keys of up to 1024 bytes plus XML
// framing stays well under 2 MiB.
const (
	maxDeleteKeys = 1000
	maxDeleteBody = 2 << 20
)

func (s *Server) listBuckets(w http.ResponseWriter, r *http.Request, req *request) {
	res := listAllMyBucketsResult{Xmlns: s3Namespace, Owner: theOwner, Buckets: []bucketEntry{}}
	for _, b := range s.Store.ListBuckets() {
		res.Buckets = append(res.Buckets, bucketEntry{Name: b.Name, CreationDate: isoFormat(b.Created)})
	}
	writeXML(w, http.StatusOK, res)
}

func (s *Server) createBucket(w http.ResponseWriter, r *http.Request, req *request) {
	p, err := req.ver.Payload(r)
	if err != nil {
		writeError(w, r, err, req.bucket, "")
		return
	}
	body, err := io.ReadAll(io.LimitReader(p, maxConfigBody+1))
	if err == nil {
		err = p.Verify()
	}
	if err != nil {
		writeError(w, r, err, req.bucket, "")
		return
	}
	if len(body) > maxConfigBody {
		writeError(w, r, s3err.New("InvalidArgument", "The CreateBucketConfiguration document is too large"), req.bucket, "")
		return
	}
	if len(strings.TrimSpace(string(body))) > 0 {
		var cfg createBucketConfiguration
		if err := xml.Unmarshal(body, &cfg); err != nil {
			writeError(w, r, s3err.New("MalformedXML", "The XML you provided was not well-formed or did not validate against our published schema"), req.bucket, "")
			return
		}
		if lc := cfg.LocationConstraint; lc != "" && lc != s.Region {
			writeError(w, r, s3err.New("InvalidLocationConstraint", "The specified location-constraint is not valid").
				With("LocationConstraint", lc), req.bucket, "")
			return
		}
	}
	if err := s.Store.CreateBucket(req.bucket); err != nil {
		writeError(w, r, err, req.bucket, "")
		return
	}
	w.Header().Set("Location", "/"+req.bucket)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) headBucket(w http.ResponseWriter, r *http.Request, req *request) {
	w.Header().Set("x-amz-bucket-region", s.Region)
	if _, err := s.Store.HeadBucket(req.bucket); err != nil {
		writeError(w, r, err, req.bucket, "")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) deleteBucket(w http.ResponseWriter, r *http.Request, req *request) {
	if err := s.Store.DeleteBucket(req.bucket); err != nil {
		writeError(w, r, err, req.bucket, "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteObjects deletes up to 1000 keys named in the request body and
// returns a per-key result. Like DeleteObject, deleting a missing key
// succeeds. In quiet mode only errors are reported.
func (s *Server) deleteObjects(w http.ResponseWriter, r *http.Request, req *request) {
	p, err := req.ver.Payload(r)
	if err != nil {
		writeError(w, r, err, req.bucket, "")
		return
	}
	body, err := io.ReadAll(io.LimitReader(p, maxDeleteBody+1))
	if err == nil {
		err = p.Verify()
	}
	if err != nil {
		writeError(w, r, err, req.bucket, "")
		return
	}
	malformed := s3err.New("MalformedXML", "The XML you provided was not well-formed or did not validate against our published schema")
	if len(body) > maxDeleteBody {
		writeError(w, r, malformed, req.bucket, "")
		return
	}
	var del deleteRequest
	if err := xml.Unmarshal(body, &del); err != nil || len(del.Objects) == 0 || len(del.Objects) > maxDeleteKeys {
		writeError(w, r, malformed, req.bucket, "")
		return
	}
	// A missing bucket fails the whole request, as in S3.
	if _, err := s.Store.HeadBucket(req.bucket); err != nil {
		writeError(w, r, err, req.bucket, "")
		return
	}
	out := deleteResult{Xmlns: s3Namespace}
	for _, o := range del.Objects {
		if err := s.Store.DeleteObject(req.bucket, o.Key); err != nil {
			e := apiError(err, req.bucket, o.Key)
			out.Errors = append(out.Errors, deleteErrorEntry{Key: o.Key, Code: e.Code, Message: e.Message})
			continue
		}
		if !del.Quiet {
			out.Deleted = append(out.Deleted, deletedEntry{Key: o.Key})
		}
	}
	writeXML(w, http.StatusOK, out)
}

// getBucketLocation is a stub for CLI region discovery, not an advertised
// operation: it reports the configured region (empty for us-east-1).
func (s *Server) getBucketLocation(w http.ResponseWriter, r *http.Request, req *request) {
	if _, err := s.Store.HeadBucket(req.bucket); err != nil {
		writeError(w, r, err, req.bucket, "")
		return
	}
	region := s.Region
	if region == "us-east-1" {
		region = ""
	}
	writeXML(w, http.StatusOK, locationConstraint{Xmlns: s3Namespace, Region: region})
}
