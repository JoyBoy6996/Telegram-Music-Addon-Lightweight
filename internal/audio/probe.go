package audio

import (
	"bytes"
	"encoding/binary"
	"regexp"
	"strings"
	"unicode/utf16"
)

type TagMetadata struct {
	Title      string
	Artist     string
	Album      string
	ISRC       string
	Duration   int
	SampleRate int
	BitDepth   int
	Bitrate    int
	Codec      string
	IsAtmos    bool
}

var atmosRegex = regexp.MustCompile(`(?i)\b(atmos|dolby\s*atmos|eac3[-\s]?joc|eac3|ec3|e-ac-3|spatial\s*audio)\b|\[atmos\]|\(atmos\)`)

// IsAtmosString checks if string contains Atmos indicators.
func IsAtmosString(s string) bool {
	return atmosRegex.MatchString(s)
}

// HasEac3SyncWords checks for Dolby Digital Plus / E-AC-3 sync frames (0x0B77).
func HasEac3SyncWords(buf []byte) bool {
	if len(buf) < 200 {
		return false
	}
	for i := 0; i < len(buf)-200; i++ {
		if buf[i] == 0x0b && buf[i+1] == 0x77 {
			strmtyp := (buf[i+2] >> 6) & 0x03
			if strmtyp <= 2 {
				frmsiz := (int(buf[i+2]&0x07) << 8) | int(buf[i+3])
				frameBytes := (frmsiz + 1) * 2
				if frameBytes >= 96 && frameBytes <= 4096 && (i+frameBytes+1) < len(buf) {
					if buf[i+frameBytes] == 0x0b && buf[i+frameBytes+1] == 0x77 {
						return true
					}
				}
			}
		}
	}
	return false
}

// SniffAudioHeader parses the first 64 KB-128 KB of an audio file to extract tags and audio specs.
func SniffAudioHeader(data []byte, ext string) *TagMetadata {
	meta := &TagMetadata{}
	if len(data) == 0 {
		return meta
	}

	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	if ext == "ec3" || ext == "eac3" {
		meta.IsAtmos = true
		meta.Codec = "eac3-joc"
		meta.SampleRate = 48000
		meta.BitDepth = 16
		return meta
	}

	// 1. FLAC
	if bytes.HasPrefix(data, []byte("fLaC")) {
		parseFlacHeader(data, meta)
		return meta
	}

	// 2. MP4 / M4A
	if ext == "m4a" || ext == "mp4" || bytes.Contains(data[:min(32, len(data))], []byte("ftyp")) {
		parseMp4Header(data, meta)
		return meta
	}

	// 3. MP3 ID3v2
	if bytes.HasPrefix(data, []byte("ID3")) {
		parseID3v2(data, meta)
		return meta
	}

	// Fallback check for E-AC-3 sync words
	if HasEac3SyncWords(data) {
		meta.IsAtmos = true
		meta.Codec = "eac3-joc"
		if meta.SampleRate == 0 {
			meta.SampleRate = 48000
		}
		if meta.BitDepth == 0 {
			meta.BitDepth = 16
		}
	}

	return meta
}

// ── FLAC Parser ─────────────────────────────────────────────────────────────

func parseFlacHeader(data []byte, meta *TagMetadata) {
	meta.Codec = "flac"
	if len(data) < 8 {
		return
	}

	offset := 4
	for offset+4 <= len(data) {
		header := data[offset]
		isLast := (header & 0x80) != 0
		blockType := header & 0x7F
		length := int(data[offset+1])<<16 | int(data[offset+2])<<8 | int(data[offset+3])
		offset += 4

		if offset+length > len(data) {
			// Incomplete block in this chunk
			length = len(data) - offset
		}

		block := data[offset : offset+length]

		if blockType == 0 && len(block) >= 18 { // STREAMINFO
			// Sample rate: 20 bits (bytes 10, 11, and top 4 bits of byte 12)
			sr := (uint32(block[10]) << 12) | (uint32(block[11]) << 4) | (uint32(block[12]) >> 4)
			// Channels: 3 bits (bits 1-3 of byte 12) + 1
			// Bits per sample: 5 bits (bits 4-7 of byte 12 and bit 0 of byte 13) + 1
			bps := (((block[12] & 0x01) << 4) | (block[13] >> 4)) + 1
			// Total samples: 36 bits
			totalSamples := (uint64(block[13]&0x0F) << 32) |
				(uint64(block[14]) << 24) |
				(uint64(block[15]) << 16) |
				(uint64(block[16]) << 8) |
				uint64(block[17])

			meta.SampleRate = int(sr)
			meta.BitDepth = int(bps)
			if sr > 0 && totalSamples > 0 {
				meta.Duration = int(totalSamples / uint64(sr))
			}
		} else if blockType == 4 { // VORBIS_COMMENT
			parseVorbisComments(block, meta)
		}

		offset += length
		if isLast || offset >= len(data) {
			break
		}
	}
}

