package sign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

const (
	// HeaderName is the outbound webhook signature header (AcmePay contract).
	HeaderName = "X-AcmePay-Signature"

	// DefaultToleranceSeconds is the AcmePay HMAC timestamp window.
	DefaultToleranceSeconds = 300
)

// Status is the outcome of Verify.
type Status int

const (
	StatusOK Status = iota
	StatusMissing
	StatusInvalid
	StatusExpired
)

// Sign returns t=<unix>,v1=<hex> using HMAC-SHA256 of "${t}.${raw_body}".
func Sign(rawBody []byte, secret string, timestamp int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.", timestamp)
	mac.Write(rawBody)
	return fmt.Sprintf("t=%d,v1=%s", timestamp, hex.EncodeToString(mac.Sum(nil)))
}

// Parsed is a decoded X-AcmePay-Signature header.
type Parsed struct {
	Timestamp int64
	V1        string
}

// Parse extracts t and v1 from a Stripe-style header. Segments may appear in
// either order. Returns ok=false when t is not a positive integer or v1 is not hex.
func Parse(header string) (Parsed, bool) {
	parts := make(map[string]string, 2)
	for _, segment := range strings.Split(header, ",") {
		eq := strings.IndexByte(segment, '=')
		if eq == -1 {
			continue
		}
		key := strings.TrimSpace(segment[:eq])
		parts[key] = strings.TrimSpace(segment[eq+1:])
	}

	tRaw, okT := parts["t"]
	v1, okV := parts["v1"]
	if !okT || !okV || v1 == "" {
		return Parsed{}, false
	}
	if !isHex(v1) {
		return Parsed{}, false
	}
	t, err := strconv.ParseInt(tRaw, 10, 64)
	if err != nil || t <= 0 {
		return Parsed{}, false
	}
	return Parsed{Timestamp: t, V1: v1}, true
}

// Verify checks HMAC (timing-safe) and the timestamp window.
func Verify(rawBody []byte, header, secret string, nowSeconds, toleranceSeconds int64) Status {
	if header == "" {
		return StatusMissing
	}
	parsed, ok := Parse(header)
	if !ok {
		return StatusInvalid
	}

	expected := Sign(rawBody, secret, parsed.Timestamp)
	expectedParsed, ok := Parse(expected)
	if !ok {
		return StatusInvalid
	}

	gotMAC, errGot := hex.DecodeString(parsed.V1)
	wantMAC, errWant := hex.DecodeString(expectedParsed.V1)
	if errGot != nil || errWant != nil {
		return StatusInvalid
	}
	if !hmac.Equal(gotMAC, wantMAC) {
		return StatusInvalid
	}

	diff := nowSeconds - parsed.Timestamp
	if diff < 0 {
		diff = -diff
	}
	if diff > toleranceSeconds {
		return StatusExpired
	}
	return StatusOK
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
