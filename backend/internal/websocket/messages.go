// Package websocket implements the real-time collaboration layer: a Hub of
// Rooms (one per board) with Clients attached, synchronised across server
// nodes through Redis Pub/Sub.
//
// Wire protocol (JSON, one message per WebSocket text frame)
//
//	client -> server
//	  {"t":"op","els":[element...]}          upsert / delete (deleted:true) shapes
//	  {"t":"clear","ts":1730000000000}       clear the board
//	  {"t":"cursor","x":12.5,"y":80}         pointer position in board coordinates
//	  {"t":"ping"}                           application heartbeat
//
//	server -> client
//	  {"t":"init","clientId","you","els","peers","clearedAt","now"}  sent once after joining
//	  {"t":"op","from","els"}  {"t":"clear","from","ts"}  {"t":"cursor","from","x","y"}
//	  {"t":"join","peer"}  {"t":"leave","id"}  {"t":"pong"}
//	  {"t":"error","code","message"}
package websocket

import (
	"encoding/json"
)

// Message type names shared by both directions.
const (
	TypeOp     = "op"
	TypeClear  = "clear"
	TypeCursor = "cursor"
	TypePing   = "ping"
	TypePong   = "pong"
	TypeInit   = "init"
	TypeJoin   = "join"
	TypeLeave  = "leave"
	TypeError  = "error"

	// typePresence is internal: a fresh presence list read from Redis.
	typePresence = "presence"
)

// Limits protecting the server from abusive clients.
const (
	maxMessageBytes  = 512 * 1024
	maxElementsPerOp = 500
)

// Peer describes a participant of a board.
type Peer struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// clientMessage is every message a client may send.
type clientMessage struct {
	Type string            `json:"t"`
	Els  []json.RawMessage `json:"els"`
	TS   int64             `json:"ts"`
	X    float64           `json:"x"`
	Y    float64           `json:"y"`
}

type initMessage struct {
	Type      string            `json:"t"`
	ClientID  string            `json:"clientId"`
	You       Peer              `json:"you"`
	Els       []json.RawMessage `json:"els"`
	Peers     []Peer            `json:"peers"`
	ClearedAt int64             `json:"clearedAt"`
	// Now is the server clock in unix milliseconds. Clients use it to correct
	// their own clock so timestamps stay within the accepted skew.
	Now int64 `json:"now"`
}

type opMessage struct {
	Type string            `json:"t"`
	From string            `json:"from"`
	Els  []json.RawMessage `json:"els"`
}

type clearMessage struct {
	Type string `json:"t"`
	From string `json:"from"`
	TS   int64  `json:"ts"`
}

type cursorMessage struct {
	Type string  `json:"t"`
	From string  `json:"from"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
}

type joinMessage struct {
	Type string `json:"t"`
	Peer Peer   `json:"peer"`
}

type leaveMessage struct {
	Type string `json:"t"`
	ID   string `json:"id"`
}

type errorMessage struct {
	Type    string `json:"t"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type simpleMessage struct {
	Type string `json:"t"`
}

// busMessage is the envelope published on Redis between nodes.
type busMessage struct {
	Node string          `json:"n"`
	Type string          `json:"t"`
	From string          `json:"f,omitempty"`
	Data json.RawMessage `json:"d,omitempty"`
}

type cursorData struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type clearData struct {
	TS int64 `json:"ts"`
}

// frame marshals a message. Marshalling these plain structs cannot fail.
func frame(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic("websocket: marshal frame: " + err.Error())
	}
	return b
}

func mustRaw(v any) json.RawMessage { return json.RawMessage(frame(v)) }
