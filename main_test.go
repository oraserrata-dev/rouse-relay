// © 2026 Ora Serrata LLC. All rights reserved.

package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Test-only token. Not a real credential.
const testToken = "test-token-0123456789abcdef"

// withAuth sets the package-level token and a fresh rate limiter for one
// test, restoring both afterwards.
func withAuth(t *testing.T, token string) {
	t.Helper()
	oldToken, oldLimiter := authToken, authLimiter
	authToken = token
	authLimiter = newRateLimiter()
	t.Cleanup(func() {
		authToken = oldToken
		authLimiter = oldLimiter
	})
}

func do(t *testing.T, h http.HandlerFunc, method, path, auth string, body []byte) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %q (%v)", rec.Body.String(), err)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	return rec, out
}

// udpSink listens on loopback and returns the port plus a function that
// collects every datagram received until the deadline.
func udpSink(t *testing.T) (int, func(time.Duration) [][]byte) {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { pc.Close() })
	port := pc.LocalAddr().(*net.UDPAddr).Port
	collect := func(wait time.Duration) [][]byte {
		var got [][]byte
		deadline := time.Now().Add(wait)
		buf := make([]byte, 2048)
		for {
			pc.SetReadDeadline(deadline)
			n, _, err := pc.ReadFrom(buf)
			if err != nil {
				return got
			}
			got = append(got, append([]byte(nil), buf[:n]...))
		}
	}
	return port, collect
}

func wakeBody(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ---------------------------------------------------------------- /health

func TestHealth(t *testing.T) {
	for _, tc := range []struct {
		token        string
		authRequired bool
	}{{"", false}, {testToken, true}} {
		withAuth(t, tc.token)
		// /health is unauthenticated: no header, still 200.
		rec, out := do(t, handleHealth, http.MethodGet, "/health", "", nil)
		if rec.Code != 200 {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if out["service"] != "rouse-relay" {
			t.Errorf("service = %v, want rouse-relay (the app checks this before trusting an endpoint)", out["service"])
		}
		if out["status"] != "ok" {
			t.Errorf("status = %v, want ok", out["status"])
		}
		if out["version"] != version {
			t.Errorf("version = %v, want %v", out["version"], version)
		}
		if out["auth_required"] != tc.authRequired {
			t.Errorf("auth_required = %v, want %v", out["auth_required"], tc.authRequired)
		}
		if len(out) != 4 {
			t.Errorf("health has %d keys, want exactly 4: %v", len(out), out)
		}
	}
}

// ---------------------------------------------------------------- auth

func TestVerifyAuth(t *testing.T) {
	withAuth(t, testToken)
	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"correct", "Bearer " + testToken, 200},
		{"missing", "", 401},
		{"wrong token", "Bearer wrong", 401},
		{"no scheme", testToken, 401},
		{"basic scheme", "Basic " + testToken, 401},
		{"token prefix only", "Bearer " + testToken[:len(testToken)-1], 401},
		{"token plus extra", "Bearer " + testToken + "x", 401},
		{"trailing space", "Bearer " + testToken + " ", 401},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			authLimiter = newRateLimiter()
			rec, out := do(t, handleVerify, http.MethodGet, "/verify", tc.header, nil)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if tc.want == 200 && (out["status"] != "ok" || out["auth"] != "valid") {
				t.Errorf("body = %v", out)
			}
			if tc.want == 401 && out["error"] != "Unauthorized" {
				t.Errorf("body = %v", out)
			}
		})
	}
}

func TestVerifyNoTokenConfigured(t *testing.T) {
	withAuth(t, "")
	rec, _ := do(t, handleVerify, http.MethodGet, "/verify", "", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 when AUTH_TOKEN is unset", rec.Code)
	}
}

func TestWakeRequiresAuth(t *testing.T) {
	withAuth(t, testToken)
	body := wakeBody(t, map[string]any{"mac": "AA:BB:CC:DD:EE:FF", "broadcast": "127.0.0.1", "port": 9})
	rec, _ := do(t, handleWake, http.MethodPost, "/wake", "", body)
	if rec.Code != 401 {
		t.Fatalf("no header: status = %d, want 401", rec.Code)
	}
	rec, _ = do(t, handleWake, http.MethodPost, "/wake", "Bearer nope", body)
	if rec.Code != 401 {
		t.Fatalf("bad token: status = %d, want 401", rec.Code)
	}
}

