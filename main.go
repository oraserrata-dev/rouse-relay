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
		return nil, fmt.Errorf("invalid MAC address: %s", macStr)
	}

	mac := make([]byte, 6)
	for i, p := range parts {
		val, err := strconv.ParseUint(p, 16, 8)
		if err != nil {
			return nil, fmt.Errorf("invalid MAC address byte: %s", p)
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

	// SecureON password (optional 6-byte append)
	if secureOn != "" {
		secureOnBytes, err := parseMac(secureOn)
		if err != nil {
			return fmt.Errorf("invalid SecureON password: %s", secureOn)
		}
		packet = append(packet, secureOnBytes...)
	}

	// net.JoinHostPort handles IPv6 brackets correctly; the older
	// fmt.Sprintf("%s:%d", …) form fails go vet on IPv6 inputs. WoL is
	// fundamentally IPv4 in practice, but doing this the right way costs
	// nothing.
	addr := net.JoinHostPort(broadcast, strconv.Itoa(port))
	conn, err := net.Dial("udp4", addr)
	if err != nil {
		// If direct dial fails, try broadcast via ListenPacket
		conn2, err2 := net.ListenPacket("udp4", ":0")
		if err2 != nil {
			return fmt.Errorf("failed to open socket: %w", err2)
		}
		defer conn2.Close()

		dst, err2 := net.ResolveUDPAddr("udp4", addr)
		if err2 != nil {
			return fmt.Errorf("failed to resolve address: %w", err2)
		}
		_, err2 = conn2.WriteTo(packet, dst)
		if err2 != nil {
			return fmt.Errorf("failed to send packet: %w", err2)
		}
		log.Printf("Magic packet sent to %s via %s:%d", macStr, broadcast, port)
		return nil
	}
	defer conn.Close()

	_, err = conn.Write(packet)
	if err != nil {
		return fmt.Errorf("failed to send packet: %w", err)
	}
	log.Printf("Magic packet sent to %s via %s:%d", macStr, broadcast, port)
	return nil
}

// ----------------------------------------------------------------------
// Failed-auth rate limiter
//
// Tracks failed-auth timestamps per remote IP. After 10 failures within a
// rolling 60-second window, the IP is blocked from auth attempts (HTTP
// 429) until enough of those failures age out. Only failed attempts are
// recorded — successful auth never counts toward the limit, so legitimate
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

// clientIP returns the client IP for rate-limiting purposes. We use the
// remote address as-is and don't honor X-Forwarded-For — clients that
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

	var req wakeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"success": false, "error": "Invalid JSON"})
		return
	}

	if req.MAC == "" {
		sendJSON(w, 400, map[string]any{"success": false, "error": "Missing 'mac' field"})
		return
	}

	if req.Broadcast == "" {
		req.Broadcast = "255.255.255.255"
	}
	if req.Port == 0 {
		req.Port = 9
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
		// Token configured. Surface a warning if it's obviously weak —
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

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/verify", handleVerify)
	mux.HandleFunc("/wake", handleWake)

	listenAddr := host + ":" + port
	log.Printf("Rouse Relay listening on %s", listenAddr)
	log.Printf("  Health:  GET  http://%s/health", listenAddr)
	log.Printf("  Verify:  GET  http://%s/verify  (requires auth)", listenAddr)
	log.Printf("  Wake:    POST http://%s/wake    (requires auth)", listenAddr)

	if err := http.ListenAndServe(listenAddr, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
