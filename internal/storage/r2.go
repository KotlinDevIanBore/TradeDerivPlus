// Package storage is the object store for KYC identity documents: Cloudflare R2,
// reached over the S3-compatible API.
//
// Deliberately small. It carries bytes and signs URLs; it knows nothing about
// submissions, users, or what an allowed document is. Key naming and validation
// live in the service, so this package cannot be the place a policy decision
// hides.
//
// The client construction is lifted from a neighbouring production codebase that
// has run R2 for months (see docs/kyc-r2-upload-investigation.md §1.1). Three
// things here are deliberately NOT lifted from it:
//
//   - A single presigned PUT rather than multipart. Multipart exists there to
//     move multi-gigabyte video; an identity document is a few megabytes, and
//     multipart's per-part presign cannot sign Content-Type or Content-Length —
//     which is exactly the guarantee wanted here.
//   - Short URL expiries, passed per call rather than fixed at construction. That
//     codebase signs downloads for four hours; an identity document should not sit
//     behind a URL that outlives the person looking at it.
//   - No constructor that reads the environment. Config arrives as a struct, so
//     internal/config stays the single place a variable is named and validated.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// Config is everything the client needs. Endpoint is optional: empty derives the
// account endpoint, which is what a normal R2 setup wants.
type Config struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	BucketName      string
	Endpoint        string
}

// PresignedRequest is a signed URL plus the headers the caller must replay.
//
// Headers matters and is not decoration: the signature covers them, so a client
// that omits or alters one gets a 403 from R2 rather than a successful upload of
// something unintended.
type PresignedRequest struct {
	URL       string      `json:"url"`
	Method    string      `json:"method"`
	Headers   http.Header `json:"headers"`
	ExpiresAt time.Time   `json:"expires_at"`
}

// ObjectMetadata is what the store knows about an object without reading it.
type ObjectMetadata struct {
	Size        int64
	ContentType string
	ETag        string
}

// R2Client talks to one bucket.
type R2Client struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// New builds a client. It refuses an incomplete config rather than producing one
// that fails later: internal/config already validates all-or-none at boot, and
// this is the second line of that same defence.
func New(cfg Config) (*R2Client, error) {
	if cfg.AccountID == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" || cfg.BucketName == "" {
		return nil, errors.New("storage: incomplete R2 configuration")
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)
	}

	// Region "auto" is what R2 expects; it has no regions, but SigV4 requires
	// something in the credential scope. Path-style addressing because R2 does not
	// serve virtual-host style on the account endpoint.
	awsCfg := aws.Config{
		Region:      "auto",
		Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})

	return &R2Client{client: client, presign: s3.NewPresignClient(client), bucket: cfg.BucketName}, nil
}

// PresignPut signs an upload URL for exactly one object of exactly one type and
// one length.
//
// Signing Content-Type and Content-Length is the whole reason this is a single
// PUT rather than multipart. Without them a valid URL is a licence to upload
// anything of any size: the server would have checked a declared type at presign
// and the browser could then send something else entirely, and the confirm step
// would be left to discover it after the bytes are already stored and paid for.
// With them, a mismatch is refused by R2 before a byte lands.
//
// It is still not sufficient on its own — a caller can declare image/jpeg and
// send JPEG-shaped bytes that are not an image — which is why confirm sniffs the
// stored object. This closes the cheap hole; the sniff closes the rest.
func (r *R2Client) PresignPut(ctx context.Context, key, contentType string, contentLength int64, expiry time.Duration) (*PresignedRequest, error) {
	out, err := r.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(r.bucket),
		Key:           aws.String(key),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(contentLength),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return nil, fmt.Errorf("presign put: %w", err)
	}
	return &PresignedRequest{
		URL:       out.URL,
		Method:    out.Method,
		Headers:   out.SignedHeader,
		ExpiresAt: time.Now().Add(expiry),
	}, nil
}

// PresignGet signs a read URL, minted per view and never stored.
//
// disposition sets Content-Disposition on the response. "inline" lets a reviewer
// look at the document in the browser instead of collecting identity documents in
// a downloads folder.
func (r *R2Client) PresignGet(ctx context.Context, key, disposition string, expiry time.Duration) (string, error) {
	in := &s3.GetObjectInput{Bucket: aws.String(r.bucket), Key: aws.String(key)}
	if disposition != "" {
		in.ResponseContentDisposition = aws.String(disposition)
	}
	out, err := r.presign.PresignGetObject(ctx, in, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("presign get: %w", err)
	}
	return out.URL, nil
}

// Head reports what is stored, or (nil, nil) when nothing is.
//
// Absent is not an error because the caller's question is "did the upload land?",
// and "no" is an answer rather than a failure. Every other failure — credentials,
// network, a bucket that does not exist — still comes back as one, so a caller
// cannot mistake a broken store for an empty key.
func (r *R2Client) Head(ctx context.Context, key string) (*ObjectMetadata, error) {
	out, err := r.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		m := &ObjectMetadata{}
		if out.ContentLength != nil {
			m.Size = *out.ContentLength
		}
		if out.ContentType != nil {
			m.ContentType = *out.ContentType
		}
		if out.ETag != nil {
			m.ETag = *out.ETag
		}
		return m, nil
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NotFound", "NoSuchKey", "404":
			return nil, nil
		}
	}
	return nil, fmt.Errorf("head object: %w", err)
}

// ReadHead returns the first n bytes, for content sniffing at confirm time.
//
// A ranged read rather than a full download: 512 bytes is all the sniffer needs,
// and pulling a 10 MiB document through the API to look at its first few bytes
// would be wasteful on every submission.
func (r *R2Client) ReadHead(ctx context.Context, key string, n int64) ([]byte, error) {
	if n <= 0 {
		return nil, errors.New("storage: read length must be positive")
	}
	out, err := r.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
		Range:  aws.String(fmt.Sprintf("bytes=0-%d", n-1)),
	})
	if err != nil {
		return nil, fmt.Errorf("read object head: %w", err)
	}
	defer out.Body.Close()
	// LimitReader as well as the Range header: a store that ignored the range must
	// not be able to pull an unbounded body into memory.
	b, err := io.ReadAll(io.LimitReader(out.Body, n))
	if err != nil {
		return nil, fmt.Errorf("read object head: %w", err)
	}
	return b, nil
}

// Delete removes an object. Deleting something already gone is not an error —
// both the orphan sweep and the failure path after a refused confirm can race
// with themselves, and neither should log a problem that is not one.
func (r *R2Client) Delete(ctx context.Context, key string) error {
	_, err := r.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.ErrorCode() {
			case "NotFound", "NoSuchKey", "404":
				return nil
			}
		}
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}
