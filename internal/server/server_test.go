package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"telegram-music-addon/internal/config"
	"telegram-music-addon/internal/index"
)

func setupTestServer() *Server {
	cfg := &config.Config{
		Port:      3000,
		UrlSecret: "testsecret123",
	}

	lib := index.NewLibrary("test_tracks.json")
	tracks := []*index.Track{
		{
			ID:           "1",
			Title:        "SexyBack (feat. Timbaland)",
			Artist:       "Justin Timberlake",
			Format:       "flac",
			AudioQuality: "LOSSLESS",
			Quality:      "LOSSLESS",
			Tier:         "LOSSLESS",
			Duration:     243,
			SizeBytes:    25000000,
			ISRC:         "USJCE0600401",
		},
		{
			ID:           "2",
			Title:        "SexyBack (feat. Timbaland) (Dolby Atmos)",
			Artist:       "Justin Timberlake",
			Format:       "eac3-joc",
			AudioQuality: "DOLBY_ATMOS",
			Quality:      "Dolby Atmos",
			Tier:         "DOLBY_ATMOS",
			IsAtmos:      true,
			Duration:     243,
			SizeBytes:    28000000,
			ISRC:         "USJCE0600401",
		},
	}
	lib.SetTracks(tracks)

	// Pre-load dummy preamble in FastStartCache for track 1
	dummyPreamble := make([]byte, 1024)
	for i := range dummyPreamble {
		dummyPreamble[i] = byte(i % 256)
	}
	lib.FastStartCache.Set("1", dummyPreamble)

	return NewServer(cfg, nil, lib, []byte("fake-png-bytes"))
}

func TestPingAndIconUnprotected(t *testing.T) {
	srv := setupTestServer()
	h := srv.Handler()

	// 1. /ping should be 200 without secret
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200 for /ping, got %d", rec.Code)
	}
	if rec.Body.String() != "pong" {
		t.Errorf("Expected 'pong', got '%s'", rec.Body.String())
	}

	// 2. /icon.png should be 200 without secret
	req = httptest.NewRequest(http.MethodGet, "/icon.png", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200 for /icon.png, got %d", rec.Code)
	}
}

func TestSecretProtection(t *testing.T) {
	srv := setupTestServer()
	h := srv.Handler()

	// Request without secret should be 401
	req := httptest.NewRequest(http.MethodGet, "/manifest.json", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for /manifest.json without secret, got %d", rec.Code)
	}

	// Request with valid secret prefix should be 200
	req = httptest.NewRequest(http.MethodGet, "/testsecret123/manifest.json", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 for /testsecret123/manifest.json, got %d", rec.Code)
	}

	var m index.Manifest
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("Failed to parse manifest: %v", err)
	}
	if !strings.Contains(m.Endpoints.Search, "/testsecret123/search") {
		t.Errorf("Manifest search endpoint missing secret prefix: %s", m.Endpoints.Search)
	}
}

func TestSearchEndpoint(t *testing.T) {
	srv := setupTestServer()
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/testsecret123/search?q=sexyback", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 for search, got %d", rec.Code)
	}

	var resp struct {
		Tracks []index.ClientTrack `json:"tracks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal search response: %v", err)
	}
	if len(resp.Tracks) != 2 {
		t.Fatalf("Expected 2 search results, got %d", len(resp.Tracks))
	}
}

func TestISRCAndResolveISRC(t *testing.T) {
	srv := setupTestServer()
	h := srv.Handler()

	// Test GET /isrc/:code
	req := httptest.NewRequest(http.MethodGet, "/testsecret123/isrc/USJCE0600401", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 for /isrc, got %d", rec.Code)
	}

	var resp struct {
		Tracks []index.ClientTrack `json:"tracks"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Tracks) != 2 {
		t.Errorf("Expected 2 tracks matching ISRC, got %d", len(resp.Tracks))
	}

	// Test GET /resolve-isrc
	req = httptest.NewRequest(http.MethodGet, "/testsecret123/resolve-isrc?isrc=USJCE0600401", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 for /resolve-isrc, got %d", rec.Code)
	}

	var resolveResp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resolveResp)
	if resolveResp["trackId"] != "1" {
		t.Errorf("Expected trackId '1', got %v", resolveResp["trackId"])
	}
}

func TestStreamDescriptor(t *testing.T) {
	srv := setupTestServer()
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/testsecret123/stream/1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 for /stream/1, got %d", rec.Code)
	}

	var desc index.StreamDescriptor
	if err := json.Unmarshal(rec.Body.Bytes(), &desc); err != nil {
		t.Fatalf("Failed to parse stream descriptor: %v", err)
	}
	if desc.Format != "flac" {
		t.Errorf("Expected format flac, got %s", desc.Format)
	}
	if !strings.Contains(desc.URL, "/testsecret123/audio/1") {
		t.Errorf("Expected URL to contain audio/1, got %s", desc.URL)
	}
}

func TestLibraryTxt(t *testing.T) {
	srv := setupTestServer()
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/testsecret123/library.txt", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 for /library.txt, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "SexyBack (feat. Timbaland) - Justin Timberlake") {
		t.Errorf("library.txt missing expected track: %s", body)
	}
}

func TestAudioRangeStreamingFromCache(t *testing.T) {
	srv := setupTestServer()
	h := srv.Handler()

	// Range request: bytes 0-99
	req := httptest.NewRequest(http.MethodGet, "/testsecret123/audio/1", nil)
	req.Header.Set("Range", "bytes=0-99")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("Expected 206 Partial Content, got %d", rec.Code)
	}

	cr := rec.Header().Get("Content-Range")
	if cr != "bytes 0-99/25000000" {
		t.Errorf("Expected Content-Range 'bytes 0-99/25000000', got '%s'", cr)
	}

	cl := rec.Header().Get("Content-Length")
	if cl != "100" {
		t.Errorf("Expected Content-Length '100', got '%s'", cl)
	}

	if rec.Body.Len() != 100 {
		t.Errorf("Expected 100 bytes returned, got %d", rec.Body.Len())
	}
}

