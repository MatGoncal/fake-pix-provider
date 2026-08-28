package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/MatGoncal/fake-pix-provider/internal/deliver"
	"github.com/MatGoncal/fake-pix-provider/internal/sign"
	"github.com/MatGoncal/fake-pix-provider/internal/store"
)

var uuidRE = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type createChargeRequest struct {
	Amount      int64  `json:"amount"`
	Currency    string `json:"currency"`
	PaymentID   string `json:"payment_id"`
	CallbackURL string `json:"callback_url"`
}

type chargeResponse struct {
	ID                 string `json:"id"`
	Status             string `json:"status"`
	QRCode             string `json:"qr_code"`
	CopyPaste          string `json:"copy_paste"`
	ProviderTxID       string `json:"provider_tx_id"`
	Amount             int64  `json:"amount"`
	Currency           string `json:"currency"`
	PaymentID          string `json:"payment_id"`
	EventID            string `json:"event_id,omitempty"`
	LastDeliveryStatus string `json:"last_delivery_status,omitempty"`
}

type simulateRequest struct {
	Type string `json:"type"`
}

func (s *Server) handleCreateCharge(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req createChargeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid_json"})
		return
	}
	if req.Amount < 1 {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid_amount"})
		return
	}
	if req.Currency != "BRL" {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid_currency"})
		return
	}
	if !uuidRE.MatchString(req.PaymentID) {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid_payment_id"})
		return
	}
	if !validCallbackURL(req.CallbackURL) {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid_callback_url"})
		return
	}

	now := s.clock()
	emv := fakeEMV(req.Currency, req.Amount, now.Unix(), req.PaymentID)
	ch, created, err := s.store.CreateOrGet(store.Charge{
		ID:           newID(),
		Status:       store.StatusPending,
		Amount:       req.Amount,
		Currency:     req.Currency,
		PaymentID:    req.PaymentID,
		CallbackURL:  req.CallbackURL,
		QRCode:       emv,
		CopyPaste:    emv,
		ProviderTxID: "pix_tx_" + newID(),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal"})
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, toChargeResponse(ch))
}

func (s *Server) handleGetCharge(w http.ResponseWriter, r *http.Request) {
	ch, ok, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, errorBody{Error: "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, toChargeResponse(ch))
}

func (s *Server) handleGetChargeByPayment(w http.ResponseWriter, r *http.Request) {
	ch, ok, err := s.store.GetByPaymentID(r.PathValue("payment_id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, errorBody{Error: "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, toChargeResponse(ch))
}

func (s *Server) handleSimulate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req simulateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid_json"})
		return
	}
	if !validEventType(req.Type) {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid_type"})
		return
	}

	eventID := "evt_" + newID()
	ch, claim, err := s.store.ClaimSimulate(r.PathValue("id"), req.Type, eventID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal"})
		return
	}
	switch claim {
	case store.ClaimNotFound:
		writeJSON(w, http.StatusNotFound, errorBody{Error: "not_found"})
		return
	case store.ClaimAlready:
		writeJSON(w, http.StatusOK, map[string]string{
			"event_id": ch.EventID,
			"status":   "already_simulated",
		})
		return
	}

	if s.disableInlineDelivery {
		writeJSON(w, http.StatusAccepted, map[string]string{
			"event_id": ch.EventID,
			"delivery": "pending",
		})
		return
	}

	now := s.clock().UTC()
	body, err := store.MarshalWebhook(ch, now.Format(time.RFC3339))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal"})
		return
	}
	ts := now.Unix()
	headers := make(http.Header, 2)
	headers.Set(sign.HeaderName, sign.Sign(body, s.webhookSecret, ts))
	headers.Set("Content-Type", "application/json")

	callbackURL := ch.CallbackURL
	chargeID := ch.ID
	s.inflight.Add(1)
	go func() {
		defer s.inflight.Done()
		out := s.deliver.PostJSON(context.Background(), callbackURL, body, headers)
		_ = s.store.SetDeliveryStatus(chargeID, deliveryStatus(out))
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{
		"event_id": ch.EventID,
		"delivery": "pending",
	})
}

func validEventType(t string) bool {
	switch t {
	case "payment.paid", "payment.expired", "payment.failed":
		return true
	default:
		return false
	}
}

func validCallbackURL(raw string) bool {
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != ""
}

func fakeEMV(currency string, amount int64, unix int64, paymentID string) string {
	prefix := paymentID
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	return fmt.Sprintf(
		"00020126ACMEPAY.FAKE.PIX.%s.%d.%d.%s",
		strings.ToUpper(currency),
		amount,
		unix,
		strings.ToLower(prefix),
	)
}

func newID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func toChargeResponse(ch store.Charge) chargeResponse {
	return chargeResponse{
		ID:                 ch.ID,
		Status:             string(ch.Status),
		QRCode:             ch.QRCode,
		CopyPaste:          ch.CopyPaste,
		ProviderTxID:       ch.ProviderTxID,
		Amount:             ch.Amount,
		Currency:           ch.Currency,
		PaymentID:          ch.PaymentID,
		EventID:            ch.EventID,
		LastDeliveryStatus: ch.LastDeliveryStatus,
	}
}

func deliveryStatus(out deliver.Outcome) string {
	if out.LastStatus > 0 {
		return strconv.Itoa(out.LastStatus)
	}
	return "network_error"
}

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
