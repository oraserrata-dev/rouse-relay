// Rouse Relay Server
//
// A lightweight HTTP server that receives authenticated wake requests
// and sends Wake-on-LAN magic packets on the local network.
//
// Deploy this on an always-on device (NAS, Raspberry Pi, router, etc.)
// on the same network as the devices you want to wake.
//
// Environment variables:
//
//	AUTH_TOKEN  - Shared secret for authentication (recommended)
//	PORT        - Port to listen on (default: 9876)
//	HOST        - Host to bind to (default: 0.0.0.0)
//
// © 2026 Ora Serrata LLC. All rights reserved.

package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// version is stamped at build time via:
//
//	go build -ldflags="-X main.version=1.0.0" .
//
// Defaults to "dev" for ad-hoc builds.
var version = "dev"

var (
	authToken string
	host      string
	port      string
)

// authLimiter throttles repeated failed-auth attempts so a remote
// attacker can't brute-force a weak token over an exposed relay.
// See its definition below.
var authLimiter = newRateLimiter()

func init() {
	authToken = os.Getenv("AUTH_TOKEN")
	host = os.Getenv("HOST")
	if host == "" {
		host = "0.0.0.0"
	}
	port = os.Getenv("PORT")
	if port == "" {
		port = "9876"
	}
}

// parseMac converts "AA:BB:CC:DD:EE:FF" or "AA-BB-CC-DD-EE-FF" to 6 bytes.
func parseMac(macStr string) ([]byte, error) {
	macStr = strings.TrimSpace(strings.ToUpper(macStr))

	sep := ":"
	if strings.Contains(macStr, "-") {
		sep = "-"
	}

	parts := strings.Split(macStr, sep)
	if len(parts) != 6 {
		// %q so a MAC containing newlines or control chars can't forge log
		// lines when this error is later logged.
		return nil, fmt.Errorf("invalid MAC address: %q", macStr)
	}

	mac := make([]byte, 6)
	for i, p := range parts {
		val, err := strconv.ParseUint(p, 16, 8)
		if err != nil {
			return nil, fmt.Errorf("invalid MAC address byte: %q", p)
		}
		mac[i] = byte(val)
	}
	return mac, nil
}

// sendMagicPacket broadcasts a WoL magic packet. If secureOn is non-empty,
// it is parsed as a 6-byte password and appended to the packet.
func sendMagicPacket(macStr, broadcast string, port int, secureOn string) error {
	mac, err := parseMac(macStr)
	if err != nil {
		return err
	}

	// Header: 6 bytes of 0xFF
	packet := make([]byte, 0, 102+6) // 6 + 96 = 102, plus optional 6 for SecureON
	for i := 0; i < 6; i++ {
		packet = append(packet, 0xFF)
	}

	// Body: MAC address repeated 16 times
	for i := 0; i < 16; i++ {
		packet = append(packet, mac...)
	}

	// SecureON password (optional 6-byte append). The error deliberately
	// omits the value so the password never lands in logs or the HTTP
	// response body.
	if secureOn != "" {
		secureOnBytes, err := parseMac(secureOn)
		if err != nil {
			return fmt.Errorf("invalid SecureON password")
		}
		packet = append(packet, secureOnBytes...)
	}

	// net.JoinHostPort handles IPv6 brackets correctly; the older
	// fmt.Sprintf("%s:%d", …) form fails go vet on IPv6 inputs. WoL is
	// fundamentally IPv4 in practice, but doing this the right way costs
	// nothing.
	addr := net.JoinHostPort(broadcast, strconv.Itoa(port))
	dst, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return fmt.Errorf("failed to resolve address: %w", err)
	}

	// Open the socket with SO_BROADCAST set via the platform-specific
	// setBroadcastOpt. The previous code relied on a plain net.Dial to the
	// broadcast address, which fails with EACCES on Linux (the Docker
	// deployment) precisely because SO_BROADCAST wasn't set, so broadcast
	// wakes could silently never leave the host.
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var optErr error
			if err := c.Control(func(fd uintptr) { optErr = setBroadcastOpt(fd) }); err != nil {
				return err
			}
			return optErr
		},
	}
	conn, err := lc.ListenPacket(context.Background(), "udp4", ":0")
	if err != nil {
		return fmt.Errorf("failed to open socket: %w", err)
	}
	defer conn.Close()

	// Send a short burst. UDP is best-effort and a single magic packet can
	// be dropped on a busy or wireless segment; the duplicates are harmless
	// (the NIC wakes on the first it sees) and materially improve
	// reliability. Mirrors the 3-packet burst the apps send. Success means
	// at least one of the burst left the host.
	const burst = 3
	var sentOK bool
	var lastErr error
	for i := 0; i < burst; i++ {
		if _, err := conn.WriteTo(packet, dst); err != nil {
			lastErr = err
		} else {
			sentOK = true
		}
		if i < burst-1 {
			time.Sleep(120 * time.Millisecond)
		}
	}
	if !sentOK {
		return fmt.Errorf("failed to send packet: %w", lastErr)
	}
	// %q so a hostile MAC/broadcast string can't inject forged log lines.
	log.Printf("Magic packet sent to %q via %q:%d", macStr, broadcast, port)
	return nil
}

