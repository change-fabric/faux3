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

// deleteRequest is the DeleteObjects request body.
type deleteRequest struct {
	XMLName xml.Name       `xml:"Delete"`
	Quiet   bool           `xml:"Quiet"`
	Objects []deleteTarget `xml:"Object"`
}

type deleteTarget struct {
	Key string `xml:"Key"`
	// VersionId is parsed but ignored: faux3 has no versioning.
	VersionId string `xml:"VersionId"`
}

// deleteResult is the DeleteObjects response. In quiet mode only Errors
// are returned.
type deleteResult struct {
	XMLName xml.Name           `xml:"DeleteResult"`
	Xmlns   string             `xml:"xmlns,attr"`
	Deleted []deletedEntry     `xml:"Deleted"`
	Errors  []deleteErrorEntry `xml:"Error"`
}

type deletedEntry struct {
	Key string `xml:"Key"`
}

type deleteErrorEntry struct {
	Key     string `xml:"Key"`
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
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