func parseVorbisComments(data []byte, meta *TagMetadata) {
	if len(data) < 8 {
		return
	}
	vendorLen := int(binary.LittleEndian.Uint32(data[0:4]))
	offset := 4 + vendorLen
	if offset+4 > len(data) {
		return
	}

	numComments := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
	offset += 4

	for i := 0; i < numComments && offset+4 <= len(data); i++ {
		cLen := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
		offset += 4
		if offset+cLen > len(data) {
			break
		}
		comment := string(data[offset : offset+cLen])
		offset += cLen

		eqIdx := strings.Index(comment, "=")
		if eqIdx != -1 {
			k := strings.ToUpper(strings.TrimSpace(comment[:eqIdx]))
			v := strings.TrimSpace(comment[eqIdx+1:])
			switch k {
			case "TITLE":
				if meta.Title == "" {
					meta.Title = v
				}
			case "ARTIST":
				if meta.Artist == "" {
					meta.Artist = v
				} else {
					meta.Artist += ", " + v
				}
			case "ALBUM":
				if meta.Album == "" {
					meta.Album = v
				}
			case "ISRC":
				if meta.ISRC == "" {
					meta.ISRC = strings.ToUpper(v)
				}
			}
		}
	}
}

// ── MP4 / M4A Parser ────────────────────────────────────────────────────────

func parseMp4Header(data []byte, meta *TagMetadata) {
	meta.Codec = "m4a"

	// Check string content for Atmos / Spatial Audio or ALAC
	headerStr := string(data)
	if strings.Contains(headerStr, "ec-3") ||
		strings.Contains(headerStr, "dec3") ||
		strings.Contains(headerStr, "damf") ||
		strings.Contains(headerStr, "SpatialAudio") ||
		HasEac3SyncWords(data) {
		meta.IsAtmos = true
		meta.Codec = "eac3-joc"
		meta.SampleRate = 48000
		meta.BitDepth = 16
	} else if strings.Contains(headerStr, "alac") {
		meta.Codec = "alac"
		if meta.BitDepth == 0 {
			meta.BitDepth = 16
		}
		if meta.SampleRate == 0 {
			meta.SampleRate = 48000
		}
	}

	dur := parseMp4Duration(data)
	if dur > 0 {
		meta.Duration = dur
	}

	// Parse iTunes metadata (ilst box)
	parseIlstBox(data, meta)
}

func parseMp4Duration(buf []byte) int {
	if len(buf) < 32 {
		return 0
	}
	idx := bytes.Index(buf, []byte("mvhd"))
	if idx == -1 || idx+24 > len(buf) {
		return 0
	}

	version := buf[idx+4]
	var timescale uint32
	var durationVal uint64

	if version == 0 && idx+24 <= len(buf) {
		timescale = binary.BigEndian.Uint32(buf[idx+16 : idx+20])
		durationVal = uint64(binary.BigEndian.Uint32(buf[idx+20 : idx+24]))
	} else if version == 1 && idx+36 <= len(buf) {
		timescale = binary.BigEndian.Uint32(buf[idx+24 : idx+28])
		durationVal = binary.BigEndian.Uint64(buf[idx+28 : idx+36])
	}

	if timescale > 0 && durationVal > 0 {
		sec := int(durationVal / uint64(timescale))
		if sec > 0 && sec < 86400 {
			return sec
		}
	}
	return 0
}

