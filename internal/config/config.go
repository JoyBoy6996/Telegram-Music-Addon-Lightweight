package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	ApiID         int
	ApiHash       string
	SessionString string
	Channel       string
	Port          int
	UrlSecret     string
	PublicURL     string
}

func CleanEnv(val string) string {
	s := strings.TrimSpace(val)
	if (strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"")) ||
		(strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'")) {
		if len(s) >= 2 {
			s = strings.TrimSpace(s[1 : len(s)-1])
		}
	}
	return s
}

// LoadEnv reads a .env file from the current directory if it exists.
func LoadEnv(path string) map[string]string {
	envMap := make(map[string]string)
	if path == "" {
		path = ".env"
	}

	f, err := os.Open(path)
	if err != nil {
		return envMap
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx != -1 {
			k := strings.TrimSpace(line[:idx])
			v := strings.TrimSpace(line[idx+1:])
			v = CleanEnv(v)
			envMap[k] = v
			// If not already in OS environment, set it
			if _, exists := os.LookupEnv(k); !exists {
				_ = os.Setenv(k, v)
			}
		}
	}
	return envMap
}

// SaveEnvKey writes or updates a key=value in the .env file.
func SaveEnvKey(path, key, value string) error {
	if path == "" {
		path = ".env"
	}

	content := ""
	if data, err := os.ReadFile(path); err == nil {
		content = string(data)
	}

	safeVal := value
	if strings.Contains(safeVal, " ") || strings.Contains(safeVal, "\"") {
		safeVal = fmt.Sprintf("\"%s\"", strings.ReplaceAll(safeVal, "\"", "\\\""))
	}

	re := regexp.MustCompile(fmt.Sprintf(`(?m)^%s=.*$`, regexp.QuoteMeta(key)))
	if re.MatchString(content) {
		content = re.ReplaceAllString(content, fmt.Sprintf("%s=%s", key, safeVal))
	} else {
		content = strings.TrimRight(content, "\r\n")
		if len(content) > 0 {
			content += "\n"
		}
		content += fmt.Sprintf("%s=%s\n", key, safeVal)
	}

	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0755)
	}

	return os.WriteFile(path, []byte(content), 0644)
}

// LoadConfig loads configuration from environment variables and .env file.
func LoadConfig() *Config {
	LoadEnv(".env")

	apiIDStr := CleanEnv(os.Getenv("TELEGRAM_API_ID"))
	apiID, _ := strconv.Atoi(apiIDStr)

	portStr := CleanEnv(os.Getenv("PORT"))
	port, _ := strconv.Atoi(portStr)
	if port <= 0 {
		port = 3000
	}

	secret := CleanEnv(os.Getenv("URL_SECRET"))
	if secret == "" {
		secret = CleanEnv(os.Getenv("ACCESS_TOKEN"))
	}

	return &Config{
		ApiID:         apiID,
		ApiHash:       CleanEnv(os.Getenv("TELEGRAM_API_HASH")),
		SessionString: CleanEnv(os.Getenv("TELEGRAM_SESSION_STRING")),
		Channel:       CleanEnv(os.Getenv("TELEGRAM_CHANNEL")),
		Port:          port,
		UrlSecret:     secret,
		PublicURL:     CleanEnv(os.Getenv("PUBLIC_URL")),
	}
}

