package httpapi

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/MatGoncal/fake-pix-provider/internal/deliver"
	"github.com/MatGoncal/fake-pix-provider/internal/store"
)

const maxBodyBytes = 1 << 20

// Server is the fake PIX PSP HTTP surface (stdlib mux, no third-party router).
type Server struct {
	store         *store.MemoryStore
	deliver       *deliver.Client
	webhookSecret string
	apiKey        string
	clock         func() time.Time

	mux      *http.ServeMux
	inflight sync.WaitGroup
}

type Config struct {
	Store         *store.MemoryStore
	Deliver       *deliver.Client
	WebhookSecret string
	APIKey        string
	Clock         func() time.Time
}

func New(cfg Config) *Server {
	if cfg.Store == nil {
		cfg.Store = store.NewMemory()
	}
	if cfg.Deliver == nil {
		cfg.Deliver = deliver.New(nil)
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.WebhookSecret == "" {
		cfg.WebhookSecret = "dev-webhook-secret"
	}

	s := &Server{
		store:         cfg.Store,
		deliver:       cfg.Deliver,
		webhookSecret: cfg.WebhookSecret,
		apiKey:        cfg.APIKey,
		clock:         cfg.Clock,
		mux:           http.NewServeMux(),
	}
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("POST /v1/charges", s.requireAPIKey(s.handleCreateCharge))
	s.mux.HandleFunc("GET /v1/charges/by-payment/{payment_id}", s.requireAPIKey(s.handleGetChargeByPayment))
	s.mux.HandleFunc("GET /v1/charges/{id}", s.requireAPIKey(s.handleGetCharge))
	s.mux.HandleFunc("POST /v1/charges/{id}/simulate", s.requireAPIKey(s.handleSimulate))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// Wait blocks until in-flight webhook deliveries finish (tests).
func (s *Server) Wait() {
	s.inflight.Wait()
}

func (s *Server) requireAPIKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.apiKey == "" {
			next(w, r)
			return
		}
		if !authorized(r, s.apiKey) {
			writeJSON(w, http.StatusUnauthorized, errorBody{Error: "unauthorized"})
			return
		}
		next(w, r)
	}
}

func authorized(r *http.Request, want string) bool {
	if got := strings.TrimSpace(r.Header.Get("X-Api-Key")); got != "" && got == want {
		return true
	}
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if strings.HasPrefix(auth, prefix) && strings.TrimSpace(auth[len(prefix):]) == want {
		return true
	}
	return false
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
