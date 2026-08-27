package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/MatGoncal/fake-pix-provider/internal/deliver"
	"github.com/MatGoncal/fake-pix-provider/internal/httpapi"
)

func main() {
	port := getenv("PORT", "8080")
	handler := httpapi.New(httpapi.Config{
		Deliver:       deliver.New(nil),
		WebhookSecret: getenv("WEBHOOK_SECRET", "dev-webhook-secret"),
		APIKey:        os.Getenv("FAKE_PIX_API_KEY"),
	})
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
