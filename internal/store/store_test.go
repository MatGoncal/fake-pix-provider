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
	loaded, ok, err := s.Get("chg_1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || loaded.Amount != 1500 {
		t.Fatalf("get = %+v ok=%v", loaded, ok)
	}
	if _, ok, err := s.Get("missing"); err != nil || ok {
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
	got, ok, err := s.GetByPaymentID(paymentID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.ID != "chg_pay" || got.Amount != 1500 {
		t.Fatalf("get by payment = %+v ok=%v", got, ok)
	}
	if _, ok, err := s.GetByPaymentID("00000000-0000-0000-0000-000000000000"); err != nil || ok {
		t.Fatal("expected missing payment_id")
	}
}

func TestCreateOrGetSamePaymentID(t *testing.T) {
	s := NewMemory()
	paymentID := "550e8400-e29b-41d4-a716-446655440000"
	first, created, err := s.CreateOrGet(Charge{
		ID:        "chg_a",
		Status:    StatusPending,
		Amount:    1500,
		Currency:  "BRL",
		PaymentID: paymentID,
		QRCode:    "qr-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created || first.ID != "chg_a" {
		t.Fatalf("first = %+v created=%v", first, created)
	}

	second, created, err := s.CreateOrGet(Charge{
		ID:        "chg_b",
		Status:    StatusPending,
		Amount:    9900,
		Currency:  "BRL",
		PaymentID: paymentID,
		QRCode:    "qr-b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("second CreateOrGet should replay, not create")
	}
	if second.ID != "chg_a" || second.Amount != 1500 || second.QRCode != "qr-a" {
		t.Fatalf("replay = %+v, want original charge", second)
	}

	got, ok, err := s.GetByPaymentID(paymentID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.ID != "chg_a" {
		t.Fatalf("store has %+v ok=%v, want chg_a", got, ok)
	}
	if _, ok, err := s.Get("chg_b"); err != nil || ok {
		t.Fatal("replay must not insert a second charge")
	}
}

func TestCreateOrGetConcurrentSamePaymentID(t *testing.T) {
	s := NewMemory()
	paymentID := "550e8400-e29b-41d4-a716-446655440000"

	const n = 64
	var created atomic.Int32
	ids := make([]string, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			ch, ok, err := s.CreateOrGet(Charge{
				ID:        fmt.Sprintf("chg_%d", i),
				Status:    StatusPending,
				Amount:    1500,
				Currency:  "BRL",
				PaymentID: paymentID,
			})
			if err != nil {
				t.Errorf("CreateOrGet: %v", err)
				return
			}
			ids[i] = ch.ID
			if ok {
				created.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Fatalf("created = %d, want 1", created.Load())
	}
	winner := ids[0]
	for i, id := range ids {
		if id == "" || id != winner {
			t.Fatalf("ids[%d] = %q, want %q", i, id, winner)
		}
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
			_, st, err := s.ClaimSimulate("chg_race", "payment.paid", fmt.Sprintf("evt_%d", i))
			if err != nil {
				t.Errorf("ClaimSimulate: %v", err)
				return
			}
			if st == ClaimOK {
				won.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if won.Load() != 1 {
		t.Fatalf("winners = %d, want 1", won.Load())
	}

	c, ok, err := s.Get("chg_race")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || c.Status != StatusPaid || c.EventID == "" || c.LastDeliveryStatus != "pending" {
		t.Fatalf("after claim: %+v ok=%v", c, ok)
	}

	again, st, err := s.ClaimSimulate("chg_race", "payment.expired", "evt_other")
	if err != nil {
		t.Fatal(err)
	}
	if st != ClaimAlready || again.EventID != c.EventID {
		t.Fatalf("second claim = %+v status=%v, want Already with same event_id", again, st)
	}
	if again.Status != StatusPaid {
		t.Fatalf("status mutated by loser: %s", again.Status)
	}
}

func TestClaimSimulateNotFound(t *testing.T) {
	s := NewMemory()
	if _, st, err := s.ClaimSimulate("nope", "payment.paid", "evt_x"); err != nil || st != ClaimNotFound {
		t.Fatalf("status = %v err=%v, want NotFound", st, err)
	}
}
