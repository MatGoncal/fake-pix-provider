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

func (s *MemoryStore) Create(c Charge) Charge {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.insertLocked(c)
}

// CreateOrGet returns the existing charge for payment_id when present.
// created is true only when this call stored a new charge.
func (s *MemoryStore) CreateOrGet(c Charge) (Charge, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.PaymentID != "" {
		if id, ok := s.byPaymentID[c.PaymentID]; ok {
			if existing, exists := s.charges[id]; exists {
				return *existing, false
			}
		}
	}
	return s.insertLocked(c), true
}

func (s *MemoryStore) insertLocked(c Charge) Charge {
	cp := c
	s.charges[c.ID] = &cp
	if c.PaymentID != "" {
		s.byPaymentID[c.PaymentID] = c.ID
	}
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

func (s *MemoryStore) GetByPaymentID(paymentID string) (Charge, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byPaymentID[paymentID]
	if !ok {
		return Charge{}, false
	}
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
