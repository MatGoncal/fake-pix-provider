package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MatGoncal/fake-pix-provider/internal/deliver"
	"github.com/MatGoncal/fake-pix-provider/internal/sign"
)

const (
	testSecret    = "dev-webhook-secret"
	testPaymentID = "550e8400-e29b-41d4-a716-446655440000"
	fixedUnix     = int64(1710000000)
)

func fixedClock() time.Time {
	return time.Unix(fixedUnix, 0).UTC()
}

func newTestAPI(t *testing.T, apiKey string) (*Server, *httptest.Server) {
	t.Helper()
	del := deliver.New(nil)
	del.Sleep = func(context.Context, time.Duration) error { return nil }
	s := New(Config{
		Deliver:       del,
		WebhookSecret: testSecret,
		APIKey:        apiKey,
		Clock:         fixedClock,
	})
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return s, ts
}

func TestHealth(t *testing.T) {
	_, api := newTestAPI(t, "secret-key")
	resp, err := http.Get(api.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestCreateCharge201FakeEMV(t *testing.T) {
	_, api := newTestAPI(t, "")
	created := createCharge(t, api.URL, "http://127.0.0.1:9/cb", 1500)
	if created.Status != "PENDING" {
		t.Fatalf("status = %q, want PENDING", created.Status)
	}
	if !strings.HasPrefix(created.QRCode, "00020126ACMEPAY.FAKE.PIX.") {
		t.Fatalf("qr_code = %q, want fake EMV prefix", created.QRCode)
	}
	if created.QRCode != created.CopyPaste {
		t.Fatalf("copy_paste != qr_code")
	}
	if created.ProviderTxID == "" || created.ID == "" {
		t.Fatalf("missing ids: %+v", created)
	}
	if created.Amount != 1500 || created.Currency != "BRL" {
		t.Fatalf("money = %d %s", created.Amount, created.Currency)
	}
}

func TestCreateRejectsInvalidAmountAndCurrency(t *testing.T) {
	_, api := newTestAPI(t, "")
	cases := []string{
		`{"amount":0,"currency":"BRL","payment_id":"550e8400-e29b-41d4-a716-446655440000","callback_url":"http://127.0.0.1/cb"}`,
		`{"amount":1500,"currency":"USD","payment_id":"550e8400-e29b-41d4-a716-446655440000","callback_url":"http://127.0.0.1/cb"}`,
		`{"amount":1500,"currency":"BRL","payment_id":"not-a-uuid","callback_url":"http://127.0.0.1/cb"}`,
		`{"amount":1500,"currency":"BRL","payment_id":"550e8400-e29b-41d4-a716-446655440000","callback_url":"ftp://x"}`,
	}
	for _, body := range cases {
		resp, err := http.Post(api.URL+"/v1/charges", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 for %s", resp.StatusCode, body)
		}
	}
}

func TestGetCharge404(t *testing.T) {
	_, api := newTestAPI(t, "")
	resp, err := http.Get(api.URL + "/v1/charges/missing")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestCreateSimulatePaidDeliversSignedIntegerAmount(t *testing.T) {
	var (
		mu      sync.Mutex
		gotBody []byte
		gotSig  string
	)
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read callback: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		mu.Lock()
		gotBody = append([]byte(nil), raw...)
		gotSig = r.Header.Get(sign.HeaderName)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(callback.Close)

	s, api := newTestAPI(t, "")
	created := createCharge(t, api.URL, callback.URL+"/v1/webhooks/payment", 1500)

	sim := postJSON(t, api.URL+"/v1/charges/"+created.ID+"/simulate", `{"type":"payment.paid"}`)
	defer sim.Body.Close()
	if sim.StatusCode != http.StatusAccepted {
		t.Fatalf("simulate status = %d, want 202", sim.StatusCode)
	}
	var simBody struct {
		EventID  string `json:"event_id"`
		Delivery string `json:"delivery"`
	}
	if err := json.NewDecoder(sim.Body).Decode(&simBody); err != nil {
		t.Fatal(err)
	}
	if simBody.EventID == "" || simBody.Delivery != "pending" {
		t.Fatalf("simulate body = %+v", simBody)
	}

	s.Wait()

	mu.Lock()
	raw, header := gotBody, gotSig
	mu.Unlock()
	if len(raw) == 0 {
		t.Fatal("callback received no body")
	}
	if !strings.Contains(header, "t=") || !strings.Contains(header, ",v1=") {
		t.Fatalf("signature header = %q, want t=,v1=", header)
	}
	if st := sign.Verify(raw, header, testSecret, fixedUnix, sign.DefaultToleranceSeconds); st != sign.StatusOK {
		t.Fatalf("Verify status = %v, want OK", st)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var payload map[string]any
	if err := dec.Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["provider"] != "fake_pix" {
		t.Fatalf("provider = %v", payload["provider"])
	}
	if payload["payment_id"] != testPaymentID {
		t.Fatalf("payment_id = %v", payload["payment_id"])
	}
	if payload["event_id"] != simBody.EventID {
		t.Fatalf("event_id = %v, want %s", payload["event_id"], simBody.EventID)
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("data = %T", payload["data"])
	}
	num, ok := data["amount"].(json.Number)
	if !ok {
		t.Fatalf("amount type %T, want json.Number (integer JSON)", data["amount"])
	}
	if strings.Contains(num.String(), ".") {
		t.Fatalf("amount %q is not an integer", num)
	}
	n, err := num.Int64()
	if err != nil || n != 1500 {
		t.Fatalf("amount = %s, want 1500", num)
	}

	got := getCharge(t, api.URL, created.ID)
	if got.Status != "PAID" {
		t.Fatalf("charge status = %q, want PAID", got.Status)
	}
	if got.LastDeliveryStatus != "200" {
		t.Fatalf("last_delivery_status = %q, want 200", got.LastDeliveryStatus)
	}
}

func TestParallelSimulateSingleDelivery(t *testing.T) {
	var hits atomic.Int32
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(callback.Close)

	s, api := newTestAPI(t, "")
	created := createCharge(t, api.URL, callback.URL, 990)

	const n = 2
	var wg sync.WaitGroup
	wg.Add(n)
	codes := make([]int, n)
	bodies := make([][]byte, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			resp, err := http.Post(
				api.URL+"/v1/charges/"+created.ID+"/simulate",
				"application/json",
				strings.NewReader(`{"type":"payment.paid"}`),
			)
			if err != nil {
				t.Errorf("simulate %d: %v", i, err)
				return
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			codes[i] = resp.StatusCode
			bodies[i] = raw
		}(i)
	}
	wg.Wait()
	s.Wait()

	if hits.Load() != 1 {
		t.Fatalf("callback hits = %d, want 1", hits.Load())
	}

	var saw202, saw200 bool
	for i, code := range codes {
		switch code {
		case http.StatusAccepted:
			saw202 = true
			if !bytes.Contains(bodies[i], []byte(`"delivery":"pending"`)) {
				t.Fatalf("202 body = %s", bodies[i])
			}
		case http.StatusOK:
			saw200 = true
			if !bytes.Contains(bodies[i], []byte(`"already_simulated"`)) {
				t.Fatalf("200 body = %s", bodies[i])
			}
		default:
			t.Fatalf("simulate %d status = %d, want 202 or 200", i, code)
		}
	}
	if !saw202 || !saw200 {
		t.Fatalf("codes = %v, want one 202 and one 200", codes)
	}
}

func TestSecondSimulateAlreadySimulatedNoDelivery(t *testing.T) {
	var hits atomic.Int32
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(callback.Close)

	s, api := newTestAPI(t, "")
	created := createCharge(t, api.URL, callback.URL, 100)

	first := postJSON(t, api.URL+"/v1/charges/"+created.ID+"/simulate", `{"type":"payment.paid"}`)
	first.Body.Close()
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first = %d, want 202", first.StatusCode)
	}
	s.Wait()

	second := postJSON(t, api.URL+"/v1/charges/"+created.ID+"/simulate", `{"type":"payment.expired"}`)
	defer second.Body.Close()
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second = %d, want 200", second.StatusCode)
	}
	s.Wait()
	if hits.Load() != 1 {
		t.Fatalf("hits = %d, want 1", hits.Load())
	}
}

func TestAPIKeyRequiredWhenConfigured(t *testing.T) {
	_, api := newTestAPI(t, "fake-pix-demo")
	resp := postJSON(t, api.URL+"/v1/charges", `{"amount":1500,"currency":"BRL","payment_id":"550e8400-e29b-41d4-a716-446655440000","callback_url":"http://127.0.0.1/cb"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no key status = %d, want 401", resp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodPost, api.URL+"/v1/charges", strings.NewReader(
		`{"amount":1500,"currency":"BRL","payment_id":"550e8400-e29b-41d4-a716-446655440000","callback_url":"http://127.0.0.1/cb"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer fake-pix-demo")
	ok, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	ok.Body.Close()
	if ok.StatusCode != http.StatusCreated {
		t.Fatalf("bearer status = %d, want 201", ok.StatusCode)
	}

	req2, err := http.NewRequest(http.MethodPost, api.URL+"/v1/charges", strings.NewReader(
		`{"amount":1500,"currency":"BRL","payment_id":"550e8400-e29b-41d4-a716-446655440000","callback_url":"http://127.0.0.1/cb"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Api-Key", "fake-pix-demo")
	ok2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	ok2.Body.Close()
	if ok2.StatusCode != http.StatusCreated {
		t.Fatalf("x-api-key status = %d, want 201", ok2.StatusCode)
	}
}

func createCharge(t *testing.T, apiURL, callbackURL string, amount int64) chargeResponse {
	t.Helper()
	body := fmt.Sprintf(
		`{"amount":%d,"currency":"BRL","payment_id":%q,"callback_url":%q}`,
		amount, testPaymentID, callbackURL,
	)
	resp := postJSON(t, apiURL+"/v1/charges", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create status = %d body = %s", resp.StatusCode, raw)
	}
	var created chargeResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	return created
}

func getCharge(t *testing.T, apiURL, id string) chargeResponse {
	t.Helper()
	resp, err := http.Get(apiURL + "/v1/charges/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d", resp.StatusCode)
	}
	var got chargeResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	return got
}

func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
