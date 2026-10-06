package index

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"
)

var (
	artistSeparatorsRegex = regexp.MustCompile(`(?i)\s*(?:[,&/;·|]|\band\b|\bx\b|\bvs\.?\b|\bfeat\.?\b|\bft\.?\b|\bfeaturing\b|\bwith\b)\s*`)
	bracketedRegex        = regexp.MustCompile(`[([][^()[\]]*[)\]]`)
	noiseWordsRegex       = regexp.MustCompile(`(?i)\b(?:official|video|audio|lyrics|lyric|lyrical|song|songs|full|hd|hq|4k|mp3|flac|ost|soundtrack|remaster|remastered|atmos|dolby)\b`)
	versionSuffixRegex    = regexp.MustCompile(`(?i)\s+[-\x{2013}\x{2014}]+\s+(?:new version|version|lofi|remix|acoustic|live|slowed|reverb|edit|revisited|unplugged|original mix|extended mix|deluxe).*$`)
	nonAlphanumericRegex  = regexp.MustCompile(`[^\p{L}\p{N}\s]`)
	spaceRegex            = regexp.MustCompile(`\s+`)
)

type composerDuo struct {
	re  *regexp.Regexp
	rep string
}

var composerDuos = []composerDuo{
	{regexp.MustCompile(`(?i)vishal[-\s\x{2013}\x{2014}]+shekhar`), "Vishal & Shekhar"},
	{regexp.MustCompile(`(?i)sachin[-\s\x{2013}\x{2014}]+jigar`), "Sachin & Jigar"},
	{regexp.MustCompile(`(?i)salim[-\s\x{2013}\x{2014}]+sulaiman`), "Salim & Sulaiman"},
	{regexp.MustCompile(`(?i)shankar[-\s\x{2013}\x{2014}]+ehsaan[-\s\x{2013}\x{2014}]+loy`), "Shankar & Ehsaan & Loy"},
	{regexp.MustCompile(`(?i)ajay[-\s\x{2013}\x{2014}]+atul`), "Ajay & Atul"},
	{regexp.MustCompile(`(?i)sajid[-\s\x{2013}\x{2014}]+wajid`), "Sajid & Wajid"},
	{regexp.MustCompile(`(?i)nadeem[-\s\x{2013}\x{2014}]+shravan`), "Nadeem & Shravan"},
	{regexp.MustCompile(`(?i)jatin[-\s\x{2013}\x{2014}]+lalit`), "Jatin & Lalit"},
	{regexp.MustCompile(`(?i)anand[-\s\x{2013}\x{2014}]+milind`), "Anand & Milind"},
	{regexp.MustCompile(`(?i)laxmikant[-\s\x{2013}\x{2014}]+pyarelal`), "Laxmikant & Pyarelal"},
	{regexp.MustCompile(`(?i)kalyanji[-\s\x{2013}\x{2014}]+anandji`), "Kalyanji & Anandji"},
	{regexp.MustCompile(`(?i)shiv[-\s\x{2013}\x{2014}]+hari`), "Shiv & Hari"},
	{regexp.MustCompile(`(?i)raam[-\s\x{2013}\x{2014}]+laxman`), "Raam & Laxman"},
}

func FormatArtistForClient(artistStr string) string {
	if artistStr == "" || strings.EqualFold(strings.TrimSpace(artistStr), "unknown artist") {
		return "Unknown Artist"
	}
	formatted := artistStr
	for _, duo := range composerDuos {
		formatted = duo.re.ReplaceAllString(formatted, duo.rep)
	}
	formatted = regexp.MustCompile(`\s+[-\x{2013}\x{2014}]+\s+`).ReplaceAllString(formatted, ", ")
	return strings.TrimSpace(formatted)
}

func ExtractCoreTitle(title string) string {
	if title == "" {
		return ""
	}
	clean := strings.ToLower(title)
	clean = bracketedRegex.ReplaceAllString(clean, " ")
	clean = noiseWordsRegex.ReplaceAllString(clean, " ")
	clean = versionSuffixRegex.ReplaceAllString(clean, " ")
	clean = nonAlphanumericRegex.ReplaceAllString(clean, " ")
	clean = spaceRegex.ReplaceAllString(clean, " ")
	return strings.TrimSpace(clean)
}

