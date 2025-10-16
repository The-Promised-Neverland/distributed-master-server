package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Agent struct {
	Conn         *websocket.Conn
	LastSeen     time.Time
	HeartbeatMux sync.Mutex
}

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	agents   = make(map[string]*Agent) // map of agentID -> agent info
	agentsMu sync.Mutex
)

const heartbeatTimeout = 20 * time.Second // mark agent offline if no heartbeat in this interval

type Heartbeat struct {
	AgentID string `json:"agent_id"`
	// HostMetrics can be added if needed
	Timestamp int64 `json:"timestamp"`
}

type Message struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("❌ Upgrade failed:", err)
		return
	}

	defer conn.Close()

	var agentID string

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Printf("⚠️ Connection closed for agent %s: %v", agentID, err)
			if agentID != "" {
				agentsMu.Lock()
				delete(agents, agentID)
				agentsMu.Unlock()
				log.Printf("⚠️ Agent %s removed from active list", agentID)
			}
			break
		}

		var baseMsg Message
		if err := json.Unmarshal(msg, &baseMsg); err != nil {
			log.Println("⚠️ Failed to parse message:", err)
			continue
		}

		switch baseMsg.Type {
		case "heartbeat":
			payloadBytes, _ := json.Marshal(baseMsg.Payload)
			var hb Heartbeat
			if err := json.Unmarshal(payloadBytes, &hb); err != nil {
				log.Println("⚠️ Failed to parse heartbeat:", err)
				continue
			}
			agentID = hb.AgentID

			agentsMu.Lock()
			agents[agentID] = &Agent{
				Conn:     conn,
				LastSeen: time.Now(),
			}
			agentsMu.Unlock()

			log.Printf("✅ Heartbeat received from agent: %s", agentID)

		default:
			log.Println("⚠️ Unknown message type:", baseMsg.Type)
		}
	}
}

// Periodically check for offline agents
func monitorAgents() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		agentsMu.Lock()
		for id, ag := range agents {
			ag.HeartbeatMux.Lock()
			if now.Sub(ag.LastSeen) > heartbeatTimeout {
				log.Printf("⚠️ Agent offline: %s", id)
				delete(agents, id)
			}
			ag.HeartbeatMux.Unlock()
		}
		agentsMu.Unlock()
	}
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/ws", wsHandler)

	go monitorAgents()

	log.Printf("🌐 Master server listening on :%s/ws\n", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
