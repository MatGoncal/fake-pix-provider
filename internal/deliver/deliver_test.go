package deliver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func noSleep(context.Context, time.Duration) error { return nil }

func testClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c := New(srv.Client())
	c.Sleep = noSleep
	return c
}

func TestRetry500Then200SameEventID(t *testing.T) {
	const eventID = "evt_same"
	body := []byte(`{"event_id":"evt_same","provider":"fake_pix"}`)

	var (
		mu     sync.Mutex
		bodies []string
		n      atomic.Int32
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		if n.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	out := testClient(t, srv).PostJSON(context.Background(), srv.URL, body, nil)
	if !out.OK() {
		t.Fatalf("outcome %+v, want OK", out)
	}
	if out.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", out.Attempts)
	}
	if out.LastStatus != http.StatusOK {
		t.Fatalf("status = %d, want 200", out.LastStatus)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("callback hits = %d, want 2", len(bodies))
	}
	if bodies[0] != string(body) || bodies[1] != string(body) {
		t.Fatalf("bodies = %#v, want the same payload twice", bodies)
	}
	for i, raw := range bodies {
		var payload struct {
			EventID string `json:"event_id"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("body %d: %v", i, err)
		}
		if payload.EventID != eventID {
			t.Fatalf("body %d event_id = %q, want %q", i, payload.EventID, eventID)
		}
	}
}

func TestNoRetryOn400(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	out := testClient(t, srv).PostJSON(context.Background(), srv.URL, []byte(`{"event_id":"evt_400"}`), nil)
	if out.OK() {
		t.Fatalf("outcome %+v, want failure", out)
	}
	if out.Attempts != 1 || n.Load() != 1 {
		t.Fatalf("attempts=%d hits=%d, want 1", out.Attempts, n.Load())
	}
	if !out.Permanent {
		t.Fatalf("expected permanent 4xx")
	}
	if out.LastStatus != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", out.LastStatus)
	}
}

func TestRetry429Then200(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	out := testClient(t, srv).PostJSON(context.Background(), srv.URL, []byte(`{"event_id":"evt_429"}`), nil)
	if !out.OK() || out.Attempts != 2 {
		t.Fatalf("outcome %+v, want OK after 2 attempts", out)
	}
}

func TestRetryNetworkUntilMaxAttempts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	url := srv.URL
	srv.Close()

	c := New(&http.Client{Timeout: 50 * time.Millisecond})
	c.Sleep = noSleep
	out := c.PostJSON(context.Background(), url, []byte(`{"event_id":"evt_net"}`), nil)
	if out.OK() {
		t.Fatalf("outcome %+v, want network failure", out)
	}
	if out.Attempts != DefaultMaxAttempts {
		t.Fatalf("attempts = %d, want %d", out.Attempts, DefaultMaxAttempts)
	}
	if out.Permanent {
		t.Fatalf("network errors are not permanent")
	}
	if out.Err == nil {
		t.Fatalf("expected network error")
	}
}

func TestSleepIsInvokedWithInjectedBackoff(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var slept []time.Duration
	c := New(srv.Client())
	c.Backoff = []time.Duration{11 * time.Millisecond, 22 * time.Millisecond}
	c.Sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}

	out := c.PostJSON(context.Background(), srv.URL, []byte(`{"event_id":"evt_backoff"}`), nil)
	if !out.OK() || out.Attempts != 3 {
		t.Fatalf("outcome %+v, want OK after 3 attempts", out)
	}
	if len(slept) != 2 || slept[0] != 11*time.Millisecond || slept[1] != 22*time.Millisecond {
		t.Fatalf("slept = %v, want [11ms 22ms]", slept)
	}
}
