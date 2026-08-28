package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

//go:embed schema.sql
var schemaSQL string

// OutboxEvent is a pending (or in-flight) webhook delivery row.
type OutboxEvent struct {
	ID          int64
	ChargeID    string
	EventID     string
	CallbackURL string
	Payload     []byte
	Attempts    int
}

// PostgresStore persists charges and outbox rows. Demo / Docker only.
type PostgresStore struct {
	db    *sql.DB
	clock func() time.Time
}

func OpenPostgres(dsn string) (*PostgresStore, error) {
	return OpenPostgresWithClock(dsn, time.Now)
}

func OpenPostgresWithClock(dsn string, clock func() time.Time) (*PostgresStore, error) {
	if clock == nil {
		clock = time.Now
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &PostgresStore{db: db, clock: clock}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *PostgresStore) Close() error {
	return s.db.Close()
}

const schemaAdvisoryLock int64 = 0x66616b6570 // "fakep" — cluster-wide serialize CREATE TABLE

func (s *PostgresStore) migrate(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, schemaAdvisoryLock); err != nil {
		return err
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, schemaAdvisoryLock)
	}()

	for _, stmt := range strings.Split(schemaSQL, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) CreateOrGet(c Charge) (Charge, bool, error) {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Charge{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `
INSERT INTO charges (
    id, status, amount, currency, payment_id, callback_url, qr_code, copy_paste, provider_tx_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (payment_id) DO NOTHING
RETURNING id, status, amount, currency, payment_id, callback_url, qr_code, copy_paste,
          provider_tx_id, event_id, event_type, last_delivery_status`,
		c.ID, string(c.Status), c.Amount, c.Currency, c.PaymentID, c.CallbackURL,
		c.QRCode, c.CopyPaste, c.ProviderTxID,
	)
	created, err := scanCharge(row)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return Charge{}, false, err
		}
		return created, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Charge{}, false, err
	}

	existing, err := getByPaymentTx(ctx, tx, c.PaymentID)
	if err != nil {
		return Charge{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Charge{}, false, err
	}
	return existing, false, nil
}

