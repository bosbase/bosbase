package filesystem

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bosbase/bosbase-enterprise/tools/filesystem/blob"
	"github.com/bosbase/bosbase-enterprise/tools/filesystem/internal/s3blob"
	"github.com/bosbase/bosbase-enterprise/tools/filesystem/internal/s3blob/s3"
	"github.com/gabriel-vasile/mimetype"
)

// S3Config describes the connection to an S3 compatible object storage
// (AWS S3, Cloudflare R2, Google Cloud Storage interoperability, MinIO, Backblaze B2, ...).
type S3Config struct {
	Bucket         string
	Region         string
	Endpoint       string
	AccessKey      string
	SecretKey      string
	ForcePathStyle bool

	// HTTPClient is an optional custom HTTP client used for the S3 requests.
	HTTPClient *http.Client
}

func (cfg S3Config) client() *s3.S3 {
	c := &s3.S3{
		Bucket:       cfg.Bucket,
		Region:       cfg.Region,
		Endpoint:     cfg.Endpoint,
		AccessKey:    cfg.AccessKey,
		SecretKey:    cfg.SecretKey,
		UsePathStyle: cfg.ForcePathStyle,
	}
	if cfg.HTTPClient != nil {
		c.Client = cfg.HTTPClient
	}
	return c
}

// NewS3FromConfig initializes an S3 filesystem instance from the provided config.
//
// NB! Make sure to call `Close()` after you are done working with it.
func NewS3FromConfig(cfg S3Config) (*System, error) {
	drv, err := s3blob.New(cfg.client())
	if err != nil {
		return nil, err
	}

	return &System{ctx: context.Background(), bucket: blob.NewBucket(drv)}, nil
}

// PresignS3URL returns an AWS Signature V4 presigned URL that allows
// executing the specified method (eg. GET or PUT) on the object key
// without credentials until the URL expires.
// S3ObjectURL returns the unsigned URL of an object (usable as public link when
// the bucket objects are publicly readable).
func S3ObjectURL(cfg S3Config, key string) string {
	return cfg.client().URL(key)
}

func PresignS3URL(cfg S3Config, method string, key string, expires time.Duration) (string, error) {
	return presignS3URL(cfg, method, key, expires, time.Now())
}

func presignS3URL(cfg S3Config, method string, key string, expires time.Duration, signTime time.Time) (string, error) {
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return "", errors.New("missing S3 credentials")
	}
	if expires <= 0 {
		expires = 15 * time.Minute
	}
	if expires > 7*24*time.Hour {
		expires = 7 * 24 * time.Hour
	}
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}

	u, err := url.Parse(cfg.client().URL(key))
	if err != nil {
		return "", err
	}

	now := signTime.UTC()
	amzDate := now.Format("20060102T150405Z")
	shortDate := now.Format("20060102")
	scope := shortDate + "/" + region + "/s3/aws4_request"

	query := map[string]string{
		"X-Amz-Algorithm":     "AWS4-HMAC-SHA256",
		"X-Amz-Credential":    cfg.AccessKey + "/" + scope,
		"X-Amz-Date":          amzDate,
		"X-Amz-Expires":       strconv.Itoa(int(expires.Seconds())),
		"X-Amz-SignedHeaders": "host",
	}
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = s3QueryEscape(k) + "=" + s3QueryEscape(query[k])
	}
	canonicalQuery := strings.Join(parts, "&")

	canonicalRequest := strings.Join([]string{
		strings.ToUpper(method),
		u.EscapedPath(),
		canonicalQuery,
		"host:" + u.Host + "\n",
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")

	hashedRequest := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hex.EncodeToString(hashedRequest[:]),
	}, "\n")

	signingKey := s3HMAC([]byte("AWS4"+cfg.SecretKey), shortDate)
	signingKey = s3HMAC(signingKey, region)
	signingKey = s3HMAC(signingKey, "s3")
	signingKey = s3HMAC(signingKey, "aws4_request")
	signature := hex.EncodeToString(s3HMAC(signingKey, stringToSign))

	u.RawQuery = canonicalQuery + "&X-Amz-Signature=" + signature

	return u.String(), nil
}

func s3HMAC(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// s3QueryEscape escapes a query value according to the AWS UriEncode rules.
func s3QueryEscape(s string) string {
	escaped := url.QueryEscape(s)
	escaped = strings.ReplaceAll(escaped, "+", "%20")
	escaped = strings.ReplaceAll(escaped, "%7E", "~")
	return escaped
}

// UploadReader streams the content of r to the fileKey location with the provided content type
// (detected from the content when empty).
func (s *System) UploadReader(r io.Reader, fileKey string, contentType string) error {
	br := bufio.NewReaderSize(r, 3072)
	if contentType == "" {
		head, _ := br.Peek(3072)
		contentType = mimetype.Detect(head).String()
	}

	w, err := s.bucket.NewWriter(s.ctx, fileKey, &blob.WriterOptions{ContentType: contentType})
	if err != nil {
		return err
	}

	if _, err := io.Copy(w, br); err != nil {
		return errors.Join(err, w.Close())
	}

	return w.Close()
}