func parseIlstBox(data []byte, meta *TagMetadata) {
	ilstIdx := bytes.Index(data, []byte("ilst"))
	if ilstIdx == -1 {
		return
	}

	offset := ilstIdx + 4
	for offset+8 <= len(data) {
		boxSize := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		if boxSize < 8 || offset+boxSize > len(data) {
			break
		}
		boxType := string(data[offset+4 : offset+8])
		boxData := data[offset+8 : offset+boxSize]

		val := extractDataAtomValue(boxData)
		if val != "" {
			switch boxType {
			case "\xa9nam": // Title
				if meta.Title == "" {
					meta.Title = val
				}
			case "\xa9ART": // Artist
				if meta.Artist == "" {
					meta.Artist = val
				}
			case "\xa9alb": // Album
				if meta.Album == "" {
					meta.Album = val
				}
			case "isrc": // ISRC
				if meta.ISRC == "" {
					meta.ISRC = strings.ToUpper(val)
				}
			case "----": // Freeform iTunes atom
				if strings.Contains(string(boxData), "ISRC") && meta.ISRC == "" {
					meta.ISRC = strings.ToUpper(val)
				}
			}
		}

		offset += boxSize
	}
}

func extractDataAtomValue(boxData []byte) string {
	dIdx := bytes.Index(boxData, []byte("data"))
	if dIdx == -1 || dIdx+12 > len(boxData) {
		return ""
	}
	dataSize := int(binary.BigEndian.Uint32(boxData[dIdx-4 : dIdx]))
	if dataSize < 16 || dIdx-4+dataSize > len(boxData) {
		dataSize = len(boxData) - (dIdx - 4)
	}
	content := boxData[dIdx+8 : dIdx-4+dataSize]
	return strings.TrimSpace(string(content))
}

// ── ID3v2 Parser ────────────────────────────────────────────────────────────

func parseID3v2(data []byte, meta *TagMetadata) {
	meta.Codec = "mp3"
	if len(data) < 10 {
		return
	}

	major := data[3]
	tagSize := synchsafeToInt(data[6:10])
	if tagSize <= 0 || tagSize+10 > len(data) {
		tagSize = len(data) - 10
	}

	offset := 10
	end := 10 + tagSize

	for offset+10 <= end {
		frameID := string(data[offset : offset+4])
		if data[offset] == 0 {
			break
		}

		var frameSize int
		if major == 4 {
			frameSize = synchsafeToInt(data[offset+4 : offset+8])
		} else {
			frameSize = int(binary.BigEndian.Uint32(data[offset+4 : offset+8]))
		}

		offset += 10
		if frameSize <= 0 || offset+frameSize > end {
			break
		}

		frameData := data[offset : offset+frameSize]
		text := decodeID3Text(frameData)

		switch frameID {
		case "TIT2":
			if meta.Title == "" {
				meta.Title = text
			}
		case "TPE1":
			if meta.Artist == "" {
				meta.Artist = text
			}
		case "TALB":
			if meta.Album == "" {
				meta.Album = text
			}
		case "TSRC":
			if meta.ISRC == "" {
				meta.ISRC = strings.ToUpper(text)
			}
		}

		offset += frameSize
	}
}

func synchsafeToInt(b []byte) int {
	if len(b) < 4 {
		return 0
	}
	return int(b[0]&0x7F)<<21 | int(b[1]&0x7F)<<14 | int(b[2]&0x7F)<<7 | int(b[3]&0x7F)
}

func decodeID3Text(data []byte) string {
	if len(data) < 2 {
		return ""
	}
	encoding := data[0]
	raw := data[1:]

	switch encoding {
	case 0: // ISO-8859-1 / ASCII
		return strings.Trim(string(raw), "\x00 \t\r\n")
	case 1: // UTF-16 with BOM
		if len(raw) < 2 {
			return ""
		}
		var bom binary.ByteOrder = binary.BigEndian
		if raw[0] == 0xFF && raw[1] == 0xFE {
			bom = binary.LittleEndian
			raw = raw[2:]
		} else if raw[0] == 0xFE && raw[1] == 0xFF {
			raw = raw[2:]
		}
		u16 := make([]uint16, len(raw)/2)
		for i := 0; i < len(u16); i++ {
			u16[i] = bom.Uint16(raw[i*2 : i*2+2])
		}
		return strings.Trim(string(utf16.Decode(u16)), "\x00 \t\r\n")
	case 2: // UTF-16BE without BOM
		u16 := make([]uint16, len(raw)/2)
		for i := 0; i < len(u16); i++ {
			u16[i] = binary.BigEndian.Uint16(raw[i*2 : i*2+2])
		}
		return strings.Trim(string(utf16.Decode(u16)), "\x00 \t\r\n")
	case 3: // UTF-8
		return strings.Trim(string(raw), "\x00 \t\r\n")
	default:
		return strings.Trim(string(raw), "\x00 \t\r\n")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