// ----------------------------------------------------------------------
// Failed-auth rate limiter
//
// Tracks failed-auth timestamps per remote IP. After 10 failures within a
// rolling 60-second window, the IP is blocked from auth attempts (HTTP
// 429) until enough of those failures age out. Only failed attempts are
// recorded; successful auth never counts toward the limit, so legitimate
// clients are unaffected.
//
// This is intentionally simple: in-memory, single-process, no eviction
// beyond the window check. The map can grow if many distinct IPs hit the
// relay, but for the typical Rouse deployment (small set of authorized
// devices) that's a non-issue.
// ----------------------------------------------------------------------

const (
	authFailureWindow = 60 * time.Second
	authFailureLimit  = 10
)

type rateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{attempts: make(map[string][]time.Time)}
}

// allowed reports whether the given IP can attempt auth right now. Also
// trims out-of-window entries so the map self-cleans.
func (l *rateLimiter) allowed(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-authFailureWindow)
	recent := l.attempts[ip][:0]
	for _, t := range l.attempts[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(l.attempts, ip)
	} else {
		l.attempts[ip] = recent
	}
	return len(recent) < authFailureLimit
}

// recordFailure appends a failed-auth timestamp for the given IP.
func (l *rateLimiter) recordFailure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts[ip] = append(l.attempts[ip], time.Now())
}

// sweep drops every fully-aged-out IP. allowed() only cleans an IP when that
// same IP is checked again, so an attacker rotating through many source IPs
// could otherwise leave stale entries that never get revisited. The janitor
// bounds the map regardless of traffic pattern.
func (l *rateLimiter) sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-authFailureWindow)
	for ip, times := range l.attempts {
		recent := times[:0]
		for _, t := range times {
			if t.After(cutoff) {
				recent = append(recent, t)
			}
		}
		if len(recent) == 0 {
			delete(l.attempts, ip)
		} else {
			l.attempts[ip] = recent
		}
	}
}

// startJanitor runs sweep() on a ticker for the life of the process.
func (l *rateLimiter) startJanitor() {
	go func() {
		ticker := time.NewTicker(authFailureWindow)
		defer ticker.Stop()
		for range ticker.C {
			l.sweep()
		}
	}()
}

