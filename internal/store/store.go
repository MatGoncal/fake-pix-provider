package store

import (
	"encoding/json"
	"sync"
)

type Status string

const (
	StatusPending Status = "PENDING"
	StatusPaid    Status = "PAID"
	StatusExpired Status = "EXPIRED"
	StatusFailed  Status = "FAILED"
)

// ClaimStatus is the outcome of ClaimSimulate.
type ClaimStatus int

const (
	ClaimOK ClaimStatus = iota
	ClaimAlready
	ClaimNotFound
)

// Charge is a synthetic PIX charge. Values are copied in and out of the store.
type Charge struct {
	ID                 string
	Status             Status
	Amount             int64
	Currency           string
	PaymentID          string
	CallbackURL        string
	QRCode             string
	CopyPaste          string
	ProviderTxID       string
	EventID            string
	EventType          string
	LastDeliveryStatus string
}

// Store is the charge persistence surface. MemoryStore is the test default.
type Store interface {
	CreateOrGet(c Charge) (Charge, bool, error)
	Get(id string) (Charge, bool, error)
	GetByPaymentID(paymentID string) (Charge, bool, error)
	ClaimSimulate(id, eventType, eventID string) (Charge, ClaimStatus, error)
	SetDeliveryStatus(id, status string) error
}

// MemoryStore holds charges in process memory. Restart drops everything.
type MemoryStore struct {
	mu          sync.Mutex
	charges     map[string]*Charge
	byPaymentID map[string]string // payment_id → charge id
}

func NewMemory() *MemoryStore {
	return &MemoryStore{
		charges:     make(map[string]*Charge),
		byPaymentID: make(map[string]string),
	}
}

var (
	_ Store = (*MemoryStore)(nil)
	_ Store = (*PostgresStore)(nil)
)

func (s *MemoryStore) Create(c Charge) Charge {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.insertLocked(c)
}

// CreateOrGet returns the existing charge for payment_id when present.
// created is true only when this call stored a new charge.
func (s *MemoryStore) CreateOrGet(c Charge) (Charge, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.PaymentID != "" {
		if id, ok := s.byPaymentID[c.PaymentID]; ok {
			if existing, exists := s.charges[id]; exists {
				return *existing, false, nil
			}
		}
	}
	return s.insertLocked(c), true, nil
}

func (s *MemoryStore) insertLocked(c Charge) Charge {
	cp := c
	s.charges[c.ID] = &cp
	if c.PaymentID != "" {
		s.byPaymentID[c.PaymentID] = c.ID
	}
	return cp
}

func (s *MemoryStore) Get(id string) (Charge, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.charges[id]
	if !ok {
		return Charge{}, false, nil
	}
	return *c, true, nil
}

func (s *MemoryStore) GetByPaymentID(paymentID string) (Charge, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byPaymentID[paymentID]
	if !ok {
		return Charge{}, false, nil
	}
	c, ok := s.charges[id]
	if !ok {
		return Charge{}, false, nil
	}
	return *c, true, nil
}

// ClaimSimulate records the first simulate on a charge. Losers of the race
// get ClaimAlready and the winner's event_id; they must not deliver again.
func (s *MemoryStore) ClaimSimulate(id, eventType, eventID string) (Charge, ClaimStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.charges[id]
	if !ok {
		return Charge{}, ClaimNotFound, nil
	}
	if c.EventID != "" {
		return *c, ClaimAlready, nil
	}
	c.EventID = eventID
	c.EventType = eventType
	c.Status = statusFromEvent(eventType)
	c.LastDeliveryStatus = "pending"
	return *c, ClaimOK, nil
}

func (s *MemoryStore) SetDeliveryStatus(id, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.charges[id]; ok {
		c.LastDeliveryStatus = status
	}
	return nil
}

func statusFromEvent(eventType string) Status {
	switch eventType {
	case "payment.paid":
		return StatusPaid
	case "payment.expired":
		return StatusExpired
	case "payment.failed":
		return StatusFailed
	default:
		return StatusPending
	}
}

type webhookPayload struct {
	EventID    string          `json:"event_id"`
	Provider   string          `json:"provider"`
	Type       string          `json:"type"`
	PaymentID  string          `json:"payment_id"`
	OccurredAt string          `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

type paidData struct {
	ProviderTxID string `json:"provider_tx_id"`
	Amount       int64  `json:"amount"`
	Currency     string `json:"currency"`
}

// MarshalWebhook is the canonical AcmePay webhook body for a claimed charge.
func MarshalWebhook(ch Charge, occurredAt string) ([]byte, error) {
	data := json.RawMessage(`{}`)
	if ch.EventType == "payment.paid" {
		raw, err := json.Marshal(paidData{
			ProviderTxID: ch.ProviderTxID,
			Amount:       ch.Amount,
			Currency:     ch.Currency,
		})
		if err != nil {
			return nil, err
		}
		data = raw
	}
	return json.Marshal(webhookPayload{
		EventID:    ch.EventID,
		Provider:   "fake_pix",
		Type:       ch.EventType,
		PaymentID:  ch.PaymentID,
		OccurredAt: occurredAt,
		Data:       data,
	})
}
