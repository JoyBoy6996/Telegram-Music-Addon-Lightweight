package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"telegram-music-addon/internal/config"
	"telegram-music-addon/internal/index"
	"telegram-music-addon/internal/telegram"
)

type Server struct {
	cfg     *config.Config
	tg      *telegram.Client
	lib     *index.Library
	icon    []byte
	version string
}

func NewServer(cfg *config.Config, tgClient *telegram.Client, lib *index.Library, iconBytes []byte) *Server {
	return &Server{
		cfg:     cfg,
		tg:      tgClient,
		lib:     lib,
		icon:    iconBytes,
		version: "3.0.1",
	}
}

func (s *Server) getBaseURL(r *http.Request, secretPrefix string) string {
	prefix := ""
	if secretPrefix != "" {
		prefix = "/" + strings.TrimPrefix(secretPrefix, "/")
	}

	if s.cfg.PublicURL != "" {
		return strings.TrimRight(s.cfg.PublicURL, "/") + prefix
	}

	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}

	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	if host == "" {
		host = fmt.Sprintf("localhost:%d", s.cfg.Port)
	}

	return fmt.Sprintf("%s://%s%s", proto, host, prefix)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Endpoints exempt from secret authentication
	mux.HandleFunc("/ping", s.handlePing)
	mux.HandleFunc("/icon.png", s.handleIcon)
	mux.HandleFunc("/favicon.ico", s.handleIcon)

	// Main API Endpoints
	mux.HandleFunc("/manifest.json", s.handleManifest)
	mux.HandleFunc("/search", s.handleSearch)
	mux.HandleFunc("/isrc/", s.handleISRC)
	mux.HandleFunc("/isrc", s.handleISRC)
	mux.HandleFunc("/resolve-isrc", s.handleResolveISRC)
	mux.HandleFunc("/stream/", s.handleStream)
	mux.HandleFunc("/audio/", s.handleAudio)
	mux.HandleFunc("/artwork/", s.handleArtwork)
	mux.HandleFunc("/library.txt", s.handleLibraryTxt)
	mux.HandleFunc("/reindex", s.handleReindex)
	mux.HandleFunc("/refresh", s.handleReindex)
	mux.HandleFunc("/", s.handleRoot)

	// Wrap in Secret Auth and Path Normalizer middleware
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Enable CORS
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		reqPath := r.URL.Path

		// Exempt public health & static assets
		if reqPath == "/ping" || reqPath == "/icon.png" || reqPath == "/favicon.ico" {
			mux.ServeHTTP(w, r)
			return
		}

		secret := s.cfg.UrlSecret
		secretPrefix := ""

		if secret != "" {
			prefix := "/" + secret
			encodedPrefix := "/" + strings.ReplaceAll(secret, "/", "%2F")

			isPrefixMatch := false
			if reqPath == prefix || strings.HasPrefix(reqPath, prefix+"/") {
				reqPath = strings.TrimPrefix(reqPath, prefix)
				secretPrefix = secret
				isPrefixMatch = true
			} else if reqPath == encodedPrefix || strings.HasPrefix(reqPath, encodedPrefix+"/") {
				reqPath = strings.TrimPrefix(reqPath, encodedPrefix)
				secretPrefix = secret
				isPrefixMatch = true
			}

			// Alternative auth methods: query param, header, or bearer token
			if !isPrefixMatch {
				if r.URL.Query().Get("secret") == secret ||
					r.Header.Get("X-Secret-Token") == secret ||
					r.Header.Get("Authorization") == "Bearer "+secret {
					secretPrefix = secret
					isPrefixMatch = true
				}
			}

			if !isPrefixMatch {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":   "Unauthorized: invalid or missing secret path",
					"message": "This Telegram Music Addon instance requires a valid secret URL prefix (e.g. /:secret/manifest.json)",
				})
				return
			}
		}

		if !strings.HasPrefix(reqPath, "/") {
			reqPath = "/" + reqPath
		}

		// Store secretPrefix in request context
		ctx := context.WithValue(r.Context(), "secretPrefix", secretPrefix)
		r = r.WithContext(ctx)
		r.URL.Path = reqPath

		mux.ServeHTTP(w, r)
	})
}

