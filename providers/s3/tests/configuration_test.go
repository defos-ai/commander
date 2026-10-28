package s3_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/defos-ai/commander/providers/s3/store"
	"github.com/defos-ai/commander/providers/s3/tests/fixtures"
)

func TestS3Configuration(t *testing.T) {
	fixtures.IsolateAWSConfiguration(t)
	for _, raw := range []string{
		"https://bucket/prefix", "s3://", "s3://bucket:9000/prefix", "s3://Bucket/prefix", "s3://bucket/%zz",
		"s3://user@bucket/prefix", "s3://user:@bucket/prefix", "s3://:secret-marker@bucket/prefix",
		"s3://user:secret-marker%00@bucket/prefix", "s3://user%0A:secret-marker@bucket/prefix",
		"s3://user:secret-marker%7F@bucket/prefix", "s3://user:secret-marker%zz@bucket/prefix",
		"s3://user:secret-marker%C2%85@bucket/prefix",
		"s3://:secret-marker/raw@bucket/prefix", "s3://user:secret-marker/raw%zz@bucket/prefix",
		"s3://user:secret-marker/raw%00@bucket/prefix", "s3://user:secret-marker/raw@Bucket/prefix",
		"s3://user:secret-marker/raw@bucket/prefix?query=value", "s3://user:secret-marker/raw@bucket/prefix#fragment",
		"s3://bucket/prefix?token=secret-marker", "s3://bucket/prefix#secret-marker",
		"s3://bucket/../outside", "s3://bucket/prefix/%2e%2e/outside", "s3://bucket/prefix//child", "s3://bucket/prefix/%5cchild",
	} {
		if _, err := store.Open(t.Context(), raw); err == nil || strings.Contains(err.Error(), "secret-marker") {
			t.Fatalf("invalid S3 configuration accepted or leaked a secret: %v", err)
		}
	}
	t.Setenv("S3_USE_PATH_STYLE", "invalid-secret-marker")
	if _, err := store.Open(t.Context(), "s3://bucket/prefix"); err == nil || strings.Contains(err.Error(), "secret-marker") {
		t.Fatalf("invalid path-style setting accepted or leaked its value: %v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.Open(cancelled, "s3://bucket/prefix"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled constructor: %v", err)
	}
}

func TestS3URLLocations(t *testing.T) {
	for _, test := range []struct {
		name, raw, prefix, accessKey, secretKey string
	}{
		{name: "ordinary prefix", raw: "s3://bucket/prefix", prefix: "prefix"},
		{name: "prefix contains credential punctuation", raw: "s3://bucket/prefix:key/child@label", prefix: "prefix:key/child@label"},
		{name: "prefix contains multiple at signs", raw: "s3://bucket/prefix@label/child:part@other", prefix: "prefix@label/child:part@other"},
		{name: "raw secret with prefix at sign", raw: "s3://key:raw/secret+=@bucket/prefix@label", prefix: "prefix@label", accessKey: "key", secretKey: "raw/secret+="},
		{name: "raw secret with encoded prefix", raw: "s3://key:raw/secret@bucket/prefix%2Fchild", prefix: "prefix/child", accessKey: "key", secretKey: "raw/secret"},
		{name: "encoded access key with raw secret", raw: "s3://key%40id:raw/secret@bucket/prefix", prefix: "prefix", accessKey: "key@id", secretKey: "raw/secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			location, prefix, err := store.ParseURL(test.raw)
			if err != nil {
				t.Fatal(err)
			}
			if location.Host != "bucket" || prefix != test.prefix {
				t.Fatal("parsing changed the bucket or prefix")
			}
			if test.accessKey == "" {
				if location.User != nil {
					t.Fatal("path punctuation was interpreted as credentials")
				}
				return
			}
			if location.User == nil {
				t.Fatal("URL credentials were not retained")
			}
			secret, present := location.User.Password()
			if location.User.Username() != test.accessKey || !present || secret != test.secretKey {
				t.Fatal("parsing changed the URL credentials")
			}
		})
	}
}