func TestWakeRejectsNonPost(t *testing.T) {
	withAuth(t, testToken)
	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		rec, _ := do(t, handleWake, m, "/wake", "Bearer "+testToken, nil)
		if rec.Code != 405 {
			t.Errorf("%s: status = %d, want 405", m, rec.Code)
		}
	}
}

func TestAuthRateLimit(t *testing.T) {
	withAuth(t, testToken)
	for i := 0; i < authFailureLimit; i++ {
		rec, _ := do(t, handleVerify, http.MethodGet, "/verify", "Bearer wrong", nil)
		if rec.Code != 401 {
			t.Fatalf("attempt %d: status = %d, want 401", i+1, rec.Code)
		}
	}
	rec, out := do(t, handleVerify, http.MethodGet, "/verify", "Bearer wrong", nil)
	if rec.Code != 429 {
		t.Fatalf("after %d failures: status = %d, want 429", authFailureLimit, rec.Code)
	}
	if rec.Header().Get("Retry-After") != "60" {
		t.Errorf("Retry-After = %q, want 60", rec.Header().Get("Retry-After"))
	}
	if out["error"] == nil {
		t.Errorf("429 body has no error: %v", out)
	}
	// While throttled, even the right token is refused (by design: the
	// response must not reveal whether the token was correct).
	rec, _ = do(t, handleVerify, http.MethodGet, "/verify", "Bearer "+testToken, nil)
	if rec.Code != 429 {
		t.Errorf("correct token while throttled: status = %d, want 429", rec.Code)
	}
}

func TestRateLimiterWindowExpires(t *testing.T) {
	l := newRateLimiter()
	old := time.Now().Add(-authFailureWindow - time.Second)
	for i := 0; i < authFailureLimit; i++ {
		l.attempts["192.0.2.9"] = append(l.attempts["192.0.2.9"], old)
	}
	if !l.allowed("192.0.2.9") {
		t.Fatal("aged-out failures still block the IP")
	}
	if _, ok := l.attempts["192.0.2.9"]; ok {
		t.Error("allowed() did not delete the aged-out entry")
	}
	l.attempts["192.0.2.10"] = []time.Time{old}
	l.attempts["192.0.2.11"] = []time.Time{time.Now()}
	l.sweep()
	if _, ok := l.attempts["192.0.2.10"]; ok {
		t.Error("sweep() kept an aged-out IP")
	}
	if _, ok := l.attempts["192.0.2.11"]; !ok {
		t.Error("sweep() dropped a recent IP")
	}
}

