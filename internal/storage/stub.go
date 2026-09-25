package storage

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Stub is an in-memory object store for seed fixtures, CI and local development,
// following the USE_MOCK_MPESA precedent: the feature stays exercisable without
// credentials, so a developer can walk the whole verification flow and the browser
// suite can run with no bucket.
//
// It is not a fake that pretends everything succeeds. It records what was signed
// and refuses an upload whose type or length does not match, because the point of
// signing those is a guarantee, and a stub that ignored them would let a test pass
// against behaviour the real store would reject.
type Stub struct {
	mu      sync.Mutex
	objects map[string]stubObject
	signed  map[string]stubSignature
	// BaseURL is where presigned URLs point. In tests it is unreachable and
	// AcceptUpload is called directly; when the API registers the development
	// upload route it is set to that route, so a browser can complete a real
	// round trip with no bucket.
	BaseURL string
}

type stubObject struct {
	body        []byte
	contentType string
}

type stubSignature struct {
	contentType   string
	contentLength int64
	expiresAt     time.Time
}

// StubUploadPath is the route the development upload route is mounted at, and the
// path stub presigned URLs point to.
const StubUploadPath = "/kyc/stub-upload/"

func NewStub() *Stub {
	return &Stub{
		objects: map[string]stubObject{},
		signed:  map[string]stubSignature{},
		BaseURL: "http://localhost:8080/api/v1" + StubUploadPath,
	}
}

func (s *Stub) PresignPut(_ context.Context, key, contentType string, contentLength int64, expiry time.Duration) (*PresignedRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	expires := time.Now().Add(expiry)
	s.signed[key] = stubSignature{contentType: contentType, contentLength: contentLength, expiresAt: expires}
	return &PresignedRequest{
		URL:       s.BaseURL + url.PathEscape(key) + "?stub-signature=put",
		Method:    "PUT",
		Headers:   http.Header{"Content-Type": {contentType}, "Content-Length": {fmt.Sprint(contentLength)}},
		ExpiresAt: expires,
	}, nil
}

func (s *Stub) PresignGet(_ context.Context, key, disposition string, expiry time.Duration) (string, error) {
	q := url.Values{"stub-signature": {"get"}, "expires": {fmt.Sprint(time.Now().Add(expiry).Unix())}}
	if disposition != "" {
		q.Set("response-content-disposition", disposition)
	}
	return s.BaseURL + url.PathEscape(key) + "?" + q.Encode(), nil
}

func (s *Stub) Head(_ context.Context, key string) (*ObjectMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[key]
	if !ok {
		return nil, nil
	}
	return &ObjectMetadata{Size: int64(len(o.body)), ContentType: o.contentType, ETag: `"stub"`}, nil
}

func (s *Stub) ReadHead(_ context.Context, key string, n int64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[key]
	if !ok {
		return nil, fmt.Errorf("read object head: %w", errStubNoSuchKey)
	}
	if int64(len(o.body)) < n {
		n = int64(len(o.body))
	}
	out := make([]byte, n)
	copy(out, o.body[:n])
	return out, nil
}

func (s *Stub) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	delete(s.signed, key)
	return nil
}

// AcceptUpload stands in for the object store receiving a PUT at a signed URL.
//
// It enforces what a real signature enforces, so neither a test nor the
// development upload route can store bytes the real store would have refused: an
// unsigned key, an expired URL, a different content type, or a different length.
// Without these the stub would be a way to prove the wrong thing.
func (s *Stub) AcceptUpload(key, contentType string, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sig, ok := s.signed[key]
	if !ok {
		return fmt.Errorf("stub: no presigned PUT for key %q", key)
	}
	if time.Now().After(sig.expiresAt) {
		return fmt.Errorf("stub: presigned PUT for %q has expired", key)
	}
	if !strings.EqualFold(sig.contentType, contentType) {
		return fmt.Errorf("stub: content type %q does not match the signed %q", contentType, sig.contentType)
	}
	if int64(len(body)) != sig.contentLength {
		return fmt.Errorf("stub: length %d does not match the signed %d", len(body), sig.contentLength)
	}
	s.objects[key] = stubObject{body: append([]byte(nil), body...), contentType: contentType}
	return nil
}

// PutRawForTest stores an object without checking any signature, for the cases a
// test needs a store whose contents disagree with what was signed — a mislabelled
// document, or one placed out of band.
func (s *Stub) PutRawForTest(key, contentType string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = stubObject{body: append([]byte(nil), body...), contentType: contentType}
}

// HasForTest reports whether an object is present, so a test can assert that a
// refused confirm or a sweep actually deleted it.
func (s *Stub) HasForTest(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok
}

// KeysForTest lists what is stored.
func (s *Stub) KeysForTest() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.objects))
	for k := range s.objects {
		out = append(out, k)
	}
	return out
}

type stubError string

func (e stubError) Error() string { return string(e) }

const errStubNoSuchKey stubError = "no such key"
