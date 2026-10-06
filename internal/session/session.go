package session

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/gotd/td/crypto"
	"github.com/gotd/td/session"
)

type jsonData struct {
	Version int          `json:"Version"`
	Data    session.Data `json:"Data"`
}

const latestVersion = 1

// StringStorage implements session.Storage in memory and allows converting to/from a session string.
type StringStorage struct {
	mu   sync.RWMutex
	data []byte
}

func NewStringStorage(initialString string) (*StringStorage, error) {
	s := &StringStorage{}
	if initialString != "" {
		initialString = strings.TrimSpace(initialString)
		parsedData, err := ParseSessionString(initialString)
		if err != nil {
			return nil, fmt.Errorf("failed to parse initial session string: %w", err)
		}
		marshaled, err := json.Marshal(jsonData{
			Version: latestVersion,
			Data:    *parsedData,
		})
		if err != nil {
			return nil, err
		}
		s.data = marshaled
	}
	return s, nil
}

func (s *StringStorage) LoadSession(ctx context.Context) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.data) == 0 {
		return nil, session.ErrNotFound
	}
	cpy := make([]byte, len(s.data))
	copy(cpy, s.data)
	return cpy, nil
}

func (s *StringStorage) StoreSession(ctx context.Context, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = make([]byte, len(data))
	copy(s.data, data)
	return nil
}

func (s *StringStorage) GetSessionData() (*session.Data, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.data) == 0 {
		return nil, session.ErrNotFound
	}
	var v jsonData
	if err := json.Unmarshal(s.data, &v); err != nil {
		return nil, err
	}
	return &v.Data, nil
}

func (s *StringStorage) SaveToTelethonString() (string, error) {
	data, err := s.GetSessionData()
	if err != nil {
		return "", err
	}
	return DataToTelethon(data)
}

// ParseSessionString parses either a Telethon/GramJS StringSession (starting with '1')
// or a gotd JSON/base64 session string.
func ParseSessionString(s string) (*session.Data, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty session string")
	}

	// 1. GramJS / Telethon string session format: version byte '1' + base64
	if s[0] == '1' {
		// Try gotd's built-in TelethonSession first (uses URLEncoding)
		data, err := session.TelethonSession(s)
		if err == nil {
			return data, nil
		}

		// Fallback for standard base64 encoding (used by some GramJS/Python versions)
		b64 := s[1:]
		if m := len(b64) % 4; m != 0 {
			b64 += strings.Repeat("=", 4-m)
		}

		var raw []byte
		raw, decErr := base64.StdEncoding.DecodeString(b64)
		if decErr != nil {
			raw, decErr = base64.URLEncoding.DecodeString(b64)
		}
		if decErr == nil {
			return decodeTelethonRaw(raw)
		}
		return nil, fmt.Errorf("failed to decode Telethon session: %w (fallback: %v)", err, decErr)
	}

	// 2. Try JSON representation
	var v jsonData
	if err := json.Unmarshal([]byte(s), &v); err == nil && v.Version == latestVersion {
		return &v.Data, nil
	}

	// 3. Try base64-encoded gotd JSON
	if decoded, err := base64.StdEncoding.DecodeString(s); err == nil {
		var v64 jsonData
		if err := json.Unmarshal(decoded, &v64); err == nil && v64.Version == latestVersion {
			return &v64.Data, nil
		}
	}

	return nil, errors.New("unrecognized session string format (expected GramJS/Telethon starting with '1' or gotd JSON)")
}

func decodeTelethonRaw(data []byte) (*session.Data, error) {
	var ipLength int
	switch len(data) {
	case 263:
		ipLength = 4
	case 275:
		ipLength = 16
	default:
		return nil, fmt.Errorf("decoded session raw bytes have invalid length: %d (expected 263 or 275)", len(data))
	}

	dcID := data[0]
	addr := make(net.IP, 0, 16)
	addr = append(addr, data[1:1+ipLength]...)
	port := binary.BigEndian.Uint16(data[1+ipLength : 3+ipLength])

	var key crypto.Key
	copy(key[:], data[3+ipLength:])
	id := key.WithID().ID

	return &session.Data{
		DC:        int(dcID),
		Addr:      net.JoinHostPort(addr.String(), strconv.Itoa(int(port))),
		AuthKey:   key[:],
		AuthKeyID: id[:],
	}, nil
}

// DataToTelethon converts session.Data into Telethon/GramJS StringSession format.
func DataToTelethon(d *session.Data) (string, error) {
	if d == nil {
		return "", errors.New("nil session data")
	}

	host, portStr, err := net.SplitHostPort(d.Addr)
	if err != nil {
		// If no port specified, default to 443
		host = d.Addr
		portStr = "443"
	}

	ip := net.ParseIP(host)
	if ip == nil {
		// Resolve IP or use default DC IP if domain
		ips, err := net.LookupIP(host)
		if err == nil && len(ips) > 0 {
			ip = ips[0]
		} else {
			// Fallback: Telegram DC 2 production IP
			ip = net.ParseIP("149.154.167.50")
		}
	}

	port, _ := strconv.Atoi(portStr)
	if port <= 0 {
		port = 443
	}

	ip4 := ip.To4()
	var raw []byte
	if ip4 != nil {
		raw = make([]byte, 1+4+2+256)
		raw[0] = byte(d.DC)
		copy(raw[1:5], ip4)
		binary.BigEndian.PutUint16(raw[5:7], uint16(port))
		copy(raw[7:], d.AuthKey)
	} else {
		raw = make([]byte, 1+16+2+256)
		raw[0] = byte(d.DC)
		copy(raw[1:17], ip.To16())
		binary.BigEndian.PutUint16(raw[17:19], uint16(port))
		copy(raw[19:], d.AuthKey)
	}

	return "1" + base64.URLEncoding.EncodeToString(raw), nil
}

