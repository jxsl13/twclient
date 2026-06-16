package net6

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jxsl13/twclient/packet"
)

const (
	httpMapAttempts    = 4                      // total tries before giving up → UDP fallback (V148)
	httpMapBackoff     = 500 * time.Millisecond // between resume attempts (V147)
	udpMapChunkRetries = 3                      // in-session re-requests of a chunk on a recv hiccup (V147)
)

// mapHTTPClient downloads maps. No client-level timeout — the per-request ctx
// (the connect-sequence ctx) governs cancellation (V66).
var mapHTTPClient = &http.Client{}

// httpDownloadMap fetches the map over HTTP(S) from
// <base>/<name>_<sha256hex>.map, RESUMING with a Range request after a network
// hiccup (V147), capping the body at info.Size and verifying SHA256 (V148).
// Returns the raw map bytes, or an error (the caller falls back to UDP).
func (s *Session) httpDownloadMap(ctx context.Context, info packet.MapInfo) ([]byte, error) {
	mapURL := fmt.Sprintf("%s/%s_%x.map",
		strings.TrimRight(s.mapDownloadURL, "/"), url.PathEscape(info.Name), info.Sha256)

	data := make([]byte, 0, info.Size)
	var lastErr error
	for attempt := range httpMapAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(httpMapBackoff):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, mapURL, nil)
		if err != nil {
			return nil, err
		}
		if len(data) > 0 { // resume from where we stopped
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", len(data)))
		}
		resp, err := mapHTTPClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		switch resp.StatusCode {
		case http.StatusOK:
			data = data[:0] // server ignored Range → restart from 0
		case http.StatusPartialContent:
			// resuming from len(data)
		default:
			resp.Body.Close()
			return nil, fmt.Errorf("session06: http map %s: status %d", mapURL, resp.StatusCode)
		}

		remaining := info.Size - len(data)
		if remaining <= 0 {
			resp.Body.Close()
			return nil, fmt.Errorf("session06: http map %q oversize (have %d, want %d)", info.Name, len(data), info.Size)
		}
		buf := make([]byte, remaining)
		n, rerr := io.ReadFull(resp.Body, buf)
		resp.Body.Close()
		data = append(data, buf[:n]...)
		if s.mapProgress != nil {
			s.mapProgress(len(data), info.Size)
		}
		if len(data) >= info.Size {
			break
		}
		lastErr = rerr // short read (hiccup) → Range-resume next attempt
	}

	if len(data) != info.Size {
		if lastErr == nil {
			lastErr = fmt.Errorf("incomplete %d/%d bytes", len(data), info.Size)
		}
		return nil, fmt.Errorf("session06: http map download: %w", lastErr)
	}
	if sha256.Sum256(data) != info.Sha256 {
		return nil, fmt.Errorf("session06: http map %q sha256 mismatch", info.Name)
	}
	return data, nil
}
