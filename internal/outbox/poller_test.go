package outbox

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MatGoncal/fake-pix-provider/internal/deliver"
	"github.com/MatGoncal/fake-pix-provider/internal/store"
)

func TestPollerRetryThenDelivered(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	st, err := store.OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}

	var hits atomic.Int32
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(callback.Close)

	ch := store.Charge{
		ID:           "chg_outbox",
		Status:       store.StatusPending,
		Amount:       1500,
		Currency:     "BRL",
		PaymentID:    "22222222-2222-4222-8222-222222222222",
		CallbackURL:  callback.URL,
		QRCode:       "qr",
		CopyPaste:    "qr",
		ProviderTxID: "pix_tx_outbox",
	}
	if _, _, err := st.CreateOrGet(ch); err != nil {
		t.Fatal(err)
	}
	if _, claim, err := st.ClaimSimulate(ch.ID, "payment.paid", "evt_retry"); err != nil || claim != store.ClaimOK {
		t.Fatalf("claim %v err=%v", claim, err)
	}

	del := deliver.New(callback.Client())
	p := New(st, del, "dev-webhook-secret")
	p.Backoff = []time.Duration{0}

	if err := p.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	delivered, err := st.OutboxDelivered(context.Background(), "evt_retry")
	if err != nil || delivered {
		t.Fatalf("after 5xx: delivered=%v err=%v", delivered, err)
	}

	if err := p.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	delivered, err = st.OutboxDelivered(context.Background(), "evt_retry")
	if err != nil || !delivered {
		t.Fatalf("after 200: delivered=%v err=%v", delivered, err)
	}
	got, ok, err := st.Get(ch.ID)
	if err != nil || !ok || got.LastDeliveryStatus != "200" {
		t.Fatalf("charge after deliver: %+v ok=%v err=%v", got, ok, err)
	}
	if hits.Load() != 2 {
		t.Fatalf("hits = %d, want 2", hits.Load())
	}
}
