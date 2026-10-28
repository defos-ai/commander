package fixtures

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/providers/s3/store"
)

type Store struct {
	Storage *store.S3Store
	Client  *awss3.Client
	Bucket  string
	Prefix  string
}

func OpenStore(t *testing.T) Store {
	t.Helper()
	raw := os.Getenv("COMMANDER_TEST_S3_URL")
	if raw == "" {
		t.Skip("set COMMANDER_TEST_S3_URL to run S3 provider integration tests")
	}
	location, _, err := store.ParseURL(raw)
	if err != nil {
		t.Fatalf("COMMANDER_TEST_S3_URL must identify an S3 bucket and optional prefix")
	}
	prefix := strings.Trim(location.Path, "/")
	if prefix != "" {
		prefix += "/"
	}
	prefix += "provider-test-" + id.New() + "/"
	location.Path = "/" + prefix
	storage, err := store.Open(t.Context(), location.String())
	if err != nil {
		t.Fatal(err)
	}
	var options []func(*config.LoadOptions) error
	if location.User != nil {
		secret, _ := location.User.Password()
		options = append(options, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(location.User.Username(), secret, "")))
	}
	cfg, err := config.LoadDefaultConfig(t.Context(), options...)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	client := awss3.NewFromConfig(cfg, func(options *awss3.Options) {
		if endpoint := os.Getenv("AWS_ENDPOINT_URL_S3"); endpoint != "" {
			options.BaseEndpoint = aws.String(endpoint)
		}
		options.UsePathStyle, _ = strconv.ParseBool(os.Getenv("S3_USE_PATH_STYLE"))
	})
	f := Store{Storage: storage, Client: client, Bucket: location.Host, Prefix: prefix}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		pages := awss3.NewListObjectsV2Paginator(client, &awss3.ListObjectsV2Input{Bucket: aws.String(f.Bucket), Prefix: aws.String(prefix)})
		for pages.HasMorePages() {
			page, err := pages.NextPage(ctx)
			if err != nil {
				t.Errorf("list S3 test cleanup: %v", err)
				return
			}
			objects := make([]types.ObjectIdentifier, 0, len(page.Contents))
			for _, object := range page.Contents {
				objects = append(objects, types.ObjectIdentifier{Key: object.Key})
			}
			if len(objects) == 0 {
				continue
			}
			result, err := client.DeleteObjects(ctx, &awss3.DeleteObjectsInput{Bucket: aws.String(f.Bucket), Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)}})
			if err != nil {
				t.Errorf("clean S3 test objects: %v", err)
				return
			}
			if len(result.Errors) != 0 {
				t.Errorf("clean S3 test objects: %+v", result.Errors)
			}
		}
	})
	return f
}
