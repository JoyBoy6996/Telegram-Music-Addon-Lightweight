package telegram

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"telegram-music-addon/internal/audio"
	"telegram-music-addon/internal/index"
	sessionpkg "telegram-music-addon/internal/session"
)

var (
	jpegHeaderHex = "ffd8ffe000104a46494600010100000100010000ffdb004300281c1e231e19282321232d2b28303c64413c37373c7b585d4964918099968f808c8aa0b4e6c3a0aadaad8a8cc8ffcbdaeef5ffffff9bc1fffffffaffe6fdfff8ffdb0043012b2d2d3c353c76414176f8a58ca5f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8ffc00011080000000003012200021101031101ffc4001f0000010501010101010100000000000000000102030405060708090a0bffc400b5100002010303020403050504040000017d01020300041105122131410613516107227114328191a1082342b1c11552d1f02433627282090a161718191a25262728292a3435363738393a434445464748494a535455565758595a636465666768696a737475767778797a838485868788898a92939495969798999aa2a3a4a5a6a7a8a9aab2b3b4b5b6b7b8b9bac2c3c4c5c6c7c8c9cad2d3d4d5d6d7d8d9dae1e2e3e4e5e6e7e8e9eaf1f2f3f4f5f6f7f8f9faffc4001f0100030101010101010101010000000000000102030405060708090a0bffc400b51100020102040403040705040400010277000102031104052131061241510761711322328108144291a1b1c109233352f0156272d10a162434e125f11718191a262728292a35363738393a434445464748494a535455565758595a636465666768696a737475767778797a82838485868788898a92939495969798999aa2a3a4a5a6a7a8a9aab2b3b4b5b6b7b8b9bac2c3c4c5c6c7c8c9cad2d3d4d5d6d7d8d9dae2e3e4e5e6e7e8e9eaf2f3f4f5f6f7f8f9faffda000c03010002110311003f00"
	jpegHeader, _ = hex.DecodeString(jpegHeaderHex)
	jpegFooter    = []byte{0xFF, 0xD9}
)

func StrippedPhotoToJpg(stripped []byte) []byte {
	if len(stripped) < 3 || stripped[0] != 1 {
		return stripped
	}
	header := make([]byte, len(jpegHeader))
	copy(header, jpegHeader)
	header[164] = stripped[1]
	header[166] = stripped[2]

	res := make([]byte, 0, len(header)+len(stripped)-3+len(jpegFooter))
	res = append(res, header...)
	res = append(res, stripped[3:]...)
	res = append(res, jpegFooter...)
	return res
}

type Client struct {
	client        *telegram.Client
	storage       *sessionpkg.StringStorage
	channelInput  string
	channelPeer   tg.InputPeerClass
	channelTitle  string
	downloadSem   chan struct{}
	docMu         sync.RWMutex
	docs          map[string]*tg.Document
	isIndexing    bool
	indexMu       sync.Mutex
	lastIndexed   time.Time
}

func NewClient(apiID int, apiHash, sessionStr, channelInput string) (*Client, error) {
	storage, err := sessionpkg.NewStringStorage(sessionStr)
	if err != nil {
		return nil, fmt.Errorf("failed to init session storage: %w", err)
	}

	c := &Client{
		storage:      storage,
		channelInput: channelInput,
		downloadSem:  make(chan struct{}, 2), // Max 2 concurrent MTProto downloads
		docs:         make(map[string]*tg.Document),
	}

	c.client = telegram.NewClient(apiID, apiHash, telegram.Options{
		SessionStorage: storage,
	})

	return c, nil
}

