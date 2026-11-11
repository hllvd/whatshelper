//go:build integration

package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"whatshelper/internal/config"
	"whatshelper/internal/core"
	"whatshelper/internal/store/dynamo"
	"whatshelper/internal/store/s3blob"
	"whatshelper/internal/store/storetest"
)

func TestDynamoStoreContract_Integration(t *testing.T) {
	cfg := config.LoadFromEnv()
	ctx := context.Background()

	client, err := dynamo.NewClient(ctx, cfg.DynamoDBEndpoint, cfg.S3Region, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		t.Fatalf("dynamo.NewClient failed: %v", err)
	}

	storetest.RunStoreContractTests(t, func(t *testing.T, clock core.Clock) core.Store {
		tableName := fmt.Sprintf("whatshelper-test-%d", time.Now().UnixNano())
		if err := dynamo.EnsureTable(ctx, client, tableName); err != nil {
			t.Fatalf("EnsureTable(%s) failed: %v", tableName, err)
		}
		t.Cleanup(func() {
			_, _ = client.DeleteTable(context.Background(), &dynamodb.DeleteTableInput{
				TableName: aws.String(tableName),
			})
		})
		return dynamo.NewStore(client, tableName, clock)
	})
}

func TestS3BlobStoreContract_Integration(t *testing.T) {
	cfg := config.LoadFromEnv()
	ctx := context.Background()

	client, err := s3blob.NewClient(ctx, cfg.S3Endpoint, cfg.S3Region, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		t.Fatalf("s3blob.NewClient failed: %v", err)
	}

	storetest.RunBlobStoreContractTests(t, func(t *testing.T) core.BlobStore {
		bucket := fmt.Sprintf("whatshelper-test-%d", time.Now().UnixNano())
		if err := s3blob.EnsureBucket(ctx, client, bucket); err != nil {
			t.Fatalf("EnsureBucket(%s) failed: %v", bucket, err)
		}
		return s3blob.NewStore(client, bucket)
	})
}
