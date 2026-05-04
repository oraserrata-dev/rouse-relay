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
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
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

	addr := fmt.Sprintf("%s:%d", broadcast, port)
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

// checkAuth validates the Authorization header. Returns true if authorized.
func checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if authToken == "" {
		return true
	}
	header := r.Header.Get("Authorization")
	if header == "Bearer "+authToken {
		return true
	}
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

func main() {
	log.Printf("Rouse Relay v%s starting up", version)

	if authToken != "" {
		log.Println("Authentication enabled (token configured)")
	} else {
		log.Println("WARNING: No AUTH_TOKEN set - relay is unauthenticated!")
		log.Println("  Set AUTH_TOKEN environment variable for security.")
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