func (c *Client) Start(ctx context.Context) error {
	ready := make(chan error, 1)

	go func() {
		err := c.client.Run(ctx, func(ctx context.Context) error {
			authStatus, err := c.client.Auth().Status(ctx)
			if err != nil {
				ready <- fmt.Errorf("auth status check failed: %w", err)
				return err
			}
			if !authStatus.Authorized {
				ready <- errors.New("telegram session is not authorized. Please run `telegram-music-addon login` first")
				return errors.New("unauthorized")
			}

			// Resolve channel
			peer, title, err := c.resolveChannel(ctx, c.channelInput)
			if err != nil {
				ready <- fmt.Errorf("failed to resolve channel %q: %w", c.channelInput, err)
				return err
			}
			c.channelPeer = peer
			c.channelTitle = title
			log.Printf("[Channel] Connected to channel: %s", title)

			ready <- nil

			<-ctx.Done()
			return ctx.Err()
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("[Telegram] Client run error: %v", err)
		}
	}()

	select {
	case err := <-ready:
		return err
	case <-time.After(30 * time.Second):
		return errors.New("timeout connecting to Telegram MTProto")
	}
}

func (c *Client) resolveChannel(ctx context.Context, input string) (tg.InputPeerClass, string, error) {
	cleanInput := strings.TrimSpace(input)
	if cleanInput == "" {
		return nil, "", errors.New("empty channel input")
	}

	target := cleanInput
	tmeRe := regexp.MustCompile(`(?i)t\.me/(?:c/)?([a-zA-Z0-9_+-]+)`)
	if m := tmeRe.FindStringSubmatch(cleanInput); len(m) > 1 {
		target = m[1]
	}
	stripped := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(target, "-100"), "@"))

	// 1. Try ContactsResolveUsername if looks like a username
	if strings.HasPrefix(target, "@") || (!strings.HasPrefix(target, "-") && !isNumeric(target)) {
		uname := strings.TrimPrefix(target, "@")
		res, err := c.client.API().ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{
			Username: uname,
		})
		if err == nil {
			for _, ch := range res.Chats {
				if channel, ok := ch.(*tg.Channel); ok {
					return &tg.InputPeerChannel{
						ChannelID:  channel.ID,
						AccessHash: channel.AccessHash,
					}, channel.Title, nil
				}
			}
		}
	}

	// 2. Iterate Dialogs to find channel by numeric ID or title
	dialogs, err := c.client.API().MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		Limit: 200,
	})
	if err == nil {
		var chats []tg.ChatClass
		switch d := dialogs.(type) {
		case *tg.MessagesDialogs:
			chats = d.Chats
		case *tg.MessagesDialogsSlice:
			chats = d.Chats
		}

		for _, ch := range chats {
			if channel, ok := ch.(*tg.Channel); ok {
				chIDStr := strconv.FormatInt(channel.ID, 10)
				fullIDStr := fmt.Sprintf("-100%s", chIDStr)
				uname := strings.ToLower(channel.Username)
				title := strings.ToLower(channel.Title)

				if chIDStr == cleanInput || fullIDStr == cleanInput ||
					chIDStr == target || fullIDStr == target ||
					chIDStr == stripped || (uname != "" && (uname == stripped || uname == strings.ToLower(target))) ||
					title == strings.ToLower(cleanInput) || title == strings.ToLower(target) {
					return &tg.InputPeerChannel{
						ChannelID:  channel.ID,
						AccessHash: channel.AccessHash,
					}, channel.Title, nil
				}
			}
		}
	}

	// 3. Fallback: direct ChannelID parse if numeric
	if numID, err := strconv.ParseInt(stripped, 10, 64); err == nil {
		return &tg.InputPeerChannel{
			ChannelID:  numID,
			AccessHash: 0,
		}, fmt.Sprintf("Channel %d", numID), nil
	}

	return nil, "", fmt.Errorf("channel %q not found in user dialogs", input)
}

func isNumeric(s string) bool {
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}

// ── Document and Message Tracking ───────────────────────────────────────────

func (c *Client) SetDocument(msgID string, doc *tg.Document) {
	c.docMu.Lock()
	defer c.docMu.Unlock()
	c.docs[msgID] = doc
}

