package store

import (
	"context"
	"os"
	"testing"
)

func openTestPostgres(t *testing.T) *PostgresStore {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	st, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Reset(context.Background()); err != nil {
		t.Fatalf("reset: %v", err)
	}
	return st
}

func sampleCharge(id, paymentID string) Charge {
	return Charge{
		ID:           id,
		Status:       StatusPending,
		Amount:       1500,
		Currency:     "BRL",
		PaymentID:    paymentID,
		CallbackURL:  "http://127.0.0.1:9/cb",
		QRCode:       "qr",
		CopyPaste:    "qr",
		ProviderTxID: "pix_tx_" + id,
	}
}

func TestPostgresRestartGetByPayment(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	st, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	if err := st.Reset(context.Background()); err != nil {
		t.Fatalf("reset: %v", err)
	}
	paymentID := "550e8400-e29b-41d4-a716-446655440000"
	if _, created, err := st.CreateOrGet(sampleCharge("chg_restart", paymentID)); err != nil || !created {
		_ = st.Close()
		t.Fatalf("create = created=%v err=%v", created, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.Close() })

	got, ok, err := st2.GetByPaymentID(paymentID)
	if err != nil || !ok || got.ID != "chg_restart" || got.Amount != 1500 {
		t.Fatalf("after reopen: %+v ok=%v err=%v", got, ok, err)
	}
}

func TestPostgresClaimSimulateOutboxSameTXAndReplay(t *testing.T) {
	st := openTestPostgres(t)
	paymentID := "11111111-1111-4111-8111-111111111111"
	ch, _, err := st.CreateOrGet(sampleCharge("chg_sim", paymentID))
	if err != nil {
		t.Fatal(err)
	}

	got, claim, err := st.ClaimSimulate(ch.ID, "payment.paid", "evt_one")
	if err != nil || claim != ClaimOK || got.Status != StatusPaid {
		t.Fatalf("claim = %+v %v err=%v", got, claim, err)
	}
	n, err := st.CountOutbox(context.Background(), "evt_one")
	if err != nil || n != 1 {
		t.Fatalf("outbox count = %d err=%v, want 1", n, err)
	}

	again, claim, err := st.ClaimSimulate(ch.ID, "payment.expired", "evt_two")
	if err != nil || claim != ClaimAlready || again.EventID != "evt_one" {
		t.Fatalf("replay = %+v %v err=%v", again, claim, err)
	}
	n, err = st.CountOutbox(context.Background(), "evt_two")
	if err != nil || n != 0 {
		t.Fatalf("replay inserted evt_two count=%d", n)
	}
	n, err = st.CountOutbox(context.Background(), "evt_one")
	if err != nil || n != 1 {
		t.Fatalf("original outbox count = %d", n)
	}
}