func TestConstantTimeBearerEqual(t *testing.T) {
	cases := []struct {
		header, token string
		want          bool
	}{
		{"Bearer abc", "abc", true},
		{"Bearer abd", "abc", false},
		{"Bearer ab", "abc", false},
		{"Bearer abcd", "abc", false},
		{"", "abc", false},
		{"bearer abc", "abc", false},
	}
	for _, tc := range cases {
		if got := constantTimeBearerEqual(tc.header, tc.token); got != tc.want {
			t.Errorf("constantTimeBearerEqual(%q, %q) = %v, want %v", tc.header, tc.token, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------- magic packet

func expectedPacket(mac []byte, secureOn []byte) []byte {
	p := bytes.Repeat([]byte{0xFF}, 6)
	for i := 0; i < 16; i++ {
		p = append(p, mac...)
	}
	return append(p, secureOn...)
}

func TestParseMac(t *testing.T) {
	want := []byte{0xAA, 0xBB, 0xCC, 0x01, 0x02, 0x0F}
	for _, s := range []string{"AA:BB:CC:01:02:0F", "aa:bb:cc:01:02:0f", "AA-BB-CC-01-02-0F", "  aa:bb:cc:01:02:0f\n"} {
		got, err := parseMac(s)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("parseMac(%q) = %x, %v; want %x", s, got, err, want)
		}
	}
	for _, s := range []string{"", "AA:BB:CC:DD:EE", "AA:BB:CC:DD:EE:FF:00", "AABBCCDDEEFF", "GG:BB:CC:DD:EE:FF", "AA:BB:CC:DD:EE:100", "AA:BB:CC:DD:EE:-1", "AA::CC:DD:EE:FF"} {
		if _, err := parseMac(s); err == nil {
			t.Errorf("parseMac(%q) accepted, want error", s)
		}
	}
}

func TestSendMagicPacketBytes(t *testing.T) {
	port, collect := udpSink(t)
	if err := sendMagicPacket("01:23:45:67:89:AB", "127.0.0.1", port, ""); err != nil {
		t.Fatalf("send: %v", err)
	}
	got := collect(500 * time.Millisecond)
	if len(got) != 3 {
		t.Fatalf("received %d packets, want a burst of 3", len(got))
	}
	want := expectedPacket([]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xAB}, nil)
	if len(want) != 102 {
		t.Fatalf("test bug: expected packet is %d bytes", len(want))
	}
	for i, p := range got {
		if !bytes.Equal(p, want) {
			t.Errorf("packet %d = %x\nwant       %x", i, p, want)
		}
	}
}

func TestSendMagicPacketSecureOn(t *testing.T) {
	port, collect := udpSink(t)
	if err := sendMagicPacket("01-23-45-67-89-ab", "127.0.0.1", port, "DE:AD:BE:EF:00:01"); err != nil {
		t.Fatalf("send: %v", err)
	}
	got := collect(500 * time.Millisecond)
	if len(got) == 0 {
		t.Fatal("no packets received")
	}
	want := expectedPacket([]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xAB}, []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01})
	if len(got[0]) != 108 || !bytes.Equal(got[0], want) {
		t.Errorf("packet = %x (%d bytes)\nwant     %x (108 bytes)", got[0], len(got[0]), want)
	}
}

func TestSendMagicPacketBadSecureOnDoesNotLeak(t *testing.T) {
	err := sendMagicPacket("01:23:45:67:89:AB", "127.0.0.1", 9, "hunter2-secret")
	if err == nil {
		t.Fatal("bad SecureON accepted")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("error leaks the SecureON value: %v", err)
	}
}

// ---------------------------------------------------------------- /wake

func TestWakeSuccess(t *testing.T) {
	withAuth(t, testToken)
	port, collect := udpSink(t)
	body := wakeBody(t, map[string]any{"mac": "AA:BB:CC:DD:EE:FF", "broadcast": "127.0.0.1", "port": port})
	rec, out := do(t, handleWake, http.MethodPost, "/wake", "Bearer "+testToken, body)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %v", rec.Code, out)
	}
	if out["success"] != true || out["mac"] != "AA:BB:CC:DD:EE:FF" || out["broadcast"] != "127.0.0.1" || out["port"] != float64(port) {
		t.Errorf("body = %v", out)
	}
	if _, ok := out["error"]; ok {
		t.Errorf("success body carries an error: %v", out)
	}
	got := collect(500 * time.Millisecond)
	if len(got) != 3 || !bytes.Equal(got[0], expectedPacket([]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}, nil)) {
		t.Errorf("relay sent %d packets, first %x", len(got), got)
	}
}

func TestWakeDefaults(t *testing.T) {
	// An absent port defaults to 9. The broadcast default
	// (255.255.255.255) is deliberately not exercised: it would put a real
	// magic packet on the LAN of whoever runs the tests.
	withAuth(t, "")
	_, out := do(t, handleWake, http.MethodPost, "/wake", "", []byte(`{"mac":"AA:BB:CC:DD:EE:FF","broadcast":"127.0.0.1"}`))
	if out["port"] != float64(9) {
		t.Errorf("defaults not applied: %v", out)
	}
}