func (c *Client) GetDocument(msgID string) *tg.Document {
	c.docMu.RLock()
	defer c.docMu.RUnlock()
	return c.docs[msgID]
}

func (c *Client) RefreshDocument(ctx context.Context, msgID string) (*tg.Document, error) {
	id, err := strconv.Atoi(msgID)
	if err != nil {
		return nil, err
	}

	res, err := c.client.API().ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: toInputChannel(c.channelPeer),
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: id}},
	})
	if err != nil {
		return nil, err
	}

	var messages []tg.MessageClass
	switch m := res.(type) {
	case *tg.MessagesChannelMessages:
		messages = m.Messages
	case *tg.MessagesMessages:
		messages = m.Messages
	case *tg.MessagesMessagesSlice:
		messages = m.Messages
	}

	for _, m := range messages {
		if msg, ok := m.(*tg.Message); ok && msg.ID == id {
			if media, ok := msg.Media.(*tg.MessageMediaDocument); ok {
				if doc, ok := media.Document.(*tg.Document); ok {
					c.SetDocument(msgID, doc)
					return doc, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("message %s not found in channel", msgID)
}

func toInputChannel(peer tg.InputPeerClass) tg.InputChannelClass {
	if ch, ok := peer.(*tg.InputPeerChannel); ok {
		return &tg.InputChannel{
			ChannelID:  ch.ChannelID,
			AccessHash: ch.AccessHash,
		}
	}
	return nil
}

// ── Media Chunk Download with Backpressure & Semaphore ─────────────────────

func (c *Client) GetFileChunk(ctx context.Context, msgID string, offset int64, limit int) ([]byte, error) {
	select {
	case c.downloadSem <- struct{}{}:
		defer func() { <-c.downloadSem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	doc := c.GetDocument(msgID)
	if doc == nil {
		var err error
		doc, err = c.RefreshDocument(ctx, msgID)
		if err != nil {
			return nil, fmt.Errorf("document not found for message %s: %w", msgID, err)
		}
	}

	// Offset must be 4KB aligned for MTProto upload.getFile
	alignedOffset := (offset / 4096) * 4096
	skip := int(offset - alignedOffset)

	reqLimit := limit + skip
	// Limit must be a power of 2 or multiple of 4KB up to 512KB
	if reqLimit <= 128*1024 {
		reqLimit = 128 * 1024
	} else if reqLimit <= 256*1024 {
		reqLimit = 256 * 1024
	} else {
		reqLimit = 512 * 1024
	}

	req := &tg.UploadGetFileRequest{
		Location: &tg.InputDocumentFileLocation{
			ID:            doc.ID,
			AccessHash:    doc.AccessHash,
			FileReference: doc.FileReference,
		},
		Offset: alignedOffset,
		Limit:  reqLimit,
	}

	res, err := c.client.API().UploadGetFile(ctx, req)
	if err != nil {
		if isFileRefError(err) {
			log.Printf("[FileRef] File reference expired for msg %s. Refreshing...", msgID)
			refreshedDoc, rErr := c.RefreshDocument(ctx, msgID)
			if rErr == nil {
				req.Location = &tg.InputDocumentFileLocation{
					ID:            refreshedDoc.ID,
					AccessHash:    refreshedDoc.AccessHash,
					FileReference: refreshedDoc.FileReference,
				}
				res, err = c.client.API().UploadGetFile(ctx, req)
			}
		}
	}

	if err != nil {
		return nil, err
	}

	var data []byte
	switch f := res.(type) {
	case *tg.UploadFile:
		data = f.Bytes
	case *tg.UploadFileCDNRedirect:
		return nil, errors.New("CDN file redirect not supported")
	default:
		return nil, errors.New("unexpected upload.File response")
	}

	if skip >= len(data) {
		return nil, nil
	}
	data = data[skip:]
	if len(data) > limit {
		data = data[:limit]
	}

	return data, nil
}

func isFileRefError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToUpper(err.Error())
	return strings.Contains(s, "FILE_REFERENCE") || strings.Contains(s, "FILEREF")
}

// ── Artwork Thumbnail Extraction ────────────────────────────────────────────

func (c *Client) GetArtwork(ctx context.Context, msgID string) ([]byte, string, error) {
	doc := c.GetDocument(msgID)
	if doc == nil {
		var err error
		doc, err = c.RefreshDocument(ctx, msgID)
		if err != nil {
			return nil, "", err
		}
	}

	for _, sz := range doc.Thumbs {
		switch s := sz.(type) {
		case *tg.PhotoStrippedSize:
			jpg := StrippedPhotoToJpg(s.Bytes)
			return jpg, "image/jpeg", nil
		}
	}

	for _, sz := range doc.Thumbs {
		switch s := sz.(type) {
		case *tg.PhotoSize:
			req := &tg.UploadGetFileRequest{
				Location: &tg.InputDocumentFileLocation{
					ID:            doc.ID,
					AccessHash:    doc.AccessHash,
					FileReference: doc.FileReference,
					ThumbSize:     s.Type,
				},
				Offset: 0,
				Limit:  128 * 1024,
			}
			res, err := c.client.API().UploadGetFile(ctx, req)
			if err != nil && isFileRefError(err) {
				refreshedDoc, rErr := c.RefreshDocument(ctx, msgID)
				if rErr == nil {
					req.Location = &tg.InputDocumentFileLocation{
						ID:            refreshedDoc.ID,
						AccessHash:    refreshedDoc.AccessHash,
						FileReference: refreshedDoc.FileReference,
						ThumbSize:     s.Type,
					}
					res, err = c.client.API().UploadGetFile(ctx, req)
				}
			}
			if err == nil {
				if f, ok := res.(*tg.UploadFile); ok {
					return f.Bytes, "image/jpeg", nil
				}
			}
		}
	}

	return nil, "", errors.New("no artwork found")
}

// ── Channel Indexing ────────────────────────────────────────────────────────

func (c *Client) IndexChannel(ctx context.Context, lib *index.Library, onProgress func(indexed int)) error {
	c.indexMu.Lock()
	if c.isIndexing {
		c.indexMu.Unlock()
		return nil
	}
	c.isIndexing = true
	c.indexMu.Unlock()

	defer func() {
		c.indexMu.Lock()
		c.isIndexing = false
		c.lastIndexed = time.Now()
		c.indexMu.Unlock()
	}()

	log.Println("[Indexer] Starting channel audio indexing...")

	existingTracks := lib.GetAllTracks()
	existingMap := make(map[string]*index.Track, len(existingTracks))
	for _, t := range existingTracks {
		existingMap[t.ID] = t
	}

	var newTracks []*index.Track
	seenIDs := make(map[string]struct{})
	offsetID := 0
	batchCount := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		res, err := c.client.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:     c.channelPeer,
			OffsetID: offsetID,
			Limit:    100,
		})
		if err != nil {
			return fmt.Errorf("get history failed: %w", err)
		}

		var messages []tg.MessageClass
		switch m := res.(type) {
		case *tg.MessagesChannelMessages:
			messages = m.Messages
		case *tg.MessagesMessages:
			messages = m.Messages
		case *tg.MessagesMessagesSlice:
			messages = m.Messages
		}

		if len(messages) == 0 {
			break
		}

		lastMsgID := 0
		for _, m := range messages {
			msg, ok := m.(*tg.Message)
			if !ok {
				continue
			}
			lastMsgID = msg.ID
			msgIDStr := strconv.Itoa(msg.ID)

			if _, seen := seenIDs[msgIDStr]; seen {
				continue
			}
			seenIDs[msgIDStr] = struct{}{}

			mediaDoc, ok := msg.Media.(*tg.MessageMediaDocument)
			if !ok {
				continue
			}

			doc, ok := mediaDoc.Document.(*tg.Document)
			if !ok {
				continue
			}

			if !isAudioDocument(doc) {
				continue
			}

			c.SetDocument(msgIDStr, doc)

			// Reuse cached track if already present
			if existing, ok := existingMap[msgIDStr]; ok {
				if existing.SizeBytes == 0 && doc.Size > 0 {
					existing.SizeBytes = doc.Size
				}
				newTracks = append(newTracks, existing)
			} else {
				parsedTrack := c.parseTrackMessage(ctx, msg, doc)
				if parsedTrack != nil {
					newTracks = append(newTracks, parsedTrack)
					index.IndexTrackKeywords(parsedTrack)
				}
			}

			batchCount++
			if batchCount%25 == 0 {
				lib.SetTracks(newTracks)
				_ = lib.SaveCache()
				if onProgress != nil {
					onProgress(len(newTracks))
				}
			}
		}

		if len(messages) < 100 || lastMsgID == 0 {
			break
		}
		offsetID = lastMsgID
	}

	lib.SetTracks(newTracks)
	_ = lib.SaveCache()
	log.Printf("[Indexer] Indexing complete: %d audio tracks loaded", len(newTracks))
	return nil
}

func isAudioDocument(doc *tg.Document) bool {
	if doc == nil {
		return false
	}
	mime := strings.ToLower(doc.MimeType)
	var fileName string
	hasAudioAttr := false

	for _, a := range doc.Attributes {
		switch attr := a.(type) {
		case *tg.DocumentAttributeFilename:
			fileName = attr.FileName
		case *tg.DocumentAttributeAudio:
			hasAudioAttr = true
		case *tg.DocumentAttributeVideo:
			return false // Exclude video containers
		}
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(fileName), "."))
	if strings.HasPrefix(mime, "video/") || isVideoExt(ext) {
		return false
	}

	isAudioMime := strings.HasPrefix(mime, "audio/") || mime == "application/ogg" || mime == "application/x-flac"
	isAudioExt := isAudioExtension(ext)

	return isAudioMime || isAudioExt || hasAudioAttr
}

