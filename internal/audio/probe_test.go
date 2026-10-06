package audio

import (
	"encoding/binary"
	"testing"
)

func TestHasEac3SyncWords(t *testing.T) {
	buf := make([]byte, 500)
	// Frame 1
	buf[50] = 0x0B
	buf[51] = 0x77
	buf[52] = 0x00 // strmtyp=0, frmsiz high bits 0
	buf[53] = 0x5F // frmsiz = 95 -> frameBytes = (95+1)*2 = 192 bytes
	// Frame 2 at 50 + 192 = 242
	buf[242] = 0x0B
	buf[243] = 0x77

	if !HasEac3SyncWords(buf) {
		t.Errorf("Expected HasEac3SyncWords to return true")
	}

	// Corrupt Frame 2
	buf[242] = 0x00
	if HasEac3SyncWords(buf) {
		t.Errorf("Expected HasEac3SyncWords to return false on corrupted second syncword")
	}
}

func TestFlacStreamInfo(t *testing.T) {
	// Build minimal FLAC header with STREAMINFO block
	buf := make([]byte, 100)
	copy(buf[0:4], "fLaC")
	// Block header: type 0 (STREAMINFO), isLast 1, length 34
	buf[4] = 0x80 // isLast = true, type = 0
	buf[5] = 0x00
	buf[6] = 0x00
	buf[7] = 34 // length = 34

	// STREAMINFO:
	// bytes 8..17: min/max block size (4), min/max frame size (6)
	// sample rate = 44100 (0x0AC44)
	// channels = 2 (channels-1 = 1)
	// bits per sample = 16 (bps-1 = 15 = 0x0F)
	// total samples = 44100 * 200 = 8820000 (0x0000869520)
	// byte 18 (sr[19:12]): 0x0A
	// byte 19 (sr[11:4]): 0xC4
	// byte 20 (sr[3:0] | (chan-1)[2:0] | (bps-1)[4]): (4<<4) | (1<<1) | 0 = 0x42
	// byte 21 ((bps-1)[3:0] | totalSamples[35:32]): (0x0F<<4) | 0x00 = 0xF0
	// byte 22..25 (totalSamples[31:0]): 8820000
	buf[18] = 0x0A
	buf[19] = 0xC4
	buf[20] = 0x42
	buf[21] = 0xF0
	binary.BigEndian.PutUint32(buf[22:26], 8820000)

	meta := SniffAudioHeader(buf, "flac")
	if meta.Codec != "flac" {
		t.Errorf("Expected codec flac, got %s", meta.Codec)
	}
	if meta.SampleRate != 44100 {
		t.Errorf("Expected sample rate 44100, got %d", meta.SampleRate)
	}
	if meta.BitDepth != 16 {
		t.Errorf("Expected bit depth 16, got %d", meta.BitDepth)
	}
	if meta.Duration != 200 {
		t.Errorf("Expected duration 200, got %d", meta.Duration)
	}
}

func TestID3v2(t *testing.T) {
	// Build minimal ID3v2.3 tag with TIT2 and TPE1
	buf := make([]byte, 100)
	copy(buf[0:3], "ID3")
	buf[3] = 3 // ID3v2.3
	buf[4] = 0 // revision
	buf[5] = 0 // flags
	// Size: synchsafe 80 bytes
	buf[9] = 80

	offset := 10
	// TIT2: Title "Test Song"
	copy(buf[offset:offset+4], "TIT2")
	binary.BigEndian.PutUint32(buf[offset+4:offset+8], 10) // 1 byte enc + 9 bytes text
	buf[offset+10] = 3                                    // utf-8
	copy(buf[offset+11:], "Test Song")
	offset += 20

	// TPE1: Artist "Test Artist"
	copy(buf[offset:offset+4], "TPE1")
	binary.BigEndian.PutUint32(buf[offset+4:offset+8], 12)
	buf[offset+10] = 3
	copy(buf[offset+11:], "Test Artist")

	meta := SniffAudioHeader(buf, "mp3")
	if meta.Title != "Test Song" {
		t.Errorf("Expected title 'Test Song', got '%s'", meta.Title)
	}
	if meta.Artist != "Test Artist" {
		t.Errorf("Expected artist 'Test Artist', got '%s'", meta.Artist)
	}
}

