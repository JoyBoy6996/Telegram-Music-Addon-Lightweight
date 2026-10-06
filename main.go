package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"telegram-music-addon/internal/config"
	"telegram-music-addon/internal/index"
	"telegram-music-addon/internal/login"
	"telegram-music-addon/internal/server"
	"telegram-music-addon/internal/telegram"
)

//go:embed icon.png
var embeddedIcon []byte

const banner = `
=================================================================
             BitChord Telegram Music Addon (Go)
=================================================================
`

func main() {
	if len(os.Args) > 1 && os.Args[1] == "login" {
		if err := login.RunLogin(); err != nil {
			log.Fatalf("Login failed: %v", err)
		}
		return
	}

	fmt.Print(banner)

	cfg := config.LoadConfig()

	// 1. Initialize in-memory index & load persisted cache
	cachePath := "tracks_cache.json"
	lib := index.NewLibrary(cachePath)
	if err := lib.LoadCache(); err == nil {
		log.Printf("[Cache] Loaded %d tracks from %s", lib.Count(), cachePath)
	} else {
		log.Printf("[Cache] No existing cache found (%v), will index from Telegram channel", err)
	}

	// 2. Setup Telegram client if credentials are present
	var tgClient *telegram.Client
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if cfg.ApiID != 0 && cfg.ApiHash != "" && cfg.SessionString != "" && cfg.Channel != "" {
		var err error
		tgClient, err = telegram.NewClient(cfg.ApiID, cfg.ApiHash, cfg.SessionString, cfg.Channel)
		if err != nil {
			log.Printf("[Telegram Error] Failed to create Telegram client: %v", err)
		} else {
			if err := tgClient.Start(ctx); err != nil {
				log.Printf("[Telegram Error] Failed to connect MTProto: %v", err)
			} else {
				// Initial index at startup (async)
				// Index only when cache is empty
if lib.Count() == 0 {
	go func() {
		indexCtx, indexCancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer indexCancel()

		if err := tgClient.IndexChannel(indexCtx, lib, func(indexed int) {
			log.Printf("[Indexer] Progress: %d tracks indexed", indexed)
		}); err != nil {
			log.Printf("[Indexer Error] %v", err)
		} else {
			if err := lib.SaveCache(); err != nil {
				log.Printf("[Cache] Failed to save index: %v", err)
			} else {
				log.Printf("[Cache] Initial indexing completed and saved")
			}
		}
	}()
} else {
	log.Printf("[Indexer] Cache contains %d tracks. Skipping indexing.", lib.Count())
}

				// Periodic 30-minute re-index timer
				
			}
		}
	} else {
		log.Println("[Warning] Telegram credentials or channel not fully configured in environment or .env.")
		log.Println("Run `./telegram-music-addon login` to authenticate with Telegram.")
	}

	// 3. Icon fallback
	iconBytes := embeddedIcon
	if len(iconBytes) == 0 {
		if data, err := os.ReadFile("icon.png"); err == nil {
			iconBytes = data
		}
	}

	// 4. Start HTTP Server
	srv := server.NewServer(cfg, tgClient, lib, iconBytes)
	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      srv.Handler(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // Disable write timeout for range streaming audio
		IdleTimeout:  60 * time.Second,
	}

	secretPath := ""
	if cfg.UrlSecret != "" {
		secretPath = "/" + cfg.UrlSecret
	}
	manifestURL := fmt.Sprintf("http://localhost:%d%s/manifest.json", cfg.Port, secretPath)
	log.Printf("[Server] Starting BitChord addon on :%d", cfg.Port)
	log.Printf("[Server] Manifest URL: %s", manifestURL)

	// Graceful shutdown handling
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("[Server Error] %v", err)
		}
	}()

	<-stop
	log.Println("[Server] Shutting down...")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)
	_ = lib.SaveCache()
	log.Println("[Server] Goodbye!")
}