// ── Endpoint Handlers ───────────────────────────────────────────────────────

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("pong"))
}

func (s *Server) handleIcon(w http.ResponseWriter, r *http.Request) {
	if len(s.icon) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.icon)
}

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	secretPrefix, _ := r.Context().Value("secretPrefix").(string)
	base := s.getBaseURL(r, secretPrefix)

	count := s.lib.Count()
	manifest := index.Manifest{
		ID:                 "com.personal.telegrammusic",
		Name:               "Telegram Music Addon",
		Version:            fmt.Sprintf("%s • %d songs", s.version, count),
		Description:        "Personal hi-res, lossless, and high-quality music library streamed directly from Telegram",
		Icon:               fmt.Sprintf("%s/icon.png", base),
		CanServeLossless:   true,
		CanServeDolbyAtmos: true,
		Resources:          []string{"search", "stream", "isrc"},
		Types:              []string{"track"},
		ContentType:        "music",
		Settings: []index.ManifestSetting{
			{
				Key:     "quality",
				Type:    "select",
				Default: "lossless",
				Options: []index.SettingOption{
					{Label: "Lossless (FLAC/ALAC)", Value: "lossless"},
					{Label: "High (320kbps MP3)", Value: "high"},
					{Label: "Low (Data Saver)", Value: "low"},
				},
			},
			{
				Key:     "atmos",
				Type:    "select",
				Default: "auto",
				Options: []index.SettingOption{
					{Label: "Dolby Atmos (Spatial Audio)", Value: "auto"},
					{Label: "Stereo Only", Value: "off"},
				},
			},
		},
		Endpoints: index.ManifestEndpoints{
			Search: fmt.Sprintf("%s/search?q={query}", base),
			ISRC:   fmt.Sprintf("%s/isrc/{isrc}", base),
			Stream: fmt.Sprintf("%s/stream/{id}", base),
			Audio:  fmt.Sprintf("%s/audio/{id}", base),
		},
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=600")
	_ = json.NewEncoder(w).Encode(manifest)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	atmosParam := r.URL.Query().Get("atmos")
	prefersAtmos := atmosParam == "auto" || atmosParam == "true"
	duration, _ := strconv.Atoi(r.URL.Query().Get("duration"))
	if duration == 0 {
		duration, _ = strconv.Atoi(r.URL.Query().Get("d"))
	}

	matchedTracks := s.lib.Search(q, prefersAtmos, duration)

	secretPrefix, _ := r.Context().Value("secretPrefix").(string)
	base := s.getBaseURL(r, secretPrefix)

	clientTracks := make([]index.ClientTrack, len(matchedTracks))
	for i, t := range matchedTracks {
		clientTracks[i] = index.FormatTrackForClient(t, base)
	}

	// Pre-warm top match if confident search hit
	if len(matchedTracks) > 0 {
		top := matchedTracks[0]
		go func(topTrack *index.Track) {
			if s.lib.FastStartCache.Get(topTrack.ID) == nil && s.tg != nil {
				preamble, err := s.tg.GetFileChunk(context.Background(), topTrack.ID, 0, 512*1024)
				if err == nil && len(preamble) > 0 {
					s.lib.FastStartCache.Set(topTrack.ID, preamble)
				}
			}
		}(top)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"tracks": clientTracks,
	})
}

func (s *Server) handleISRC(w http.ResponseWriter, r *http.Request) {
	code := path.Base(r.URL.Path)
	if code == "isrc" || code == "/" || code == "." {
		code = r.URL.Query().Get("code")
		if code == "" {
			code = r.URL.Query().Get("isrc")
		}
	}
	code = strings.TrimSuffix(strings.TrimSpace(code), ".json")

	if code == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":  "ISRC code required",
			"tracks": []interface{}{},
		})
		return
	}

	matches := s.lib.LookupISRC(code)
	secretPrefix, _ := r.Context().Value("secretPrefix").(string)
	base := s.getBaseURL(r, secretPrefix)

	clientTracks := make([]index.ClientTrack, len(matches))
	for i, t := range matches {
		clientTracks[i] = index.FormatTrackForClient(t, base)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"tracks": clientTracks,
	})
}

