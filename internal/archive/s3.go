package archive

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net/http"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Options address a bucket on S3 or an S3 compatible store (MinIO).
type S3Options struct {
	Endpoint  string // host[:port]
	Bucket    string
	Region    string
	AccessKey string
	SecretKey string
	UseSSL    bool
	Insecure  bool // skip TLS verification
}

// S3Store writes objects into one bucket.
type S3Store struct {
	client *minio.Client
	bucket string
	region string
}

// NewS3Store returns a store for the bucket; it does not contact the server.
func NewS3Store(o S3Options) (*S3Store, error) {
	opts := &minio.Options{
		Creds:  credentials.NewStaticV4(o.AccessKey, o.SecretKey, ""),
		Secure: o.UseSSL,
		Region: o.Region,
	}
	if o.Insecure {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in via S3_INSECURE
		opts.Transport = tr
	}
	c, err := minio.New(o.Endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("s3 client: %w", err)
	}
	return &S3Store{client: c, bucket: o.Bucket, region: o.Region}, nil
}

// Put writes body as a JSON object under key.
func (s *S3Store) Put(ctx context.Context, key string, body []byte) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(body), int64(len(body)),
		minio.PutObjectOptions{ContentType: "application/json"})
	return err
}

// EnsureBucket checks that the bucket exists, and creates it if create is set.
func (s *S3Store) EnsureBucket(ctx context.Context, create bool) error {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("check bucket %s: %w", s.bucket, err)
	}
	if ok {
		return nil
	}
	if !create {
		return fmt.Errorf("bucket %s does not exist (set S3_CREATE_BUCKET=true to create it)", s.bucket)
	}
	if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: s.region}); err != nil {
		return fmt.Errorf("create bucket %s: %w", s.bucket, err)
	}
	return nil
}