func IndexTrackKeywords(t *Track) {
	if t == nil {
		return
	}
	words := make(map[string]struct{})
	var rawWordsList []string

	addWords := func(str string) {
		if str == "" {
			return
		}
		tokens := strings.FieldsFunc(strings.ToLower(str), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r)
		})
		for _, tok := range tokens {
			words[tok] = struct{}{}
			rawWordsList = append(rawWordsList, tok)
		}
	}

	addWords(t.Title)
	addWords(ExtractCoreTitle(t.Title))
	addWords(t.Artist)
	addWords(t.Album)
	if t.ISRC != "" {
		words[strings.ToLower(strings.TrimSpace(t.ISRC))] = struct{}{}
	}

	t.SearchWords = words
	t.SearchWordArray = rawWordsList
	t.SearchFullText = strings.ToLower(fmt.Sprintf("%s %s %s", t.Title, t.Artist, t.Album))
}

type QueryContext struct {
	QClean         string
	QueryCore      string
	QueryTokens    []string
	TargetDuration int
}

func ScoreTrackMatch(track *Track, ctx *QueryContext) int {
	if ctx == nil || ctx.QClean == "" {
		return 100
	}

	// Exact ISRC match
	if track.ISRC != "" && strings.EqualFold(strings.TrimSpace(track.ISRC), ctx.QClean) {
		return 500
	}

	trackTitle := strings.ToLower(track.Title)
	trackTitleCore := ExtractCoreTitle(track.Title)
	titleTokens := strings.Fields(trackTitleCore)
	trackArtist := strings.ToLower(track.Artist)
	trackAlbum := strings.ToLower(track.Album)

	queryCoreCompact := strings.ReplaceAll(ctx.QueryCore, " ", "")
	trackTitleCoreCompact := strings.ReplaceAll(trackTitleCore, " ", "")

	cleanRawTitle := nonAlphanumericRegex.ReplaceAllString(trackTitle, "")
	cleanRawQuery := nonAlphanumericRegex.ReplaceAllString(ctx.QClean, "")

	baseScore := 0

	// Exact full title match
	if cleanRawTitle != "" && cleanRawTitle == cleanRawQuery {
		baseScore = 350
	} else if ctx.QueryCore != "" && trackTitleCore != "" {
		isTitlePrefix := strings.HasPrefix(ctx.QueryCore, trackTitleCore+" ") ||
			(trackTitleCoreCompact != "" && strings.HasPrefix(ctx.QueryCore, trackTitleCoreCompact+" "))
		if isTitlePrefix {
			var extraWords string
			if strings.HasPrefix(ctx.QueryCore, trackTitleCore+" ") {
				extraWords = strings.TrimSpace(ctx.QueryCore[len(trackTitleCore):])
			} else {
				extraWords = strings.TrimSpace(ctx.QueryCore[len(trackTitleCoreCompact):])
			}
			if extraWords == "" {
				baseScore = 250
			} else if strings.Contains(trackArtist, extraWords) || SharesArtist(extraWords, track.Artist) {
				baseScore = 400 // Perfect match: Title + Artist
			} else if trackAlbum != "" && strings.Contains(trackAlbum, extraWords) {
				baseScore = 320
			} else if strings.Contains(trackTitle, extraWords) {
				baseScore = 310
			} else {
				extraTokens := strings.Fields(nonAlphanumericRegex.ReplaceAllString(extraWords, " "))
				hasArtistToken := false
				for _, w := range extraTokens {
					if strings.Contains(trackArtist, w) || SharesArtist(w, track.Artist) {
						hasArtistToken = true
						break
					}
				}
				hasAlbumToken := false
				for _, w := range extraTokens {
					if trackAlbum != "" && strings.Contains(trackAlbum, w) {
						hasAlbumToken = true
						break
					}
				}
				if hasArtistToken {
					baseScore = 380
				} else if hasAlbumToken {
					baseScore = 300
				} else {
					baseScore = 120
				}
			}
		}
	}

	// Exact core title match
	if baseScore == 0 && ctx.QueryCore != "" &&
		(ctx.QueryCore == trackTitleCore || (queryCoreCompact != "" && queryCoreCompact == trackTitleCoreCompact)) {
		if strings.Contains(track.Title, "(") || strings.Contains(track.Title, "[") {
			baseScore = 295
		} else {
			baseScore = 300
		}
	}

	// Whole word match in title
	if baseScore == 0 && len(cleanRawQuery) > 0 {
		re, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(cleanRawQuery) + `\b`)
		if err == nil && (re.MatchString(trackTitleCore) || re.MatchString(trackTitle)) {
			baseScore = 200
		}
	}

	// Token matching: when track title is fully inside query tokens
	if baseScore == 0 && len(titleTokens) > 0 {
		allMatch := true
		for _, tw := range titleTokens {
			found := false
			for _, qw := range ctx.QueryTokens {
				if tw == qw {
					found = true
					break
				}
			}
			if !found {
				allMatch = false
				break
			}
		}
		if allMatch {
			var extraTokens []string
			for _, qw := range ctx.QueryTokens {
				isTitle := false
				for _, tw := range titleTokens {
					if qw == tw {
						isTitle = true
						break
					}
				}
				if !isTitle {
					extraTokens = append(extraTokens, qw)
				}
			}
			if len(extraTokens) == 0 {
				baseScore = 250
			} else {
				hasArtistToken := false
				for _, qw := range extraTokens {
					if strings.Contains(trackArtist, qw) || SharesArtist(qw, track.Artist) {
						hasArtistToken = true
						break
					}
				}
				hasAlbumToken := false
				for _, qw := range extraTokens {
					if trackAlbum != "" && strings.Contains(trackAlbum, qw) {
						hasAlbumToken = true
						break
					}
				}
				if hasArtistToken {
					baseScore = 380
				} else if hasAlbumToken {
					baseScore = 300
				} else if len(titleTokens) >= 2 || len(trackTitleCore) >= 8 {
					baseScore = 170
				}
			}
		}
	}

	// Token matching: whole-word matching across title, artist, album
	if baseScore == 0 {
		trackWords := strings.FieldsFunc(fmt.Sprintf("%s %s %s", trackTitleCore, trackArtist, trackAlbum), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r)
		})
		trackWordSet := make(map[string]struct{}, len(trackWords))
		for _, tw := range trackWords {
			trackWordSet[tw] = struct{}{}
		}

		matchCount := 0
		for _, tok := range ctx.QueryTokens {
			if _, ok := trackWordSet[tok]; ok {
				matchCount++
			} else if len(tok) >= 4 {
				for _, tw := range trackWords {
					if strings.HasPrefix(tw, tok) || strings.HasPrefix(tok, tw) {
						matchCount++
						break
					}
				}
			}
		}
		if len(ctx.QueryTokens) > 0 {
			ratio := float64(matchCount) / float64(len(ctx.QueryTokens))
			if ratio >= 0.6 {
				baseScore = int(ratio * 120)
			} else if len(ctx.QClean) >= 5 {
				fullText := fmt.Sprintf("%s %s %s", trackTitle, trackArtist, trackAlbum)
				if strings.Contains(fullText, ctx.QClean) {
					baseScore = 60
				}
			}
		}
	}

	// Duration proximity scoring
	if baseScore > 0 && ctx.TargetDuration > 0 && track.Duration > 0 {
		diff := ctx.TargetDuration - track.Duration
		if diff < 0 {
			diff = -diff
		}
		if diff <= 2 {
			baseScore += 50
		} else if diff <= 5 {
			baseScore += 25
		} else if diff > 15 {
			baseScore -= 80
			if baseScore < 10 {
				baseScore = 10
			}
		}
	}

	return baseScore
}