func (s *Server) handleResolveISRC(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSuffix(strings.TrimSpace(r.URL.Query().Get("isrc")), ".json")
	if code == "" {
		code = strings.TrimSuffix(strings.TrimSpace(r.URL.Query().Get("code")), ".json")
	}

	if code == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "ISRC code required",
			"trackId": nil,
		})
		return
	}

	matches := s.lib.LookupISRC(code)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if len(matches) > 0 {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"trackId": matches[0].ID,
			"id":      matches[0].ID,
		})
		return
	}

	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error":   "Track not found",
		"trackId": nil,
	})
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	trackID := path.Base(r.URL.Path)
	track := s.lib.GetTrack(trackID)
	if track == nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Track not found",
		})
		return
	}

	secretPrefix, _ := r.Context().Value("secretPrefix").(string)
	base := s.getBaseURL(r, secretPrefix)

	isAtmos := track.IsAtmos
	fmtName := strings.ToLower(track.Format)
	isLossless := fmtName == "flac" || fmtName == "alac" || fmtName == "wav"
	isHiRes := isLossless && (track.BitDepth > 16 || track.SampleRate > 44100)

	qualityTier := "HIGH"
	if isAtmos {
		qualityTier = "DOLBY_ATMOS"
	} else if isHiRes {
		qualityTier = "HI_RES"
	} else if isLossless {
		qualityTier = "LOSSLESS"
	}

	formatVal := track.Format
	codecVal := track.Format
	containerVal := track.Format
	audioMode := "STEREO"
	streamQuality := track.Quality
	if isAtmos {
		formatVal = "eac3-joc"
		codecVal = "eac3-joc"
		containerVal = "mp4"
		audioMode = "DOLBY_ATMOS"
		streamQuality = "Dolby Atmos"
	}

	sampleRate := track.SampleRate
	if sampleRate == 0 {
		sampleRate = 44100
	}
	bitDepth := track.BitDepth
	if bitDepth == 0 {
		bitDepth = 16
	}

	desc := index.StreamDescriptor{
		URL:           fmt.Sprintf("%s/audio/%s", base, track.ID),
		Format:        formatVal,
		Codec:         codecVal,
		Container:     containerVal,
		Manifest:      "none",
		Encrypted:     false,
		AudioMode:     audioMode,
		SampleRate:    sampleRate,
		BitDepth:      bitDepth,
		Quality:       qualityTier,
		AudioQuality:  qualityTier,
		Tier:          qualityTier,
		StreamQuality: streamQuality,
		Description:   track.Quality,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(desc)
}

func (s *Server) handleArtwork(w http.ResponseWriter, r *http.Request) {
	trackID := path.Base(r.URL.Path)
	track := s.lib.GetTrack(trackID)
	if track == nil || !track.HasArtwork {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	data, mimeType, err := s.tg.GetArtwork(ctx, trackID)
	if err != nil || len(data) == 0 {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	w.Header().Set("ETag", fmt.Sprintf("\"%s-art\"", trackID))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleLibraryTxt(w http.ResponseWriter, r *http.Request) {
	tracks := s.lib.GetAllTracks()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60")

	for _, t := range tracks {
		artist := index.FormatArtistForClient(t.Artist)
		_, _ = fmt.Fprintf(w, "%s - %s\n", t.Title, artist)
	}
}

func (s *Server) handleReindex(w http.ResponseWriter, r *http.Request) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_ = s.tg.IndexChannel(ctx, s.lib, nil)
	}()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":    true,
		"count": s.lib.Count(),
	})
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	secretPrefix, _ := r.Context().Value("secretPrefix").(string)
	base := s.getBaseURL(r, secretPrefix)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"name":     "Telegram Music Addon (Go)",
		"status":   "running",
		"tracks":   s.lib.Count(),
		"manifest": fmt.Sprintf("%s/manifest.json", base),
	})
}

