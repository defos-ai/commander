package store

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Use an isolated test bucket or prefix and ensure work is settled before cleanup.
func (s *S3Store) CleanTestData(ctx context.Context) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	for _, namespace := range []string{"artifacts", "reports"} {
		prefix, err := s.objectKey(namespace)
		if err != nil {
			return err
		}
		pages := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
			Bucket: aws.String(s.bucket), Prefix: aws.String(prefix + "/"),
		})
		for pages.HasMorePages() {
			if err := ctx.Err(); err != nil {
				return err
			}
			page, err := pages.NextPage(ctx)
			if err != nil {
				return wrapError("list S3 test objects", err)
			}
			objects := make([]types.ObjectIdentifier, 0, len(page.Contents))
			for _, object := range page.Contents {
				objects = append(objects, types.ObjectIdentifier{Key: object.Key})
			}
			if len(objects) == 0 {
				continue
			}
			result, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: aws.String(s.bucket), Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)},
			})
			if err != nil {
				return wrapError("delete S3 test objects", err)
			}
			if len(result.Errors) > 0 {
				return fmt.Errorf("delete S3 test objects: %d objects were not deleted", len(result.Errors))
			}
		}
	}
	return ctx.Err()
}
