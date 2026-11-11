package s3api

import (
	"encoding/xml"
	"net/http"
	"time"
)

const (
	s3Namespace = "http://s3.amazonaws.com/doc/2006-03-01/"
	isoTime     = "2006-01-02T15:04:05.000Z"
	// ownerID and ownerName identify the single faux3 account.
	ownerID   = "75aa57f09aa0c8caeab4f8c24e99d10f8e7faeebf76c078efc7c6caea54ba06a"
	ownerName = "faux3"
)

type owner struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

var theOwner = owner{ID: ownerID, DisplayName: ownerName}

type listAllMyBucketsResult struct {
	XMLName xml.Name      `xml:"ListAllMyBucketsResult"`
	Xmlns   string        `xml:"xmlns,attr"`
	Owner   owner         `xml:"Owner"`
	Buckets []bucketEntry `xml:"Buckets>Bucket"`
}

type bucketEntry struct {
	Name         string `xml:"Name"`
	CreationDate string `xml:"CreationDate"`
}

type locationConstraint struct {
	XMLName xml.Name `xml:"LocationConstraint"`
	Xmlns   string   `xml:"xmlns,attr"`
	Region  string   `xml:",chardata"`
}

type createBucketConfiguration struct {
	XMLName            xml.Name `xml:"CreateBucketConfiguration"`
	LocationConstraint string   `xml:"LocationConstraint"`
}

type listBucketResult struct {
	XMLName               xml.Name       `xml:"ListBucketResult"`
	Xmlns                 string         `xml:"xmlns,attr"`
	Name                  string         `xml:"Name"`
	Prefix                string         `xml:"Prefix"`
	Delimiter             string         `xml:"Delimiter,omitempty"`
	MaxKeys               int            `xml:"MaxKeys"`
	KeyCount              int            `xml:"KeyCount"`
	IsTruncated           bool           `xml:"IsTruncated"`
	ContinuationToken     string         `xml:"ContinuationToken,omitempty"`
	NextContinuationToken string         `xml:"NextContinuationToken,omitempty"`
	StartAfter            string         `xml:"StartAfter,omitempty"`
	EncodingType          string         `xml:"EncodingType,omitempty"`
	Contents              []objectEntry  `xml:"Contents"`
	CommonPrefixes        []commonPrefix `xml:"CommonPrefixes"`
}

// listBucketResultV1 is the ListObjects (V1) response. It differs from
// listBucketResult: a caller-visible Marker and NextMarker in place of the
// continuation tokens, and no KeyCount. Marker is always present, even
// empty, as in S3.
type listBucketResultV1 struct {
	XMLName        xml.Name       `xml:"ListBucketResult"`
	Xmlns          string         `xml:"xmlns,attr"`
	Name           string         `xml:"Name"`
	Prefix         string         `xml:"Prefix"`
	Marker         string         `xml:"Marker"`
	NextMarker     string         `xml:"NextMarker,omitempty"`
	MaxKeys        int            `xml:"MaxKeys"`
	Delimiter      string         `xml:"Delimiter,omitempty"`
	EncodingType   string         `xml:"EncodingType,omitempty"`
	IsTruncated    bool           `xml:"IsTruncated"`
	Contents       []objectEntry  `xml:"Contents"`
	CommonPrefixes []commonPrefix `xml:"CommonPrefixes"`
}

type objectEntry struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
	Owner        *owner `xml:"Owner,omitempty"`
}

type commonPrefix struct {
	Prefix string `xml:"Prefix"`
}

func isoFormat(t time.Time) string { return t.UTC().Format(isoTime) }

// writeXML sends a success document with the XML declaration.
func writeXML(w http.ResponseWriter, status int, v any) {
	body, err := xml.Marshal(v)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(body)
}