func isVideoExt(ext string) bool {
	switch ext {
	case "mp4", "mkv", "avi", "mov", "webm", "flv":
		return true
	default:
		return false
	}
}

var audioExtensions = map[string]string{
	"flac": "flac",
	"mp3":  "mp3",
	"m4a":  "m4a",
	"aac":  "aac",
	"wav":  "wav",
	"ogg":  "ogg",
	"opus": "opus",
	"alac": "alac",
	"ec3":  "eac3",
	"eac3": "eac3",
}

func isAudioExtension(ext string) bool {
	_, ok := audioExtensions[ext]
	return ok
}

func (c *Client) parseTrackMessage(ctx context.Context, msg *tg.Message, doc *tg.Document) *index.Track {
	msgIDStr := strconv.Itoa(msg.ID)
	var fileName string
	var audioAttr *tg.DocumentAttributeAudio

	for _, a := range doc.Attributes {
		switch attr := a.(type) {
		case *tg.DocumentAttributeFilename:
			fileName = attr.FileName
		case *tg.DocumentAttributeAudio:
			audioAttr = attr
		}
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(fileName), "."))
	if ext == "" {
		ext = "mp3"
	}
	fallbackTitle := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	if fallbackTitle == "" {
		fallbackTitle = fmt.Sprintf("Track %s", msgIDStr)
	}

	title := fallbackTitle
	artist := "Unknown Artist"
	duration := 0

	if audioAttr != nil {
		if audioAttr.Title != "" {
			title = strings.TrimSpace(audioAttr.Title)
		}
		if audioAttr.Performer != "" {
			artist = strings.TrimSpace(audioAttr.Performer)
		}
		if audioAttr.Duration > 0 {
			duration = audioAttr.Duration
		}
	}

	// Sniff tags from first 64 KB
	var tagMeta *audio.TagMetadata
	if doc.Size > 0 {
		probeLimit := 64 * 1024
		if int64(probeLimit) > doc.Size {
			probeLimit = int(doc.Size)
		}
		headerBuf, err := c.GetFileChunk(ctx, msgIDStr, 0, probeLimit)
		if err == nil && len(headerBuf) > 0 {
			tagMeta = audio.SniffAudioHeader(headerBuf, ext)
			if tagMeta.Title != "" {
				title = tagMeta.Title
			}
			if tagMeta.Artist != "" {
				artist = tagMeta.Artist
			}
			if duration == 0 && tagMeta.Duration > 0 {
				duration = tagMeta.Duration
			}
		}
	}

	if tagMeta == nil {
		tagMeta = &audio.TagMetadata{}
	}

	// Atmos check
	isAtmos := tagMeta.IsAtmos ||
		audio.IsAtmosString(fileName) ||
		audio.IsAtmosString(msg.Message) ||
		audio.IsAtmosString(title) ||
		ext == "ec3" || ext == "eac3"

	formatName := audioExtensions[ext]
	if formatName == "" {
		formatName = ext
	}

	sampleRate := tagMeta.SampleRate
	bitDepth := tagMeta.BitDepth
	if isAtmos {
		formatName = "eac3-joc"
		if sampleRate == 0 {
			sampleRate = 48000
		}
		if bitDepth == 0 {
			bitDepth = 16
		}
	} else if formatName == "m4a" && tagMeta.Codec == "alac" {
		formatName = "alac"
		if bitDepth == 0 {
			bitDepth = 16
		}
		if sampleRate == 0 {
			sampleRate = 48000
		}
	}

	qualityText := strings.ToUpper(formatName)
	if isAtmos {
		qualityText = "Dolby Atmos"
	} else if bitDepth > 0 && sampleRate > 0 {
		qualityText = fmt.Sprintf("%d-bit / %.1fkHz %s", bitDepth, float64(sampleRate)/1000.0, strings.ToUpper(formatName))
	} else if formatName == "flac" || formatName == "wav" || formatName == "alac" {
		qualityText = fmt.Sprintf("16-bit / 44.1kHz %s Lossless", strings.ToUpper(formatName))
	}

	isLossless := formatName == "flac" || formatName == "alac" || formatName == "wav"
	isHiRes := isLossless && (bitDepth > 16 || sampleRate > 44100)
	qualityTier := "HIGH"
	if isAtmos {
		qualityTier = "DOLBY_ATMOS"
	} else if isHiRes {
		qualityTier = "HI_RES"
	} else if isLossless {
		qualityTier = "LOSSLESS"
	}

	hasArtwork := len(doc.Thumbs) > 0
	mime := doc.MimeType
	if isAtmos {
		mime = "audio/mp4"
	} else if mime == "" {
		if formatName == "flac" {
			mime = "audio/flac"
		} else {
			mime = "audio/mpeg"
		}
	}

	msgLower := strings.ToLower(msg.Message)
	keep := strings.Contains(msgLower, "/keep") || strings.Contains(msgLower, "/ig") || strings.Contains(msgLower, "#keep")

	return &index.Track{
		ID:           msgIDStr,
		Title:        title,
		Artist:       artist,
		Album:        tagMeta.Album,
		Duration:     duration,
		Format:       formatName,
		AudioQuality: qualityTier,
		Quality:      qualityText,
		Tier:         qualityTier,
		BitDepth:     bitDepth,
		SampleRate:   sampleRate,
		SizeBytes:    doc.Size,
		AudioModes:   []string{"STEREO"},
		IsAtmos:      isAtmos,
		HasArtwork:   hasArtwork,
		MimeType:     mime,
		ISRC:         tagMeta.ISRC,
		Keep:         keep,
	}
}

