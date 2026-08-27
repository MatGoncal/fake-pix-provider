package store

import "sync"

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

// Charge is a synthetic PIX charge. Values are copied in and out of MemoryStore.
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

// MemoryStore holds charges in process memory. Restart drops everything.
type MemoryStore struct {
	mu      sync.Mutex
	charges map[string]*Charge
}

func NewMemory() *MemoryStore {
	return &MemoryStore{charges: make(map[string]*Charge)}
}

func (s *MemoryStore) Create(c Charge) Charge {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := c
	s.charges[c.ID] = &cp
	return cp
}

func (s *MemoryStore) Get(id string) (Charge, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.charges[id]
	if !ok {
		return Charge{}, false
	}
	return *c, true
}

// ClaimSimulate records the first simulate on a charge. Losers of the race
// get ClaimAlready and the winner's event_id; they must not deliver again.
func (s *MemoryStore) ClaimSimulate(id, eventType, eventID string) (Charge, ClaimStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.charges[id]
	if !ok {
		return Charge{}, ClaimNotFound
	}
	if c.EventID != "" {
		return *c, ClaimAlready
	}
	c.EventID = eventID
	c.EventType = eventType
	c.Status = statusFromEvent(eventType)
	c.LastDeliveryStatus = "pending"
	return *c, ClaimOK
}

func (s *MemoryStore) SetDeliveryStatus(id, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.charges[id]; ok {
		c.LastDeliveryStatus = status
	}
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