func TestWakeBadInput(t *testing.T) {
	withAuth(t, testToken)
	cases := []struct {
		name string
		body string
		want int
		err  string
	}{
		{"empty body", ``, 400, "Invalid JSON"},
		{"not json", `mac=AA:BB:CC:DD:EE:FF`, 400, "Invalid JSON"},
		{"truncated", `{"mac":"AA:BB:CC:DD:EE:FF"`, 400, "Invalid JSON"},
		{"array", `["AA:BB:CC:DD:EE:FF"]`, 400, "Invalid JSON"},
		{"port as string", `{"mac":"AA:BB:CC:DD:EE:FF","port":"9"}`, 400, "Invalid JSON"},
		{"port as float", `{"mac":"AA:BB:CC:DD:EE:FF","port":9.5}`, 400, "Invalid JSON"},
		{"mac as number", `{"mac":12345}`, 400, "Invalid JSON"},
		{"json null", `null`, 400, "Missing 'mac' field"},
		{"missing mac", `{"broadcast":"127.0.0.1"}`, 400, "Missing 'mac' field"},
		{"bad mac", `{"mac":"not-a-mac"}`, 400, ""},
		{"hostname broadcast", `{"mac":"AA:BB:CC:DD:EE:FF","broadcast":"example.com"}`, 400, "Invalid broadcast address"},
		{"ipv6 broadcast", `{"mac":"AA:BB:CC:DD:EE:FF","broadcast":"::1"}`, 400, "Invalid broadcast address"},
		{"negative port", `{"mac":"AA:BB:CC:DD:EE:FF","port":-1}`, 400, "Port must be between 1 and 65535"},
		{"port too big", `{"mac":"AA:BB:CC:DD:EE:FF","port":65536}`, 400, "Port must be between 1 and 65535"},
		{"bad secure_on", `{"mac":"AA:BB:CC:DD:EE:FF","broadcast":"127.0.0.1","secure_on":"hunter2-secret"}`, 400, "invalid SecureON password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, out := do(t, handleWake, http.MethodPost, "/wake", "Bearer "+testToken, []byte(tc.body))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body %v", rec.Code, tc.want, out)
			}
			if out["success"] != false {
				t.Errorf("success = %v, want false", out["success"])
			}
			if tc.err != "" && out["error"] != tc.err {
				t.Errorf("error = %q, want %q", out["error"], tc.err)
			}
			if strings.Contains(rec.Body.String(), "hunter2") {
				t.Errorf("response leaks the SecureON value: %s", rec.Body.String())
			}
		})
	}
}

func TestWakeBodySizeCap(t *testing.T) {
	withAuth(t, testToken)
	port, collect := udpSink(t)

	// A valid request padded with an unknown field so the JSON object
	// itself is just over 16 KB.
	pad := strings.Repeat("x", 16*1024)
	over := []byte(`{"mac":"AA:BB:CC:DD:EE:FF","broadcast":"127.0.0.1","port":` + itoa(port) + `,"pad":"` + pad + `"}`)
	rec, out := do(t, handleWake, http.MethodPost, "/wake", "Bearer "+testToken, over)
	if rec.Code != 400 || out["success"] != false {
		t.Fatalf("oversize body: status = %d, body %v; want 400", rec.Code, out)
	}
	if got := collect(300 * time.Millisecond); len(got) != 0 {
		t.Errorf("oversize request still sent %d packets", len(got))
	}

	// Just under the cap is accepted.
	under := []byte(`{"mac":"AA:BB:CC:DD:EE:FF","broadcast":"127.0.0.1","port":` + itoa(port) + `,"pad":"`)
	under = append(under, []byte(strings.Repeat("x", 16*1024-len(under)-2)+`"}`)...)
	if len(under) != 16*1024 {
		t.Fatalf("test bug: under-cap body is %d bytes", len(under))
	}
	rec, out = do(t, handleWake, http.MethodPost, "/wake", "Bearer "+testToken, under)
	if rec.Code != 200 {
		t.Fatalf("16 KB body: status = %d, body %v; want 200", rec.Code, out)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// ---------------------------------------------------------------- misc

func TestIsPublicFacingHost(t *testing.T) {
	for h, want := range map[string]bool{"0.0.0.0": true, "::": true, "": true, "127.0.0.1": false, "192.168.1.5": false} {
		if got := isPublicFacingHost(h); got != want {
			t.Errorf("isPublicFacingHost(%q) = %v, want %v", h, got, want)
		}
	}
}
