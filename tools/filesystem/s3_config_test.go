package filesystem

import (
	"net/url"
	"testing"
	"time"
)

// Test vector from the AWS documentation
// https://docs.aws.amazon.com/AmazonS3/latest/API/sigv4-query-string-auth.html
func TestPresignS3URLAWSExample(t *testing.T) {
	cfg := S3Config{
		Bucket:    "examplebucket",
		Region:    "us-east-1",
		Endpoint:  "https://s3.amazonaws.com",
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	signTime := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

	raw, err := presignS3URL(cfg, "GET", "test.txt", 86400*time.Second, signTime)
	if err != nil {
		t.Fatal(err)
	}

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "examplebucket.s3.amazonaws.com" || u.Path != "/test.txt" {
		t.Fatalf("unexpected url %s", raw)
	}

	expected := "aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"
	if sig := u.Query().Get("X-Amz-Signature"); sig != expected {
		t.Fatalf("expected signature %s, got %s (%s)", expected, sig, raw)
	}
}

func TestPresignS3URLPathStyle(t *testing.T) {
	cfg := S3Config{
		Bucket:         "b",
		Endpoint:       "http://127.0.0.1:9000",
		AccessKey:      "a",
		SecretKey:      "s",
		ForcePathStyle: true,
	}
	raw, err := PresignS3URL(cfg, "PUT", "dir/my file.png", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	if u.Host != "127.0.0.1:9000" || u.EscapedPath() != "/b/dir/my%20file.png" {
		t.Fatalf("unexpected url %s", raw)
	}
	if u.Query().Get("X-Amz-Expires") != "3600" || u.Query().Get("X-Amz-Credential") == "" {
		t.Fatalf("missing presign params %s", raw)
	}
	if _, err := PresignS3URL(S3Config{Bucket: "b", Endpoint: "x"}, "GET", "k", 0); err == nil {
		t.Fatal("expected missing credentials error")
	}
}