func (s *PostgresStore) Get(id string) (Charge, bool, error) {
	ch, err := scanCharge(s.db.QueryRow(`
SELECT id, status, amount, currency, payment_id, callback_url, qr_code, copy_paste,
       provider_tx_id, event_id, event_type, last_delivery_status
FROM charges WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Charge{}, false, nil
	}
	if err != nil {
		return Charge{}, false, err
	}
	return ch, true, nil
}

func (s *PostgresStore) GetByPaymentID(paymentID string) (Charge, bool, error) {
	ch, err := getByPaymentTx(context.Background(), s.db, paymentID)
	if errors.Is(err, sql.ErrNoRows) {
		return Charge{}, false, nil
	}
	if err != nil {
		return Charge{}, false, err
	}
	return ch, true, nil
}

// ClaimSimulate marks the charge terminal and inserts the outbox row in one TX.
func (s *PostgresStore) ClaimSimulate(id, eventType, eventID string) (Charge, ClaimStatus, error) {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Charge{}, ClaimNotFound, err
	}
	defer func() { _ = tx.Rollback() }()

	ch, err := scanCharge(tx.QueryRowContext(ctx, `
SELECT id, status, amount, currency, payment_id, callback_url, qr_code, copy_paste,
       provider_tx_id, event_id, event_type, last_delivery_status
FROM charges WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Charge{}, ClaimNotFound, nil
	}
	if err != nil {
		return Charge{}, ClaimNotFound, err
	}
	if ch.EventID != "" {
		_ = tx.Rollback()
		return ch, ClaimAlready, nil
	}

	ch.EventID = eventID
	ch.EventType = eventType
	ch.Status = statusFromEvent(eventType)
	ch.LastDeliveryStatus = "pending"

	if _, err := tx.ExecContext(ctx, `
UPDATE charges
SET event_id = $2, event_type = $3, status = $4, last_delivery_status = $5
WHERE id = $1`, ch.ID, ch.EventID, ch.EventType, string(ch.Status), ch.LastDeliveryStatus); err != nil {
		return Charge{}, ClaimNotFound, err
	}

	occurredAt := s.clock().UTC().Format(time.RFC3339)
	payload, err := MarshalWebhook(ch, occurredAt)
	if err != nil {
		return Charge{}, ClaimNotFound, err
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO outbox_events (charge_id, event_id, callback_url, payload, next_attempt_at)
VALUES ($1, $2, $3, $4, $5)`,
		ch.ID, ch.EventID, ch.CallbackURL, payload, s.clock().UTC()); err != nil {
		return Charge{}, ClaimNotFound, err
	}

	if err := tx.Commit(); err != nil {
		return Charge{}, ClaimNotFound, err
	}
	return ch, ClaimOK, nil
}

func (s *PostgresStore) SetDeliveryStatus(id, status string) error {
	_, err := s.db.Exec(`UPDATE charges SET last_delivery_status = $2 WHERE id = $1`, id, status)
	return err
}

func (s *PostgresStore) ClaimPending(ctx context.Context, limit, maxAttempts int, now time.Time) ([]OutboxEvent, error) {
	if limit < 1 {
		limit = 10
	}
	if maxAttempts < 1 {
		maxAttempts = 5
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, charge_id, event_id, callback_url, payload, attempts
FROM outbox_events
WHERE delivered_at IS NULL
  AND attempts < $1
  AND next_attempt_at <= $2
ORDER BY id
LIMIT $3`, maxAttempts, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OutboxEvent
	for rows.Next() {
		var e OutboxEvent
		if err := rows.Scan(&e.ID, &e.ChargeID, &e.EventID, &e.CallbackURL, &e.Payload, &e.Attempts); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *PostgresStore) MarkDelivered(ctx context.Context, e OutboxEvent, httpStatus string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
UPDATE outbox_events
SET delivered_at = $2, last_status = $3, attempts = $4
WHERE id = $1`, e.ID, now.UTC(), httpStatus, e.Attempts+1); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE charges SET last_delivery_status = $2 WHERE id = $1`, e.ChargeID, httpStatus); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) MarkAttempt(ctx context.Context, e OutboxEvent, httpStatus string, nextAttempt time.Time, giveUp bool, maxAttempts int) error {
	attempts := e.Attempts + 1
	if giveUp && attempts < maxAttempts {
		attempts = maxAttempts
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
UPDATE outbox_events
SET attempts = $2, last_status = $3, next_attempt_at = $4
WHERE id = $1`, e.ID, attempts, httpStatus, nextAttempt.UTC()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE charges SET last_delivery_status = $2 WHERE id = $1`, e.ChargeID, httpStatus); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) CountOutbox(ctx context.Context, eventID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox_events WHERE event_id = $1`, eventID).Scan(&n)
	return n, err
}

func (s *PostgresStore) OutboxDelivered(ctx context.Context, eventID string) (bool, error) {
	var delivered sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT delivered_at FROM outbox_events WHERE event_id = $1`, eventID).Scan(&delivered)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return delivered.Valid, nil
}

func (s *PostgresStore) Reset(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `TRUNCATE outbox_events, charges`)
	return err
}

func getByPaymentTx(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, paymentID string) (Charge, error) {
	return scanCharge(q.QueryRowContext(ctx, `
SELECT id, status, amount, currency, payment_id, callback_url, qr_code, copy_paste,
       provider_tx_id, event_id, event_type, last_delivery_status
FROM charges WHERE payment_id = $1`, paymentID))
}

func scanCharge(row interface{ Scan(dest ...any) error }) (Charge, error) {
	var (
		ch                               Charge
		status, currency                 string
		eventID, eventType, lastDelivery sql.NullString
	)
	err := row.Scan(
		&ch.ID, &status, &ch.Amount, &currency, &ch.PaymentID, &ch.CallbackURL,
		&ch.QRCode, &ch.CopyPaste, &ch.ProviderTxID, &eventID, &eventType, &lastDelivery,
	)
	if err != nil {
		return Charge{}, err
	}
	ch.Status = Status(status)
	ch.Currency = strings.TrimSpace(currency)
	if eventID.Valid {
		ch.EventID = eventID.String
	}
	if eventType.Valid {
		ch.EventType = eventType.String
	}
	if lastDelivery.Valid {
		ch.LastDeliveryStatus = lastDelivery.String
	}
	return ch, nil
}
