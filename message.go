package main

// Message is the universal envelope for all WebSocket signaling traffic.
// The signaling server only ever brokers the WebRTC handshake; live location
// data travels peer-to-peer over DataChannels and never appears here.
// Fields are omitted from JSON when empty to keep payloads lean.
type Message struct {
	Type string `json:"type"`

	// Routing
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	Name string `json:"name,omitempty"`

	// Room the client belongs to (sent in the welcome message so the client
	// can display and share it). Peers only ever see others in the same room.
	Room string `json:"room,omitempty"`

	// Peer list (sent once on join)
	Peers []PeerInfo `json:"peers,omitempty"`

	// WebRTC signaling
	SDP       string `json:"sdp,omitempty"`
	Candidate string `json:"candidate,omitempty"`
}

// PeerInfo is a lightweight peer descriptor sent in the initial peer list.
type PeerInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
