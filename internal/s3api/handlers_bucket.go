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

// getBucketLocation returns the LocationConstraint recorded for the bucket
// when it was created. As in S3, the us-east-1 region is reported as an
// empty constraint.
func (s *Server) getBucketLocation(w http.ResponseWriter, r *http.Request, req *request) {
	b, err := s.Store.HeadBucket(req.bucket)
	if err != nil {
		writeError(w, r, err, req.bucket, "")
		return
	}
	region := b.Region
	if region == "us-east-1" {
		region = ""
	}
	writeXML(w, http.StatusOK, locationConstraint{Xmlns: s3Namespace, Region: region})
}
