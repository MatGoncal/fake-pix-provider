package store

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestCreateAndGet(t *testing.T) {
	s := NewMemory()
	got := s.Create(Charge{
		ID:           "chg_1",
		Status:       StatusPending,
		Amount:       1500,
		Currency:     "BRL",
		PaymentID:    "550e8400-e29b-41d4-a716-446655440000",
		ProviderTxID: "pix_tx_1",
	})
	if got.ID != "chg_1" || got.Status != StatusPending {
		t.Fatalf("create = %+v", got)
	}
	loaded, ok := s.Get("chg_1")
	if !ok || loaded.Amount != 1500 {
		t.Fatalf("get = %+v ok=%v", loaded, ok)
	}
	if _, ok := s.Get("missing"); ok {
		t.Fatal("expected missing charge")
	}
}

func TestGetByPaymentID(t *testing.T) {
	s := NewMemory()
	paymentID := "550e8400-e29b-41d4-a716-446655440000"
	s.Create(Charge{
		ID:        "chg_pay",
		Status:    StatusPending,
		Amount:    1500,
		Currency:  "BRL",
		PaymentID: paymentID,
	})
	got, ok := s.GetByPaymentID(paymentID)
	if !ok || got.ID != "chg_pay" || got.Amount != 1500 {
		t.Fatalf("get by payment = %+v ok=%v", got, ok)
	}
	if _, ok := s.GetByPaymentID("00000000-0000-0000-0000-000000000000"); ok {
		t.Fatal("expected missing payment_id")
	}
}

func TestClaimSimulateOnceUnderRace(t *testing.T) {
	s := NewMemory()
	s.Create(Charge{ID: "chg_race", Status: StatusPending, Amount: 100, Currency: "BRL"})

	const n = 64
	var won atomic.Int32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, st := s.ClaimSimulate("chg_race", "payment.paid", fmt.Sprintf("evt_%d", i))
			if st == ClaimOK {
				won.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if won.Load() != 1 {
		t.Fatalf("winners = %d, want 1", won.Load())
	}

	c, ok := s.Get("chg_race")
	if !ok || c.Status != StatusPaid || c.EventID == "" || c.LastDeliveryStatus != "pending" {
		t.Fatalf("after claim: %+v ok=%v", c, ok)
	}

	again, st := s.ClaimSimulate("chg_race", "payment.expired", "evt_other")
	if st != ClaimAlready || again.EventID != c.EventID {
		t.Fatalf("second claim = %+v status=%v, want Already with same event_id", again, st)
	}
	if again.Status != StatusPaid {
		t.Fatalf("status mutated by loser: %s", again.Status)
	}
}

func TestClaimSimulateNotFound(t *testing.T) {
	s := NewMemory()
	if _, st := s.ClaimSimulate("nope", "payment.paid", "evt_x"); st != ClaimNotFound {
		t.Fatalf("status = %v, want NotFound", st)
	}
}