// clientIP returns the client IP for rate-limiting purposes. We use the
// remote address as-is and don't honor X-Forwarded-For. Clients that
// reach this server through a trusted reverse proxy can be configured to
// add explicit handling later, but trusting forwarded headers by default
// would let any caller spoof their identity to evade rate limiting.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// constantTimeBearerEqual compares the incoming Authorization header
// against the expected "Bearer <token>" string in constant time, so an
// attacker can't use response-time differences to incrementally guess
// the token one byte at a time. Length mismatches return false but the
// comparison still runs to the end so the time taken is constant for a
// given expected-token length.
//
// Mirrors RouseRelayService.swift's constantTimeEqual on the app side.
func constantTimeBearerEqual(header, token string) bool {
	expected := "Bearer " + token
	a := []byte(header)
	b := []byte(expected)
	// subtle.ConstantTimeCompare returns 0 unconditionally for
	// different-length inputs, so we have to length-match ourselves.
	// Build a padded view of `a` matching `b`'s length and fold a
	// length-mismatch flag into the result.
	padded := make([]byte, len(b))
	copy(padded, a)
	lenMatch := subtle.ConstantTimeEq(int32(len(a)), int32(len(b)))
	eq := subtle.ConstantTimeCompare(padded, b)
	return lenMatch&eq == 1
}

// checkAuth validates the Authorization header. Returns true if authorized.
// Failed attempts are recorded against the client IP; after 10 failures in
// a 60-second window the IP is throttled with HTTP 429.
func checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if authToken == "" {
		return true
	}

	ip := clientIP(r)
	if !authLimiter.allowed(ip) {
		// Don't reveal whether the request would have authenticated.
		// Just throttle and tell the caller to slow down.
		w.Header().Set("Retry-After", strconv.Itoa(int(authFailureWindow.Seconds())))
		sendJSON(w, 429, map[string]any{"error": "Too many failed auth attempts"})
		return false
	}

	header := r.Header.Get("Authorization")
	if constantTimeBearerEqual(header, authToken) {
		return true
	}

	authLimiter.recordFailure(ip)
	sendJSON(w, 401, map[string]any{"error": "Unauthorized"})
	return false
}

// sendJSON writes a JSON response with the given status code.
func sendJSON(w http.ResponseWriter, status int, data map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// handleHealth responds with relay status (unauthenticated).
func handleHealth(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, 200, map[string]any{
		"status":        "ok",
		"service":       "rouse-relay",
		"version":       version,
		"auth_required": authToken != "",
	})
}

// handleVerify confirms authentication is valid.
func handleVerify(w http.ResponseWriter, r *http.Request) {
	if !checkAuth(w, r) {
		return
	}
	sendJSON(w, 200, map[string]any{
		"status": "ok",
		"auth":   "valid",
	})
}

// wakeRequest is the JSON body for POST /wake.
type wakeRequest struct {
	MAC       string `json:"mac"`
	Broadcast string `json:"broadcast"`
	Port      int    `json:"port"`
	SecureOn  string `json:"secure_on"`
}

// handleWake sends a magic packet to the specified device.
func handleWake(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, 405, map[string]any{"error": "Method not allowed"})
		return
	}

	if !checkAuth(w, r) {
		return
	}

	// Cap the request body. A wake payload is a few hundred bytes; 16 KB is
	// generous and stops a client from streaming an unbounded body into the
	// JSON decoder.
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)

	var req wakeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"success": false, "error": "Invalid JSON"})
		return
	}

	if req.MAC == "" {
		sendJSON(w, 400, map[string]any{"success": false, "error": "Missing 'mac' field"})
		return
	}
	// Validate the MAC up front so a malformed one is a 400 (client error),
	// not a 500 from the send path. sendMagicPacket re-parses; that's cheap.
	if _, err := parseMac(req.MAC); err != nil {
		sendJSON(w, 400, map[string]any{"success": false, "error": err.Error()})
		return
	}

	if req.Broadcast == "" {
		req.Broadcast = "255.255.255.255"
	}
	// Only accept a literal IP address as the broadcast target. Rejecting
	// hostnames keeps the relay from being coerced into a DNS lookup or
	// resolving an attacker-controlled name to an unintended destination.
	if net.ParseIP(req.Broadcast) == nil {
		sendJSON(w, 400, map[string]any{"success": false, "error": "Invalid broadcast address"})
		return
	}
	if req.Port == 0 {
		req.Port = 9
	}
	if req.Port < 1 || req.Port > 65535 {
		sendJSON(w, 400, map[string]any{"success": false, "error": "Port must be between 1 and 65535"})
		return
	}

	err := sendMagicPacket(req.MAC, req.Broadcast, req.Port, req.SecureOn)

	resp := map[string]any{
		"success":   err == nil,
		"mac":       req.MAC,
		"broadcast": req.Broadcast,
		"port":      req.Port,
	}

	if err != nil {
		log.Printf("Failed to send magic packet: %v", err)
		resp["error"] = err.Error()
		sendJSON(w, 500, resp)
	} else {
		sendJSON(w, 200, resp)
	}
}

