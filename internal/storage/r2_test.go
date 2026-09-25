package storage

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"
)

// testClient builds a real client against an endpoint nothing listens on.
//
// Presigning is a local computation — SigV4 over the request — so these tests
// make no network call and need no bucket. That is the technique the neighbouring
// codebase uses for the same reason: the interesting behaviour is what ends up in
// the signature, and asserting on it does not require a server.
func testClient(t *testing.T) *R2Client {
	t.Helper()
	c, err := New(Config{
		AccountID:       "acct",
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		BucketName:      "bucket",
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

func TestNewDerivesTheR2Endpoint(t *testing.T) {
	c := testClient(t)
	got, err := c.PresignGet(context.Background(), "kyc/u/d.jpg", "", time.Minute)
	if err != nil {
		t.Fatalf("presign get: %v", err)
	}
	if !strings.HasPrefix(got, "https://acct.r2.cloudflarestorage.com/") {
		t.Fatalf("URL %q does not use the account's R2 endpoint", got)
	}
	// Path-style: the bucket is in the path, not the host. R2 does not serve
	// virtual-host style on the account endpoint, so getting this wrong produces
	// requests to a hostname that does not resolve.
	if !strings.Contains(got, "/bucket/kyc/u/d.jpg") {
		t.Fatalf("URL %q is not path-style addressed", got)
	}
}

func TestNewHonoursAnExplicitEndpoint(t *testing.T) {
	c, err := New(Config{
		AccountID: "acct", AccessKeyID: "key", SecretAccessKey: "secret",
		BucketName: "bucket", Endpoint: "https://example.invalid",
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	got, err := c.PresignGet(context.Background(), "k", "", time.Minute)
	if err != nil {
		t.Fatalf("presign get: %v", err)
	}
	if !strings.HasPrefix(got, "https://example.invalid/") {
		t.Fatalf("explicit endpoint ignored: %q", got)
	}
}

func TestNewRefusesAnIncompleteConfig(t *testing.T) {
	full := Config{AccountID: "a", AccessKeyID: "k", SecretAccessKey: "s", BucketName: "b"}
	for _, tc := range []struct {
		name  string
		blank func(*Config)
	}{
		{"AccountID", func(c *Config) { c.AccountID = "" }},
		{"AccessKeyID", func(c *Config) { c.AccessKeyID = "" }},
		{"SecretAccessKey", func(c *Config) { c.SecretAccessKey = "" }},
		{"BucketName", func(c *Config) { c.BucketName = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := full
			tc.blank(&cfg)
			if _, err := New(cfg); err == nil {
				t.Fatalf("a config missing %s produced a client", tc.name)
			}
		})
	}
}

// TestPresignPutSignsTypeAndLength is the assertion the whole single-PUT decision
// rests on.
//
// If Content-Type and Content-Length are not in the signature, a valid upload URL
// is a licence to store anything of any size, and the type checked at presign
// time means nothing. The neighbouring codebase's multipart presign cannot sign
// either, which is why it was not lifted.
func TestPresignPutSignsTypeAndLength(t *testing.T) {
	c := testClient(t)
	req, err := c.PresignPut(context.Background(), "kyc/u/d.jpg", "image/jpeg", 1234, 10*time.Minute)
	if err != nil {
		t.Fatalf("presign put: %v", err)
	}
	if req.Method != "PUT" {
		t.Fatalf("method %q, want PUT", req.Method)
	}

	u, err := url.Parse(req.URL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	signed := u.Query().Get("X-Amz-SignedHeaders")
	for _, want := range []string{"content-length", "content-type"} {
		if !strings.Contains(signed, want) {
			t.Fatalf("X-Amz-SignedHeaders %q does not cover %s: an upload could ignore it", signed, want)
		}
	}

	// The caller is told which headers to replay; a client that does not send
	// them gets a 403 rather than an upload.
	if got := req.Headers.Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("Content-Type header %q, want image/jpeg", got)
	}
	if got := req.Headers.Get("Content-Length"); got != "1234" {
		t.Fatalf("Content-Length header %q, want 1234", got)
	}
}

func TestPresignPutExpiryIsCarried(t *testing.T) {
	c := testClient(t)
	before := time.Now()
	req, err := c.PresignPut(context.Background(), "k", "image/png", 10, 7*time.Minute)
	if err != nil {
		t.Fatalf("presign put: %v", err)
	}
	u, _ := url.Parse(req.URL)
	if got := u.Query().Get("X-Amz-Expires"); got != "420" {
		t.Fatalf("X-Amz-Expires %q, want 420 (7 minutes)", got)
	}
	// ExpiresAt is what the client shows a user, so it must reflect the same
	// window rather than being a placeholder.
	if req.ExpiresAt.Before(before.Add(6*time.Minute)) || req.ExpiresAt.After(before.Add(8*time.Minute)) {
		t.Fatalf("ExpiresAt %v is not ~7 minutes from now", req.ExpiresAt)
	}
}

// TestPresignGetCarriesTheDisposition — a reviewer should see the document, not
// silently accumulate identity documents in a downloads folder.
func TestPresignGetCarriesTheDisposition(t *testing.T) {
	c := testClient(t)
	got, err := c.PresignGet(context.Background(), "kyc/u/d.pdf", "inline", 5*time.Minute)
	if err != nil {
		t.Fatalf("presign get: %v", err)
	}
	u, _ := url.Parse(got)
	if u.Query().Get("response-content-disposition") != "inline" {
		t.Fatalf("URL %q does not set response-content-disposition", got)
	}
	if u.Query().Get("X-Amz-Expires") != "300" {
		t.Fatalf("expiry not carried: %q", got)
	}
}

// TestReadHeadRefusesANonPositiveLength guards the one argument that could turn a
// bounded read into an unbounded one.
func TestReadHeadRefusesANonPositiveLength(t *testing.T) {
	c := testClient(t)
	if _, err := c.ReadHead(context.Background(), "k", 0); err == nil {
		t.Fatal("a zero-length ranged read was accepted")
	}
}

// --- the stub -------------------------------------------------------------
//
// The stub exists so CI and local development need no bucket, which makes it
// worth testing: a stub that accepted uploads the real store would refuse would
// let the suite prove the wrong thing.

func TestStubEnforcesWhatTheSignatureCovers(t *testing.T) {
	s := NewStub()
	ctx := context.Background()
	const key = "kyc/u/d.jpg"

	if err := s.PutForTest(key, "image/jpeg", []byte("abc")); err == nil {
		t.Fatal("the stub accepted an upload to a key nothing had signed")
	}

	if _, err := s.PresignPut(ctx, key, "image/jpeg", 3, time.Minute); err != nil {
		t.Fatalf("presign: %v", err)
	}
	if err := s.PutForTest(key, "application/pdf", []byte("abc")); err == nil {
		t.Fatal("the stub accepted a content type the signature did not cover")
	}
	if err := s.PutForTest(key, "image/jpeg", []byte("abcd")); err == nil {
		t.Fatal("the stub accepted a length the signature did not cover")
	}
	if err := s.PutForTest(key, "image/jpeg", []byte("abc")); err != nil {
		t.Fatalf("a matching upload was refused: %v", err)
	}

	meta, err := s.Head(ctx, key)
	if err != nil || meta == nil {
		t.Fatalf("head after upload: meta=%v err=%v", meta, err)
	}
	if meta.Size != 3 || meta.ContentType != "image/jpeg" {
		t.Fatalf("head reported %+v", meta)
	}
}

func TestStubHeadReportsAbsenceAsNotAnError(t *testing.T) {
	s := NewStub()
	meta, err := s.Head(context.Background(), "nothing/here")
	if err != nil {
		t.Fatalf("absence reported as an error: %v", err)
	}
	if meta != nil {
		t.Fatalf("absence reported metadata: %+v", meta)
	}
}

func TestStubPresignPutExpiryIsEnforced(t *testing.T) {
	s := NewStub()
	const key = "kyc/u/late.jpg"
	if _, err := s.PresignPut(context.Background(), key, "image/png", 1, -time.Second); err != nil {
		t.Fatalf("presign: %v", err)
	}
	if err := s.PutForTest(key, "image/png", []byte("x")); err == nil {
		t.Fatal("the stub accepted an upload against an expired URL")
	}
}

func TestStubDeleteIsIdempotent(t *testing.T) {
	s := NewStub()
	if err := s.Delete(context.Background(), "never/existed"); err != nil {
		t.Fatalf("deleting an absent object errored: %v", err)
	}
}
