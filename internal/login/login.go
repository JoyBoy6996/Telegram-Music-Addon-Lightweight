package login

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"

	"telegram-music-addon/internal/config"
	sessionpkg "telegram-music-addon/internal/session"
)

func promptInput(reader *bufio.Reader, prompt string) string {
	fmt.Print(prompt)
	text, _ := reader.ReadString('\n')
	return strings.TrimSpace(text)
}

// RunLogin executes the interactive login subcommand to generate a session string.
func RunLogin() error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("=================================================================")
	fmt.Println("       BitChord Telegram Music Addon - Login Setup (Go)          ")
	fmt.Println("=================================================================")
	fmt.Println()

	cfg := config.LoadConfig()

	apiID := cfg.ApiID
	apiHash := cfg.ApiHash

	if apiID == 0 || apiHash == "" {
		fmt.Println("You need Telegram API credentials to connect.")
		fmt.Println("If you do not have them yet:")
		fmt.Println("  1. Go to https://my.telegram.org")
		fmt.Println("  2. Log in with your phone number")
		fmt.Println("  3. Click 'API development tools' and create an app to get API ID & Hash.")
		fmt.Println()

		if apiID == 0 {
			val := promptInput(reader, "Enter your TELEGRAM_API_ID (numbers only): ")
			id, err := strconv.Atoi(val)
			if err != nil || id == 0 {
				return fmt.Errorf("invalid API ID provided: %s", val)
			}
			apiID = id
		}

		if apiHash == "" {
			apiHash = promptInput(reader, "Enter your TELEGRAM_API_HASH: ")
			if apiHash == "" {
				return fmt.Errorf("empty API Hash provided")
			}
		}
	}

	phone := promptInput(reader, "Your phone number (with international code, e.g. +1... or +91...): ")
	if phone == "" {
		return fmt.Errorf("phone number cannot be empty")
	}

	storage, err := sessionpkg.NewStringStorage("")
	if err != nil {
		return fmt.Errorf("failed to init session storage: %w", err)
	}

	client := telegram.NewClient(apiID, apiHash, telegram.Options{
		SessionStorage: storage,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	fmt.Println("\nConnecting to Telegram to authorize your session...")

	var password string
	flow := auth.NewFlow(
		auth.Constant(
			phone,
			password,
			auth.CodeAuthenticatorFunc(func(ctx context.Context, sentCode *tg.AuthSentCode) (string, error) {
				code := promptInput(reader, "\nVerification code Telegram just sent to your app: ")
				return code, nil
			}),
		),
		auth.SendCodeOptions{},
	)

	err = client.Run(ctx, func(ctx context.Context) error {
		authClient := client.Auth()
		status, err := authClient.Status(ctx)
		if err != nil {
			return err
		}
		if status.Authorized {
			fmt.Println("Session is already authorized!")
			return nil
		}

		if err := authClient.IfNecessary(ctx, flow); err != nil {
			if errorsIs2FA(err) {
				pwd := promptInput(reader, "Your 2FA password (leave blank if none): ")
				if pwd != "" {
					if _, err := authClient.Password(ctx, pwd); err != nil {
						return fmt.Errorf("2FA password verification failed: %w", err)
					}
				} else {
					return fmt.Errorf("2FA password required: %w", err)
				}
			} else {
				return err
			}
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("telegram authentication error: %w", err)
	}

	sessionString, err := storage.SaveToTelethonString()
	if err != nil {
		return fmt.Errorf("failed to export session string: %w", err)
	}

	fmt.Println()
	fmt.Println("=================================================================")
	fmt.Println("                  LOGIN SUCCESSFUL!                              ")
	fmt.Println("=================================================================")
	fmt.Println()

	fmt.Println("Saving TELEGRAM_API_ID, TELEGRAM_API_HASH, and TELEGRAM_SESSION_STRING to .env...")
	_ = config.SaveEnvKey(".env", "TELEGRAM_API_ID", strconv.Itoa(apiID))
	_ = config.SaveEnvKey(".env", "TELEGRAM_API_HASH", apiHash)
	_ = config.SaveEnvKey(".env", "TELEGRAM_SESSION_STRING", sessionString)

	channel := cfg.Channel
	if channel == "" {
		fmt.Println("\nNow configure your Telegram Music Channel.")
		fmt.Println("You can provide either:")
		fmt.Println("  - The public username (e.g. @my_music_channel)")
		fmt.Println("  - Or the numeric channel ID (e.g. -1001234567890)")
		fmt.Println("  - Or the exact channel name/title")
		channel = promptInput(reader, "Your Telegram Channel handle or ID: ")
		if channel != "" {
			_ = config.SaveEnvKey(".env", "TELEGRAM_CHANNEL", channel)
		}
	}

	if cfg.Port == 0 {
		_ = config.SaveEnvKey(".env", "PORT", "3000")
	}

	fmt.Println("\n[✔] Configuration successfully saved to .env!")
	fmt.Println("\nYour Session String (keep this secret):")
	fmt.Println(sessionString)
	fmt.Println("\nYou can now start the server with:")
	fmt.Println("  ./telegram-music-addon")
	fmt.Println()

	return nil
}

func errorsIs2FA(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "2FA") || strings.Contains(s, "SESSION_PASSWORD_NEEDED")
}
