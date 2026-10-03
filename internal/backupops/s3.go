package backupops

import (
	"context"
	"errors"
	"io"
	"net/url"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Settings struct {
	Endpoint  string `json:"endpoint"`
	Bucket    string `json:"bucket"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
}
type s3Objects struct {
	store  *objectstore.RustFSStore
	client *s3.Client
	bucket string
}

func newS3(s S3Settings) (*s3Objects, error) {
	endpoint, err := url.Parse(s.Endpoint)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || s.Bucket == "" || s.AccessKey == "" || s.SecretKey == "" {
		return nil, errors.New("invalid explicit S3 settings")
	}
	store, err := objectstore.NewRustFSStore(s.Endpoint, s.AccessKey, s.SecretKey, s.Bucket)
	if err != nil {
		return nil, errors.New("object settings unavailable")
	}
	client := s3.New(s3.Options{Region: "us-east-1", BaseEndpoint: aws.String(s.Endpoint), UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider(s.AccessKey, s.SecretKey, "")})
	return &s3Objects{store: store, client: client, bucket: s.Bucket}, nil
}
func (s *s3Objects) WalkObjects(ctx context.Context, visit func(string, int64) error) error {
	pages := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), MaxKeys: aws.Int32(1000)}, func(o *s3.ListObjectsV2PaginatorOptions) { o.StopOnDuplicateToken = true })
	previous := ""
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return errors.New("object listing failed")
		}
		for _, object := range page.Contents {
			key := aws.ToString(object.Key)
			if key == "" || key <= previous || object.Size == nil {
				return errors.New("object listing unordered or invalid")
			}
			previous = key
			if err = visit(key, *object.Size); err != nil {
				return err
			}
		}
		if aws.ToBool(page.IsTruncated) && (!pages.HasMorePages() || aws.ToString(page.NextContinuationToken) == "") {
			return errors.New("object listing incomplete")
		}
	}
	return nil
}
func (s *s3Objects) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	return s.store.Open(ctx, key)
}
func (s *s3Objects) Put(ctx context.Context, key string, r io.ReadSeeker, size int64, digest string) error {
	return s.store.PutVerifiedReader(ctx, key, r, size, digest)
}
func (s *s3Objects) Empty(ctx context.Context) error {
	page, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), MaxKeys: aws.Int32(1)})
	if err != nil || len(page.Contents) != 0 || aws.ToBool(page.IsTruncated) {
		return errors.New("target object store not empty")
	}
	return nil
}
