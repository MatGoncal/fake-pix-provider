package deliver

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"
)

const DefaultMaxAttempts = 5

// DefaultBackoff is the sleep after attempts 1–4 (injectable in tests).
var DefaultBackoff = []time.Duration{
	50 * time.Millisecond,
	150 * time.Millisecond,
	450 * time.Millisecond,
	1350 * time.Millisecond,
}

// Sleeper waits between retries. Tests inject a no-op to avoid wall-clock delay.
type Sleeper func(ctx context.Context, d time.Duration) error

// Client POSTs a JSON webhook with retry classification:
// 2xx stop; 4xx (except 429) permanent; 429 / 5xx / network retry.
type Client struct {
	HTTP        *http.Client
	Sleep       Sleeper
	Backoff     []time.Duration
	MaxAttempts int
}

// New returns a Client. httpClient may be nil (uses http.DefaultClient).
func New(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		HTTP:        httpClient,
		Sleep:       sleepWithContext,
		Backoff:     append([]time.Duration(nil), DefaultBackoff...),
		MaxAttempts: DefaultMaxAttempts,
	}
}

// Outcome is the result of a (possibly retried) delivery.
type Outcome struct {
	Attempts   int
	LastStatus int
	Permanent  bool
	Err        error
}

func (o Outcome) OK() bool {
	return o.Err == nil && o.LastStatus >= 200 && o.LastStatus <= 299
}

// PostJSON POSTs body to url, reusing the same payload (same event_id) on every attempt.
func (c *Client) PostJSON(ctx context.Context, url string, body []byte, headers http.Header) Outcome {
	max := c.MaxAttempts
	if max <= 0 {
		max = DefaultMaxAttempts
	}
	backoff := c.Backoff
	if len(backoff) == 0 {
		backoff = DefaultBackoff
	}
	sleep := c.Sleep
	if sleep == nil {
		sleep = sleepWithContext
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	var last Outcome
	for attempt := 1; attempt <= max; attempt++ {
		status, err := doOnce(ctx, httpClient, url, body, headers)
		last = Outcome{Attempts: attempt, LastStatus: status, Err: err}

		if err == nil && status >= 200 && status <= 299 {
			return last
		}

		if !shouldRetry(status, err) {
			last.Permanent = err == nil && isPermanentClientError(status)
			return last
		}
		if attempt == max {
			return last
		}

		delay := backoff[len(backoff)-1]
		if attempt-1 < len(backoff) {
			delay = backoff[attempt-1]
		}
		if err := sleep(ctx, delay); err != nil {
			last.Err = err
			return last
		}
	}
	return last
}

func doOnce(ctx context.Context, httpClient *http.Client, url string, body []byte, headers http.Header) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	for key, values := range headers {
		for _, v := range values {
			req.Header.Add(key, v)
		}
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func shouldRetry(status int, err error) bool {
	if err != nil {
		return true
	}
	if status == http.StatusTooManyRequests {
		return true
	}
	return status >= 500
}

func isPermanentClientError(status int) bool {
	return status >= 400 && status <= 499 && status != http.StatusTooManyRequests
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
