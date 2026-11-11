package s3blob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"whatshelper/internal/core"
)

// Store implementa core.BlobStore sobre Amazon S3 ou MinIO compatível.
type Store struct {
	client *s3.Client
	bucket string
}

// NewClient inicializa o cliente S3 com UsePathStyle=true para compatibilidade com MinIO local.
func NewClient(ctx context.Context, endpoint, region, accessKey, secretKey string) (*s3.Client, error) {
	if region == "" {
		region = "us-east-1"
	}
	loadOpts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}
	if accessKey != "" || secretKey != "" {
		loadOpts = append(loadOpts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		))
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("falha ao carregar config AWS para S3: %w", err)
	}

	var clientOpts []func(*s3.Options)
	clientOpts = append(clientOpts, func(o *s3.Options) {
		o.UsePathStyle = true
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})

	return s3.NewFromConfig(awsCfg, clientOpts...), nil
}

// NewStore cria um BlobStore conectado ao bucket especificado.
func NewStore(client *s3.Client, bucket string) *Store {
	return &Store{
		client: client,
		bucket: bucket,
	}
}

// EnsureBucket garante que o bucket exista e ativa o versionamento (usado em inicialização/testes).
func EnsureBucket(ctx context.Context, client *s3.Client, bucket string) error {
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		_, createErr := client.CreateBucket(ctx, &s3.CreateBucketInput{
			Bucket: aws.String(bucket),
		})
		if createErr != nil {
			var owned *types.BucketAlreadyOwnedByYou
			var exists *types.BucketAlreadyExists
			if !errors.As(createErr, &owned) && !errors.As(createErr, &exists) {
				return fmt.Errorf("falha ao criar bucket %s: %w", bucket, createErr)
			}
		}
	}
	return nil
}

func (s *Store) Put(ctx context.Context, key string, data []byte, contentType string) error {
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	_, err := s.client.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("s3 Put(%s) falhou: %w", key, err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 Get(%s) falhou: %w", key, err)
	}
	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("s3 ReadAll(%s) falhou: %w", key, err)
	}
	return data, nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("s3 Delete(%s) falhou: %w", key, err)
	}
	return nil
}

func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("s3 List(%s) falhou: %w", prefix, err)
		}
		for _, obj := range page.Contents {
			if obj.Key != nil {
				keys = append(keys, *obj.Key)
			}
		}
	}
	return keys, nil
}

func (s *Store) ListVersions(ctx context.Context, key string) ([]core.BlobVersion, error) {
	var versions []core.BlobVersion
	paginator := s3.NewListObjectVersionsPaginator(s.client, &s3.ListObjectVersionsInput{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(key),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("s3 ListVersions(%s) falhou: %w", key, err)
		}
		for _, v := range page.Versions {
			if aws.ToString(v.Key) != key {
				continue
			}
			versions = append(versions, core.BlobVersion{
				VersionID:    aws.ToString(v.VersionId),
				IsLatest:     aws.ToBool(v.IsLatest),
				LastModified: aws.ToTime(v.LastModified),
				Size:         aws.ToInt64(v.Size),
			})
		}
	}
	return versions, nil
}

func (s *Store) GetVersion(ctx context.Context, key, versionID string) ([]byte, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}
	if versionID != "" {
		input.VersionId = aws.String(versionID)
	}
	out, err := s.client.GetObject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("s3 GetVersion(%s, %s) falhou: %w", key, versionID, err)
	}
	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("s3 ReadAll(%s) falhou: %w", key, err)
	}
	return data, nil
}
