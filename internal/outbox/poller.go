package outbox

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/MatGoncal/fake-pix-provider/internal/deliver"
	"github.com/MatGoncal/fake-pix-provider/internal/sign"
	"github.com/MatGoncal/fake-pix-provider/internal/store"
)

const DefaultMaxAttempts = deliver.DefaultMaxAttempts

// Poller delivers claimed simulate events from Postgres outbox_events.
// One POST per row per tick so a process kill can resume on the next boot.
type Poller struct {
	Store       *store.PostgresStore
	Deliver     *deliver.Client
	Secret      string
	Clock       func() time.Time
	Backoff     []time.Duration
	MaxAttempts int
	Interval    time.Duration

	mu sync.Mutex
}

func New(st *store.PostgresStore, d *deliver.Client, secret string) *Poller {
	if d == nil {
		d = deliver.New(nil)
	}
	c := *d
	c.MaxAttempts = 1
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	return &Poller{
		Store:       st,
		Deliver:     &c,
		Secret:      secret,
		Clock:       time.Now,
		Backoff:     append([]time.Duration(nil), deliver.DefaultBackoff...),
		MaxAttempts: DefaultMaxAttempts,
		Interval:    200 * time.Millisecond,
	}
}

func (p *Poller) Run(ctx context.Context) {
	interval := p.Interval
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	_ = p.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = p.Tick(ctx)
		}
	}
}

func (p *Poller) Tick(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.Store == nil {
		return nil
	}
	clock := p.Clock
	if clock == nil {
		clock = time.Now
	}
	max := p.MaxAttempts
	if max <= 0 {
		max = DefaultMaxAttempts
	}

	events, err := p.Store.ClaimPending(ctx, 20, max, clock())
	if err != nil {
		return err
	}
	for _, e := range events {
		if err := p.deliverOne(ctx, e, clock, max); err != nil {
			return err
		}
	}
	return nil
}

func (p *Poller) deliverOne(ctx context.Context, e store.OutboxEvent, clock func() time.Time, max int) error {
	now := clock()
	headers := make(http.Header, 2)
	headers.Set(sign.HeaderName, sign.Sign(e.Payload, p.Secret, now.Unix()))
	headers.Set("Content-Type", "application/json")

	out := p.Deliver.PostJSON(ctx, e.CallbackURL, e.Payload, headers)
	status := deliveryStatus(out)

	if out.OK() {
		return p.Store.MarkDelivered(ctx, e, status, now)
	}

	giveUp := out.Permanent || e.Attempts+1 >= max
	next := now.Add(backoffDelay(p.Backoff, e.Attempts))
	if giveUp {
		next = now.Add(24 * time.Hour)
	}
	return p.Store.MarkAttempt(ctx, e, status, next, giveUp, max)
}

func backoffDelay(backoff []time.Duration, attemptsSoFar int) time.Duration {
	if len(backoff) == 0 {
		backoff = deliver.DefaultBackoff
	}
	if attemptsSoFar < len(backoff) {
		return backoff[attemptsSoFar]
	}
	return backoff[len(backoff)-1]
}

func deliveryStatus(out deliver.Outcome) string {
	if out.LastStatus > 0 {
		return strconv.Itoa(out.LastStatus)
	}
	return "network_error"
}
