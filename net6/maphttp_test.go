package net6

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/jxsl13/twclient/packet"
)

func mapInfoFor(blob []byte) packet.MapInfo {
	return packet.MapInfo{Name: "test map", Size: len(blob), Sha256: sha256.Sum256(blob)}
}

// V148: a full HTTP(S) map download returns the bytes, size + SHA256 verified.
func TestHTTPMapDownloadFull(t *testing.T) {
	blob := []byte(strings.Repeat("twmap-bytes", 200))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(blob)
	}))
	t.Cleanup(srv.Close)

	s := &Session{mapDownloadURL: srv.URL}
	got, err := s.httpDownloadMap(context.Background(), mapInfoFor(blob))
	if err != nil {
		t.Fatalf("httpDownloadMap: %v", err)
	}
	if sha256.Sum256(got) != sha256.Sum256(blob) || len(got) != len(blob) {
		t.Errorf("downloaded %d bytes, want %d (sha mismatch=%v)", len(got), len(blob), sha256.Sum256(got) != sha256.Sum256(blob))
	}
}

// V147: a mid-body cut is RESUMED via a Range request (not restarted), and the
// assembled map still verifies.
func TestHTTPMapDownloadRangeResume(t *testing.T) {
	blob := []byte(strings.Repeat("ABCDEFGH", 500)) // 4000 bytes
	half := len(blob) / 2
	var rangeReqs int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rng := r.Header.Get("Range")
		if rng == "" {
			// First request: deliver only the first half, then drop the body
			// (chunked, no Content-Length) → client short-reads → resumes.
			_, _ = w.Write(blob[:half])
			return
		}
		rangeReqs++
		// Range: bytes=<start>-
		start, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rng, "bytes="), "-"))
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(blob)-1, len(blob)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(blob[start:])
	}))
	t.Cleanup(srv.Close)

	s := &Session{mapDownloadURL: srv.URL}
	got, err := s.httpDownloadMap(context.Background(), mapInfoFor(blob))
	if err != nil {
		t.Fatalf("httpDownloadMap (resume): %v", err)
	}
	if len(got) != len(blob) || sha256.Sum256(got) != sha256.Sum256(blob) {
		t.Errorf("resumed download wrong: %d/%d bytes, sha-ok=%v", len(got), len(blob), sha256.Sum256(got) == sha256.Sum256(blob))
	}
	if rangeReqs == 0 {
		t.Error("expected a Range resume request, got none")
	}
}

// V148: a body whose bytes don't match the announced SHA256 is rejected (so the
// caller falls back to UDP), not silently accepted.
func TestHTTPMapDownloadShaMismatch(t *testing.T) {
	blob := []byte(strings.Repeat("x", 128))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("y", 128))) // wrong bytes, right length
	}))
	t.Cleanup(srv.Close)

	s := &Session{mapDownloadURL: srv.URL}
	if _, err := s.httpDownloadMap(context.Background(), mapInfoFor(blob)); err == nil {
		t.Fatal("httpDownloadMap accepted a sha256-mismatched body, want error")
	}
}

// V148: a non-2xx response is an error (→ UDP fallback at the caller).
func TestHTTPMapDownloadNotFound(t *testing.T) {
	blob := []byte("whatever")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	s := &Session{mapDownloadURL: srv.URL}
	if _, err := s.httpDownloadMap(context.Background(), mapInfoFor(blob)); err == nil {
		t.Fatal("httpDownloadMap accepted a 404, want error")
	}
}
