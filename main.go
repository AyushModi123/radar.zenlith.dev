package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

//go:embed frontend
var frontendFS embed.FS

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// Allow all origins — Cloudflare Pages serves the frontend on a different
	// host than this signaling server (Render), so same-origin checks would
	// reject legitimate traffic. The server brokers only opaque WebRTC
	// envelopes scoped to a room, so cross-origin access carries no data risk.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// roomCodePattern restricts explicit room codes to a safe, shareable charset.
var roomCodePattern = regexp.MustCompile(`[^A-Za-z0-9_-]`)

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// clientIP extracts the originating address, honouring the proxy headers set
// by Cloudflare/Render before falling back to the socket peer.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First entry is the original client.
		if i := strings.IndexByte(xff, ','); i >= 0 {
			xff = xff[:i]
		}
		if ip := strings.TrimSpace(xff); ip != "" {
			return ip
		}
	}
	if xrip := strings.TrimSpace(r.Header.Get("X-Real-IP")); xrip != "" {
		return xrip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// roomFor decides which radar a connection joins. Unlike a file-drop tool, a
// location radar must never group people implicitly — sharing live GPS with
// whoever happens to share your office Wi-Fi would be a privacy disaster. So a
// room is joined ONLY via an explicit, shareable `?room=` code. A connection
// without a valid code is placed in its own throwaway room where it can see no
// one, so the radar is strictly opt-in.
func roomFor(r *http.Request) string {
	if code := r.URL.Query().Get("room"); code != "" {
		code = roomCodePattern.ReplaceAllString(code, "")
		if len(code) > 32 {
			code = code[:32]
		}
		if code != "" {
			return "code-" + code
		}
	}
	return "solo-" + generateID()
}

func wsHandler(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !hub.TryReserveIP(ip) {
			http.Error(w, "too many connections", http.StatusTooManyRequests)
			return
		}
		// From here on we own an IP reservation; make sure it is always
		// released, even on the early-return paths below.
		released := false
		release := func() {
			if !released {
				released = true
				hub.releaseIP(ip)
			}
		}
		defer release()

		room := roomFor(r)
		if hub.RoomFull(room) {
			http.Error(w, "radar is full (max 8)", http.StatusServiceUnavailable)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("[!] upgrade error: %v", err)
			return
		}

		client := NewClient(generateID(), randomName(), room, ip, hub, conn)
		hub.Register(client)

		go client.WritePump()
		client.ReadPump() // blocks until the connection closes
		hub.Unregister(client)
		release()
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	hub := NewHub()

	// Serve the embedded frontend from the repo's frontend/ directory so the
	// binary is self-contained and local development works out of the box. In
	// production the frontend is served separately by Cloudflare Pages and
	// this endpoint is just a convenience / health surface.
	staticFS, err := fs.Sub(frontendFS, "frontend")
	if err != nil {
		log.Fatalf("[!] embed error: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", wsHandler(hub))
	mux.HandleFunc("/health", healthHandler)
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	srv := &http.Server{Addr: ":" + port, Handler: mux}

	// Graceful shutdown: stop accepting new connections and give in-flight
	// requests a moment to finish on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("[*] radar signaling server starting on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[!] server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("[*] shutting down…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[!] shutdown error: %v", err)
	}
}
