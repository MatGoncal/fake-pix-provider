package sign

import (
	"strings"
	"testing"
)

const (
	goldenT      int64 = 1710000000
	goldenSecret       = "dev-webhook-secret"
	goldenBody         = `{"event_id":"evt_golden","provider":"fake_pix","type":"payment.paid"}`
	goldenV1           = "9f53a6865405cf874442533bdad13db074fa4bff9ba49e9123ed18b5a89b712f"
)

func TestSignGoldenVector(t *testing.T) {
	got := Sign([]byte(goldenBody), goldenSecret, goldenT)
	want := "t=1710000000,v1=" + goldenV1
	if got != want {
		t.Fatalf("Sign() = %q, want %q", got, want)
	}
}

func TestParseEitherOrder(t *testing.T) {
	a, ok := Parse("t=123,v1=abc")
	if !ok || a.Timestamp != 123 || a.V1 != "abc" {
		t.Fatalf("t-first: %+v ok=%v", a, ok)
	}
	b, ok := Parse("v1=abc,t=123")
	if !ok || b.Timestamp != 123 || b.V1 != "abc" {
		t.Fatalf("v1-first: %+v ok=%v", b, ok)
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	for _, header := range []string{
		"",
		"sha256=deadbeef",
		"t=nope,v1=abc",
		"t=0,v1=abc",
		"t=-1,v1=abc",
		"t=123,v1=",
		"t=123,v1=not-hex",
	} {
		if _, ok := Parse(header); ok {
			t.Errorf("Parse(%q) unexpectedly succeeded", header)
		}
	}
}

func TestVerifyAcceptsInsideWindow(t *testing.T) {
	const now, tolerance int64 = 1710000100, 300
	header := Sign([]byte(goldenBody), goldenSecret, now-10)
	if st := Verify([]byte(goldenBody), header, goldenSecret, now, tolerance); st != StatusOK {
		t.Fatalf("status = %v, want OK", st)
	}
}

func TestVerifyRejectsExpiredTimestamp(t *testing.T) {
	const now, tolerance int64 = 1710000000, 300
	header := Sign([]byte(goldenBody), goldenSecret, now-tolerance-1)
	if st := Verify([]byte(goldenBody), header, goldenSecret, now, tolerance); st != StatusExpired {
		t.Fatalf("status = %v, want Expired", st)
	}
}

func TestVerifyRejectsFutureTimestamp(t *testing.T) {
	const now, tolerance int64 = 1710000000, 300
	header := Sign([]byte(goldenBody), goldenSecret, now+tolerance+1)
	if st := Verify([]byte(goldenBody), header, goldenSecret, now, tolerance); st != StatusExpired {
		t.Fatalf("status = %v, want Expired", st)
	}
}

func TestVerifyTimingSafeWrongV1(t *testing.T) {
	wrong := "t=1710000000,v1=" + strings.Repeat("ab", 32)
	if st := Verify([]byte(goldenBody), wrong, goldenSecret, goldenT, DefaultToleranceSeconds); st != StatusInvalid {
		t.Fatalf("status = %v, want Invalid", st)
	}
}

func TestVerifyMissingHeader(t *testing.T) {
	if st := Verify([]byte(goldenBody), "", goldenSecret, goldenT, DefaultToleranceSeconds); st != StatusMissing {
		t.Fatalf("status = %v, want Missing", st)
	}
}

func TestVerifyWrongSecret(t *testing.T) {
	header := Sign([]byte(goldenBody), goldenSecret, goldenT)
	if st := Verify([]byte(goldenBody), header, "other-secret", goldenT, DefaultToleranceSeconds); st != StatusInvalid {
		t.Fatalf("status = %v, want Invalid", st)
	}
}