func SharesArtist(queryArtistStr, trackArtistStr string) bool {
	qParts := parseArtistTokens(queryArtistStr)
	tParts := parseArtistTokens(trackArtistStr)
	if len(qParts) == 0 || len(tParts) == 0 {
		return false
	}
	for _, q := range qParts {
		for _, t := range tParts {
			if runOf(q, t) || runOf(t, q) {
				return true
			}
		}
	}
	return false
}

func parseArtistTokens(str string) [][]string {
	if str == "" {
		return nil
	}
	parts := artistSeparatorsRegex.Split(strings.ToLower(str), -1)
	var res [][]string
	for _, p := range parts {
		words := strings.Fields(nonAlphanumericRegex.ReplaceAllString(p, " "))
		if len(words) > 0 {
			res = append(res, words)
		}
	}
	return res
}

func runOf(outer, inner []string) bool {
	if len(inner) == 0 || len(inner) > len(outer) {
		return false
	}
	for i := 0; i <= len(outer)-len(inner); i++ {
		match := true
		for j := 0; j < len(inner); j++ {
			if outer[i+j] != inner[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func FormatTrackForClient(t *Track, base string) ClientTrack {
	isAtmos := t.IsAtmos
	fmtName := strings.ToLower(t.Format)
	isLossless := fmtName == "flac" || fmtName == "alac" || fmtName == "wav"
	isHiRes := isLossless && ((t.BitDepth > 16) || (t.SampleRate > 44100))

	qualityTier := "HIGH"
	if isAtmos {
		qualityTier = "DOLBY_ATMOS"
	} else if isHiRes {
		qualityTier = "HI_RES"
	} else if isLossless {
		qualityTier = "LOSSLESS"
	}

	audioModes := []string{"STEREO"}
	var atmosVal *bool
	formatVal := t.Format
	if isAtmos {
		audioModes = []string{"DOLBY_ATMOS"}
		b := true
		atmosVal = &b
		formatVal = "eac3-joc"
	}

	bitDepth := t.BitDepth
	if bitDepth == 0 {
		if isHiRes {
			bitDepth = 24
		} else {
			bitDepth = 16
		}
	}

	sampleRate := t.SampleRate
	if sampleRate == 0 {
		if isHiRes {
			sampleRate = 96000
		} else {
			sampleRate = 44100
		}
	}

	var durPtr *int
	if t.Duration > 0 {
		d := t.Duration
		durPtr = &d
	}

	var artURL string
	if t.HasArtwork {
		artURL = fmt.Sprintf("%s/artwork/%s", base, t.ID)
	}

	streamURL := fmt.Sprintf("%s/audio/%s", base, t.ID)
	descURL := fmt.Sprintf("%s/stream/%s", base, t.ID)

	return ClientTrack{
		ID:              t.ID,
		Title:           t.Title,
		Artist:          FormatArtistForClient(t.Artist),
		Album:           t.Album,
		Duration:        durPtr,
		Format:          formatVal,
		AudioQuality:    qualityTier,
		Quality:         qualityTier,
		Tier:            qualityTier,
		BitDepth:        bitDepth,
		SampleRate:      sampleRate,
		AudioModes:      audioModes,
		Atmos:           atmosVal,
		ArtworkURL:      artURL,
		AlbumArtworkURL: artURL,
		Stream:          descURL,
		StreamURL:       streamURL,
		StreamUrl:       streamURL,
		Audio:           streamURL,
		AudioUrl:        streamURL,
		ISRC:            t.ISRC,
	}
}

// ── LRU Fast Start Cache (max 4 MB) ──────────────────────────────────────────

type LRUCache struct {
	mu       sync.Mutex
	capacity int
	items    map[string][]byte
	keys     []string
}

func NewLRUCache(maxItems int) *LRUCache {
	return &LRUCache{
		capacity: maxItems,
		items:    make(map[string][]byte),
		keys:     make([]string, 0, maxItems),
	}
}

func (c *LRUCache) Get(key string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	val, ok := c.items[key]
	if !ok {
		return nil
	}
	// Move to back
	for i, k := range c.keys {
		if k == key {
			c.keys = append(append(c.keys[:i], c.keys[i+1:]...), key)
			break
		}
	}
	return val
}

func (c *LRUCache) Set(key string, val []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[key]; ok {
		c.items[key] = val
		return
	}
	if len(c.keys) >= c.capacity {
		oldest := c.keys[0]
		c.keys = c.keys[1:]
		delete(c.items, oldest)
	}
	c.keys = append(c.keys, key)
	c.items[key] = val
}

// ── In-Memory Library Store ──────────────────────────────────────────────────

type Library struct {
	mu             sync.RWMutex
	tracks         []*Track
	idMap          map[string]*Track
	cachePath      string
	FastStartCache *LRUCache
}

func NewLibrary(cachePath string) *Library {
	if cachePath == "" {
		cachePath = "tracks_cache.json"
	}
	return &Library{
		tracks:         make([]*Track, 0),
		idMap:          make(map[string]*Track),
		cachePath:      cachePath,
		FastStartCache: NewLRUCache(8), // max 8 preambles (~4 MB total)
	}
}

func (lib *Library) Count() int {
	lib.mu.RLock()
	defer lib.mu.RUnlock()
	return len(lib.tracks)
}

func (lib *Library) GetAllTracks() []*Track {
	lib.mu.RLock()
	defer lib.mu.RUnlock()
	cpy := make([]*Track, len(lib.tracks))
	copy(cpy, lib.tracks)
	return cpy
}

func (lib *Library) GetTrack(id string) *Track {
	lib.mu.RLock()
	defer lib.mu.RUnlock()
	return lib.idMap[id]
}

func (lib *Library) SetTracks(tracks []*Track) {
	lib.mu.Lock()
	defer lib.mu.Unlock()
	lib.tracks = tracks
	lib.idMap = make(map[string]*Track, len(tracks))
	for _, t := range tracks {
		IndexTrackKeywords(t)
		lib.idMap[t.ID] = t
	}
}

func (lib *Library) LoadCache() error {
	data, err := os.ReadFile(lib.cachePath)
	if err != nil {
		return err
	}
	var rawTracks []*Track
	if err := json.Unmarshal(data, &rawTracks); err != nil {
		return err
	}
	lib.SetTracks(rawTracks)
	return nil
}

func (lib *Library) SaveCache() error {
	lib.mu.RLock()
	data, err := json.MarshalIndent(lib.tracks, "", "  ")
	lib.mu.RUnlock()
	if err != nil {
		return err
	}

	tmpPath := lib.cachePath + ".tmp"
	dir := filepath.Dir(lib.cachePath)
	if dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0755)
	}

	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, lib.cachePath)
}

func (lib *Library) Search(q string, prefersAtmos bool, targetDuration int) []*Track {
	lib.mu.RLock()
	defer lib.mu.RUnlock()

	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		results := make([]*Track, len(lib.tracks))
		copy(results, lib.tracks)
		if prefersAtmos {
			sort.SliceStable(results, func(i, j int) bool {
				if results[i].IsAtmos && !results[j].IsAtmos {
					return true
				}
				return false
			})
		}
		if len(results) > 60 {
			return results[:60]
		}
		return results
	}

	qCore := ExtractCoreTitle(q)
	qTokens := strings.Fields(nonAlphanumericRegex.ReplaceAllString(q, " "))
	ctx := &QueryContext{
		QClean:         q,
		QueryCore:      qCore,
		QueryTokens:    qTokens,
		TargetDuration: targetDuration,
	}

	// Candidate filter
	var candidates []*Track
	for _, t := range lib.tracks {
		isCandidate := false
		if t.SearchWords != nil {
			for _, qTok := range qTokens {
				if _, ok := t.SearchWords[qTok]; ok {
					isCandidate = true
					break
				}
				if len(qTok) >= 4 {
					for _, tw := range t.SearchWordArray {
						if strings.HasPrefix(tw, qTok) || strings.HasPrefix(qTok, tw) {
							isCandidate = true
							break
						}
					}
					if isCandidate {
						break
					}
				}
			}
		} else {
			IndexTrackKeywords(t)
			isCandidate = true
		}

		if !isCandidate && len(q) >= 4 && strings.Contains(t.SearchFullText, q) {
			isCandidate = true
		}

		if isCandidate {
			candidates = append(candidates, t)
		}
	}

	pool := candidates
	if len(pool) == 0 {
		pool = lib.tracks
	}

	type scoredItem struct {
		track *Track
		score int
	}

	var scored []scoredItem
	for _, t := range pool {
		score := ScoreTrackMatch(t, ctx)
		if score > 0 && prefersAtmos && t.IsAtmos {
			score += 20
		}
		if score > 0 {
			scored = append(scored, scoredItem{track: t, score: score})
		}
	}

	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	limit := 60
	if len(scored) < limit {
		limit = len(scored)
	}

	results := make([]*Track, limit)
	for i := 0; i < limit; i++ {
		results[i] = scored[i].track
	}
	return results
}

func (lib *Library) LookupISRC(cleanCode string) []*Track {
	lib.mu.RLock()
	defer lib.mu.RUnlock()

	cleanCode = strings.ToUpper(strings.TrimSpace(cleanCode))
	var matches []*Track
	for _, t := range lib.tracks {
		if t.ISRC != "" && strings.ToUpper(strings.TrimSpace(t.ISRC)) == cleanCode {
			matches = append(matches, t)
		}
	}
	return matches
}

