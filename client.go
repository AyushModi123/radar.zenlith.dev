package main

import (
	"encoding/json"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 4096 // signaling envelopes only, never location data
	sendBufferSize = 64

	// Mesh setup is bursty: a newcomer joining a room of 7 fires an offer to
	// each existing peer and every pair then trickles a batch of ICE
	// candidates, so a single connection can legitimately emit a hundred-odd
	// messages in the first second. The limit is generous and exists only to
	// stop a client from flooding the room.
	msgsPerWindow = 200
	rateWindow    = time.Second
)

type Client struct {
	id   string
	name string
	room string
	ip   string
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

func NewClient(id, name, room, ip string, hub *Hub, conn *websocket.Conn) *Client {
	return &Client{
		id:   id,
		name: name,
		room: room,
		ip:   ip,
		hub:  hub,
		conn: conn,
		send: make(chan []byte, sendBufferSize),
	}
}

// ReadPump reads incoming messages from the WebSocket and routes them.
func (c *Client) ReadPump() {
	defer func() {
		c.hub.Unregister(c)
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// Simple fixed-window rate limiter.
	windowStart := time.Now()
	var windowCount int

	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[!] unexpected close from %s: %v", c.name, err)
			}
			break
		}

		if now := time.Now(); now.Sub(windowStart) > rateWindow {
			windowStart = now
			windowCount = 0
		}
		windowCount++
		if windowCount > msgsPerWindow {
			log.Printf("[!] rate limit exceeded by %s, dropping message", c.name)
			continue
		}

		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("[!] invalid message from %s: %v", c.name, err)
			continue
		}

		msg.From = c.id // always set from server side, never trust client
		msg.Room = ""   // clients never set the room; it is server state only

		switch msg.Type {
		case "offer", "answer", "ice-candidate":
			if msg.To == "" {
				log.Printf("[!] message type %s missing 'to' field from %s", msg.Type, c.name)
				continue
			}
			c.hub.Route(c, msg)
		default:
			log.Printf("[!] unknown message type '%s' from %s", msg.Type, c.name)
		}
	}
}

// WritePump writes outgoing messages from the send channel to the WebSocket.
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				log.Printf("[!] write error for %s: %v", c.name, err)
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
