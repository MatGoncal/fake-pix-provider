package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/MatGoncal/fake-pix-provider/internal/deliver"
	"github.com/MatGoncal/fake-pix-provider/internal/httpapi"
	"github.com/MatGoncal/fake-pix-provider/internal/outbox"
	"github.com/MatGoncal/fake-pix-provider/internal/store"
)

func main() {
	port := getenv("PORT", "8080")
	secret := getenv("WEBHOOK_SECRET", "dev-webhook-secret")
	del := deliver.New(nil)
	cfg := httpapi.Config{
		Deliver:       del,
		WebhookSecret: secret,
		APIKey:        os.Getenv("FAKE_PIX_API_KEY"),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		pg, err := store.OpenPostgres(dsn)
		if err != nil {
			log.Fatalf("postgres: %v", err)
		}
		defer pg.Close()
		cfg.Store = pg
		cfg.DisableInlineDelivery = true
		poller := outbox.New(pg, del, secret)
		if v := os.Getenv("OUTBOX_POLL_INTERVAL"); v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				poller.Interval = d
			}
		}
		go poller.Run(ctx)
		log.Printf("fake-pix-provider using Postgres store + outbox poller")
	}

	handler := httpapi.New(cfg)
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("fake-pix-provider listening on %s", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