// ── HTTP 206 Partial Content Range Streaming ────────────────────────────────

var rangeRegex = regexp.MustCompile(`bytes=(\d*)-(\d*)`)

func (s *Server) handleAudio(w http.ResponseWriter, r *http.Request) {
	trackID := path.Base(r.URL.Path)
	track := s.lib.GetTrack(trackID)
	if track == nil {
		http.NotFound(w, r)
		return
	}

	totalSize := track.SizeBytes
	if totalSize <= 0 {
		http.Error(w, "Unable to determine audio file size", http.StatusInternalServerError)
		return
	}

	start := int64(0)
	end := totalSize - 1
	isRange := false

	rangeHeader := r.Header.Get("Range")
	if rangeHeader != "" {
		if m := rangeRegex.FindStringSubmatch(rangeHeader); len(m) == 3 {
			if m[1] == "" && m[2] != "" { // suffix range: bytes=-500
				suffix, err := strconv.ParseInt(m[2], 10, 64)
				if err == nil {
					start = totalSize - suffix
					if start < 0 {
						start = 0
					}
					isRange = true
				}
			} else if m[1] != "" {
				sVal, err1 := strconv.ParseInt(m[1], 10, 64)
				if err1 == nil {
					start = sVal
					isRange = true
					if m[2] != "" {
						eVal, err2 := strconv.ParseInt(m[2], 10, 64)
						if err2 == nil && eVal < totalSize {
							end = eVal
						}
					}
				}
			}
		}
	}

	if isRange && (start >= totalSize || start > end) {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", totalSize))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}

	if start < 0 {
		start = 0
	}
	if end >= totalSize {
		end = totalSize - 1
	}

	bytesNeeded := end - start + 1

	// Fast cache validation
	etag := fmt.Sprintf("\"%s-%d\"", track.ID, totalSize)
	if !isRange && r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	contentType := track.MimeType
	if track.IsAtmos {
		contentType = "audio/mp4"
	} else if contentType == "" {
		if track.Format == "flac" {
			contentType = "audio/flac"
		} else {
			contentType = "application/octet-stream"
		}
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Keep-Alive", "timeout=30, max=100")
	w.Header().Set("Content-Length", strconv.FormatInt(bytesNeeded, 10))
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")

	if isRange {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, totalSize))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	if r.Method == http.MethodHead {
		return
	}

	// Fast-Start Preamble check
	bytesSent := int64(0)
	cachedPreamble := s.lib.FastStartCache.Get(track.ID)
	if cachedPreamble != nil && start < int64(len(cachedPreamble)) {
		sliceEnd := start + bytesNeeded
		if sliceEnd > int64(len(cachedPreamble)) {
			sliceEnd = int64(len(cachedPreamble))
		}
		preambleSlice := cachedPreamble[start:sliceEnd]
		n, err := w.Write(preambleSlice)
		bytesSent += int64(n)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		if err != nil || bytesSent >= bytesNeeded {
			return
		}
	}

	// Stream remaining bytes live from Telegram MTProto with backpressure
	ctx := r.Context()
	for bytesSent < bytesNeeded {
		select {
		case <-ctx.Done():
			return // Client seeked away or closed connection
		default:
		}

		currentPos := start + bytesSent
		remaining := bytesNeeded - bytesSent
		chunkLimit := 256 * 1024 // 256 KB chunk limit
		if remaining < int64(chunkLimit) {
			chunkLimit = int(remaining)
		}

		chunk, err := s.tg.GetFileChunk(ctx, track.ID, currentPos, chunkLimit)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Printf("[Stream Error] Track %s byte %d: %v", track.ID, currentPos, err)
			}
			return
		}

		if len(chunk) == 0 {
			break
		}

		// Cache preamble for fast starts if this is playback start
		if start == 0 && bytesSent == 0 && len(chunk) >= 64*1024 {
			s.lib.FastStartCache.Set(track.ID, chunk)
		}

		n, writeErr := w.Write(chunk)
		bytesSent += int64(n)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}

		if writeErr != nil {
			return // Client socket closed
		}
	}
}
