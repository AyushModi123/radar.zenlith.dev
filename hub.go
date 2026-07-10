package main

import (
	"encoding/json"
	"log"
	"sync"
)

const (
	// maxRoomSize caps how many peers may share a single radar. The client
	// builds a full WebRTC mesh (every peer connects to every other), so the
	// number of connections grows quadratically. Eight keeps mesh bandwidth
	// and battery sane and is enforced here as a hard product limit.
	maxRoomSize = 8
	// maxConnsPerIP limits how many simultaneous connections a single client
	// address may hold — a basic guard against one origin exhausting the
	// server. A convoy of phones behind one CGNAT/office NAT shares an IP, so
	// this is generous relative to maxRoomSize.
	maxConnsPerIP = 16
)

type Hub struct {
	mu      sync.RWMutex
	clients map[string]*Client            // id -> client, for direct routing
	rooms   map[string]map[string]*Client // room -> set of clients by id
	ipConns map[string]int                // client ip -> open connection count
}

func NewHub() *Hub {
	return &Hub{
		clients: make(map[string]*Client),
		rooms:   make(map[string]map[string]*Client),
		ipConns: make(map[string]int),
	}
}

// TryReserveIP records a new connection for the given IP. It returns false if
// that IP is already at the connection limit, in which case nothing is
// reserved and the caller should reject the connection.
func (h *Hub) TryReserveIP(ip string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ipConns[ip] >= maxConnsPerIP {
		return false
	}
	h.ipConns[ip]++
	return true
}

// releaseIP is called when a reserved connection goes away (including when a
// reserved connection is rejected before registering).
func (h *Hub) releaseIP(ip string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ipConns[ip] > 0 {
		h.ipConns[ip]--
	}
	if h.ipConns[ip] == 0 {
		delete(h.ipConns, ip)
	}
}

// RoomFull reports whether the named room is already at capacity.
func (h *Hub) RoomFull(room string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms[room]) >= maxRoomSize
}

func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	h.clients[c.id] = c
	if h.rooms[c.room] == nil {
		h.rooms[c.room] = make(map[string]*Client)
	}
	h.rooms[c.room][c.id] = c

	// Snapshot existing peers in the same room while we hold the lock.
	peers := make([]PeerInfo, 0, len(h.rooms[c.room])-1)
	for id, existing := range h.rooms[c.room] {
		if id != c.id {
			peers = append(peers, PeerInfo{ID: id, Name: existing.name})
		}
	}
	h.mu.Unlock()

	// Send the new client their own identity (including the room they landed
	// in, so the UI can display and share it).
	c.send <- mustMarshal(Message{
		Type: "welcome",
		From: c.id,
		Name: c.name,
		Room: c.room,
	})

	c.send <- mustMarshal(Message{
		Type:  "peers",
		Peers: peers,
	})

	// Announce the join to everyone else in the same room only.
	h.broadcast(c.room, c.id, Message{
		Type: "peer-joined",
		From: c.id,
		Name: c.name,
	})

	log.Printf("[+] %s (%s) joined room %q — %d in room", c.name, c.id, c.room, h.roomCount(c.room))
}

func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	delete(h.clients, c.id)
	if room := h.rooms[c.room]; room != nil {
		delete(room, c.id)
		if len(room) == 0 {
			// Room is empty: forget it entirely. No trace of the group's
			// existence survives once the last tab closes.
			delete(h.rooms, c.room)
		}
	}
	h.mu.Unlock()

	h.broadcast(c.room, c.id, Message{
		Type: "peer-left",
		From: c.id,
	})

	log.Printf("[-] %s (%s) left room %q — %d in room", c.name, c.id, c.room, h.roomCount(c.room))
}

// Route forwards a message from one client to a specific target client. The
// target must be in the same room as the sender; cross-room delivery is
// silently dropped so rooms stay isolated.
func (h *Hub) Route(sender *Client, msg Message) {
	h.mu.RLock()
	target, ok := h.clients[msg.To]
	sameRoom := ok && target.room == sender.room
	h.mu.RUnlock()

	if !sameRoom {
		log.Printf("[!] target %s not reachable from %s for message type %s", msg.To, sender.name, msg.Type)
		return
	}

	select {
	case target.send <- mustMarshal(msg):
	default:
		log.Printf("[!] send buffer full for %s, dropping message type %s", target.name, msg.Type)
	}
}

// broadcast sends a message to all clients in a room except the sender.
func (h *Hub) broadcast(room, excludeID string, msg Message) {
	data := mustMarshal(msg)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for id, c := range h.rooms[room] {
		if id != excludeID {
			select {
			case c.send <- data:
			default:
				log.Printf("[!] send buffer full for %s, dropping message", c.name)
			}
		}
	}
}

func (h *Hub) roomCount(room string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms[room])
}

func mustMarshal(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("[!] marshal error: %v", err)
		return []byte("{}")
	}
	return data
}