// isPublicFacingHost reports whether the configured listen address is
// reachable from anything other than localhost. 0.0.0.0 / :: / empty all
// mean "listen on every interface", which on a typical home router or
// VPS exposes the relay to the LAN at minimum and the public internet
// if there's no firewall in front.
func isPublicFacingHost(h string) bool {
	switch h {
	case "0.0.0.0", "::", "":
		return true
	default:
		return false
	}
}

func main() {
	log.Printf("Rouse Relay v%s starting up", version)

	// Refuse known placeholder tokens outright. These literals appear in
	// the install scripts and the website's copy-paste snippets, so a relay
	// running with one is effectively unauthenticated: the "secret" is
	// public. They're also long enough to dodge the short-token warning
	// below. Normalizing dashes to underscores catches both the
	// YOUR_PASSWORD_HERE and your-password-here spellings.
	switch strings.ToUpper(strings.ReplaceAll(authToken, "-", "_")) {
	case "YOUR_PASSWORD_HERE", "CHANGE_ME", "CHANGEME", "PASSWORD", "TOKEN":
		log.Fatal("AUTH_TOKEN is a placeholder value. Set a real token (use Generate in the Rouse app's relay settings) and restart.")
	}

	// Authentication state warnings. The combination of "no token" + "any
	// interface" is the dangerous one: anybody who can reach this server
	// can wake any device on its network. Loud warning so an operator who
	// accidentally exposes the relay sees it in the logs immediately.
	if authToken == "" {
		if isPublicFacingHost(host) {
			log.Println("================================================================")
			log.Println("CRITICAL: Relay is running unauthenticated AND listening on a")
			log.Println("public-facing address. Any device that can reach this server")
			log.Println("can wake your devices.")
			log.Println("  - Set AUTH_TOKEN to require authentication, OR")
			log.Println("  - Bind HOST=127.0.0.1 to accept only local-machine requests.")
			log.Println("================================================================")
		} else {
			log.Println("Authentication disabled (no AUTH_TOKEN set). Accepting wake")
			log.Printf("requests without a token. Bound to %s only.", host)
		}
	} else {
		// Token configured. Surface a warning if it's obviously weak, since
		// the app's "Generate" button produces 32 random alphanumerics,
		// so anything shorter is almost certainly user-typed and
		// guessable.
		if len(authToken) < 16 {
			log.Println("WARNING: AUTH_TOKEN is shorter than 16 characters. Consider")
			log.Println("  generating a longer token from the Rouse app (Settings →")
			log.Println("  Relay Mode → Generate) to make brute-force impractical.")
		} else {
			log.Println("Authentication enabled (token configured)")
		}
	}

	// Reap stale rate-limiter entries in the background.
	authLimiter.startJanitor()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/verify", handleVerify)
	mux.HandleFunc("/wake", handleWake)

	listenAddr := net.JoinHostPort(host, port)
	log.Printf("Rouse Relay listening on %s", listenAddr)
	log.Printf("  Health:  GET  http://%s/health", listenAddr)
	log.Printf("  Verify:  GET  http://%s/verify  (requires auth)", listenAddr)
	log.Printf("  Wake:    POST http://%s/wake    (requires auth)", listenAddr)

	// Explicit timeouts so a slow or idle client can't tie up a connection
	// indefinitely (Slowloris). The default http.Server has none of these,
	// which on a public-facing relay is a real exhaustion vector.
	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16, // 64 KB
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
