package store

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

var (
	ErrObjectExists   = errors.New("S3 object already exists")
	ErrObjectNotFound = errors.New("S3 object not found")
)

type S3Store struct {
	client *s3.Client
	bucket string
	prefix string
}

func Open(ctx context.Context, rawURL string) (*S3Store, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	location, prefix, err := ParseURL(rawURL)
	if err != nil {
		return nil, err
	}
	pathStyle := false
	if value := os.Getenv("S3_USE_PATH_STYLE"); value != "" {
		pathStyle, err = strconv.ParseBool(value)
		if err != nil {
			return nil, errors.New("S3_USE_PATH_STYLE must be a boolean")
		}
	}
	var options []func(*config.LoadOptions) error
	if location.User != nil {
		secret, _ := location.User.Password()
		// URL credentials are a complete static pair, independent of ambient tokens.
		options = append(options, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(location.User.Username(), secret, "")))
	}
	cfg, err := config.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, wrapError("load S3 configuration", err)
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	client := s3.NewFromConfig(cfg, func(options *s3.Options) {
		options.UsePathStyle = pathStyle
		options.Retryer = retry.AddWithErrorCodes(options.Retryer, "ConditionalRequestConflict")
	})
	return &S3Store{client: client, bucket: location.Host, prefix: prefix}, nil
}

func (s *S3Store) Put(ctx context.Context, key string, body io.ReadSeeker, size int64, mediaType, sha256Hex string) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	key, err := s.objectKey(key)
	if err != nil {
		return err
	}
	if body == nil || size < 0 {
		return errors.New("S3 put requires a body and nonnegative size")
	}
	digest, err := hex.DecodeString(sha256Hex)
	if err != nil || len(digest) != 32 {
		return errors.New("S3 put requires a SHA256 digest")
	}
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key), Body: body,
		ContentLength: aws.Int64(size), ContentType: aws.String(mediaType),
		IfNoneMatch: aws.String("*"), ChecksumSHA256: aws.String(base64.StdEncoding.EncodeToString(digest)),
	})
	if err != nil {
		var response *smithyhttp.ResponseError
		if errors.As(err, &response) && response.HTTPStatusCode() == 412 {
			return ErrObjectExists
		}
		return wrapError("put S3 object", err)
	}
	return nil
}

func (s *S3Store) Get(ctx context.Context, key string) ([]byte, error) {
	return s.get(ctx, key, nil)
}

func (s *S3Store) GetExact(ctx context.Context, key string, size int64) ([]byte, error) {
	if size < 0 || size == math.MaxInt64 {
		return nil, errors.New("S3 object size is invalid")
	}
	return s.get(ctx, key, &size)
}

func (s *S3Store) get(ctx context.Context, key string, size *int64) ([]byte, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	key, err := s.objectKey(key)
	if err != nil {
		return nil, err
	}
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		var api smithy.APIError
		if errors.As(err, &api) && api.ErrorCode() == "NoSuchKey" {
			return nil, ErrObjectNotFound
		}
		return nil, wrapError("get S3 object", err)
	}
	defer result.Body.Close()
	var reader io.Reader = result.Body
	if size != nil {
		if result.ContentLength != nil && *result.ContentLength != *size {
			return nil, errors.New("S3 object does not match its expected size")
		}
		reader = io.LimitReader(reader, *size+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, wrapError("read S3 object", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if size != nil && int64(len(data)) != *size {
		return nil, errors.New("S3 object does not match its expected size")
	}
	return data, nil
}

func (s *S3Store) objectKey(key string) (string, error) {
	if s == nil || s.client == nil {
		return "", errors.New("S3 store is not configured")
	}
	if !validKey(key) {
		return "", errors.New("S3 object key must be a relative path without empty, dot, or parent segments")
	}
	if s.prefix != "" {
		key = s.prefix + "/" + key
	}
	if len(key) > 1024 {
		return "", errors.New("S3 object key exceeds 1024 bytes")
	}
	return key, nil
}

// ParseURL validates an S3 location and returns its decoded prefix. The returned
// URL may contain credentials and must not be included in diagnostics.
func ParseURL(rawURL string) (*url.URL, string, error) {
	value, err := url.Parse(escapePasswordSlashes(rawURL))
	if err != nil {
		return nil, "", errors.New("invalid S3 URL; expected s3://[access-key:secret-key@]bucket[/prefix]")
	}
	if value.User != nil {
		key := value.User.Username()
		secret, present := value.User.Password()
		if !present || key == "" || secret == "" || strings.ContainsFunc(key, unicode.IsControl) || strings.ContainsFunc(secret, unicode.IsControl) {
			return nil, "", errors.New("S3 URL credentials require a nonempty access key and secret key without control characters")
		}
	}
	if value.Scheme != "s3" || value.Opaque != "" || value.RawQuery != "" || value.ForceQuery || strings.Contains(rawURL, "#") {
		return nil, "", errors.New("invalid S3 URL; expected s3://[access-key:secret-key@]bucket[/prefix] without query or fragment")
	}
	if !validBucket(value.Host) {
		return nil, "", errors.New("S3 URL requires a valid bucket name")
	}
	prefix := strings.TrimSuffix(strings.TrimPrefix(value.Path, "/"), "/")
	if prefix != "" && !validKey(prefix) {
		return nil, "", errors.New("S3 URL prefix must not contain empty, dot, or parent segments")
	}
	return value, prefix, nil
}

func escapePasswordSlashes(rawURL string) string {
	rest, ok := strings.CutPrefix(rawURL, "s3://")
	if !ok {
		return rawURL
	}
	userinfo, destination, found := strings.Cut(rest, "@")
	key, secret, pair := strings.Cut(userinfo, ":")
	if !found || !pair || strings.Contains(key, "/") || !strings.Contains(secret, "/") {
		return rawURL
	}
	// A slash in an AWS secret is credential data rather than a bucket-path
	// separator. Leave existing escapes intact for net/url to decode once.
	return "s3://" + key + ":" + strings.ReplaceAll(secret, "/", "%2F") + "@" + destination
}

func validBucket(bucket string) bool {
	if len(bucket) < 3 || len(bucket) > 63 || net.ParseIP(bucket) != nil || strings.Contains(bucket, "..") {
		return false
	}
	for index, ch := range bucket {
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' {
			continue
		}
		if (ch != '-' && ch != '.') || index == 0 || index == len(bucket)-1 {
			return false
		}
	}
	return true
}

func validKey(key string) bool {
	if strings.ContainsAny(key, "\\") || strings.ContainsFunc(key, unicode.IsControl) {
		return false
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("S3 storage requires a context")
	}
	return ctx.Err()
}

type operationError struct {
	op    string
	cause error
}

func wrapError(op string, err error) error { return &operationError{op: op, cause: err} }

func (e *operationError) Error() string {
	if errors.Is(e.cause, context.Canceled) || errors.Is(e.cause, context.DeadlineExceeded) {
		return e.op + ": request canceled or timed out"
	}
	var api smithy.APIError
	if errors.As(e.cause, &api) {
		code := api.ErrorCode()
		if code != "" && !strings.ContainsFunc(code, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_')
		}) {
			return e.op + ": " + code
		}
	}
	var response *smithyhttp.ResponseError
	if errors.As(e.cause, &response) {
		return fmt.Sprintf("%s: HTTP %d", e.op, response.HTTPStatusCode())
	}
	return e.op + ": request failed"
}

func (e *operationError) Unwrap() error { return e.cause }
