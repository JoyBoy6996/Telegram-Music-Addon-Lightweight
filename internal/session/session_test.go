package session

import (
	"crypto/rand"
	"testing"

	gotdsession "github.com/gotd/td/session"
)

func TestSessionTelethonRoundtrip(t *testing.T) {
	authKey := make([]byte, 256)
	_, _ = rand.Read(authKey)

	original := &gotdsession.Data{
		DC:      2,
		Addr:    "149.154.167.50:443",
		AuthKey: authKey,
	}

	str, err := DataToTelethon(original)
	if err != nil {
		t.Fatalf("DataToTelethon failed: %v", err)
	}

	if len(str) == 0 || str[0] != '1' {
		t.Fatalf("Expected session string to start with '1', got: %s", str)
	}

	parsed, err := ParseSessionString(str)
	if err != nil {
		t.Fatalf("ParseSessionString failed: %v", err)
	}

	if parsed.DC != original.DC {
		t.Errorf("DC mismatch: got %d, expected %d", parsed.DC, original.DC)
	}
	if parsed.Addr != original.Addr {
		t.Errorf("Addr mismatch: got %s, expected %s", parsed.Addr, original.Addr)
	}
	if len(parsed.AuthKey) != len(original.AuthKey) {
		t.Errorf("AuthKey length mismatch: got %d, expected %d", len(parsed.AuthKey), len(original.AuthKey))
	}
}

func TestStringStorage(t *testing.T) {
	authKey := make([]byte, 256)
	_, _ = rand.Read(authKey)

	original := &gotdsession.Data{
		DC:      4,
		Addr:    "149.154.167.91:443",
		AuthKey: authKey,
	}

	str, err := DataToTelethon(original)
	if err != nil {
		t.Fatalf("DataToTelethon failed: %v", err)
	}

	storage, err := NewStringStorage(str)
	if err != nil {
		t.Fatalf("NewStringStorage failed: %v", err)
	}

	data, err := storage.GetSessionData()
	if err != nil {
		t.Fatalf("GetSessionData failed: %v", err)
	}

	if data.DC != 4 {
		t.Errorf("Expected DC 4, got %d", data.DC)
	}

	savedStr, err := storage.SaveToTelethonString()
	if err != nil {
		t.Fatalf("SaveToTelethonString failed: %v", err)
	}

	if savedStr != str {
		t.Errorf("Saved string mismatch:\ngot:  %s\nwant: %s", savedStr, str)
	}
}

