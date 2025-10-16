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

// ---------------------------
// WebSocket upgrader
// ---------------------------
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// ---------------------------
// Agent structure
// ---------------------------
type Agent struct {
	Conn          *websocket.Conn
	LastHeartbeat int64
	Metrics       interface{} // store last metrics
}

// Global map to track all connected agents
var (
	agents   = make(map[string]*Agent) // agentID -> Agent
	agentsMu sync.Mutex                // mutex to protect the map
)

// ---------------------------
// Message structures
// ---------------------------
type HeartbeatPayload struct {
	AgentID string      `json:"agent_id"`
	Metrics interface{} `json:"host_metrics,omitempty"`
	Timestamp int64     `json:"timestamp"`
}

// ---------------------------
// WebSocket handler
// ---------------------------
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
			log.Println("⚠️ Connection closed for agent:", agentID, err)
			if agentID != "" {
				agentsMu.Lock()
				delete(agents, agentID)
				agentsMu.Unlock()
				log.Println("🛑 Agent removed:", agentID) 
			}
			break
		}

		var hb HeartbeatPayload
		if err := json.Unmarshal(msg, &hb); err != nil {
			log.Println("⚠️ Failed to parse message:", err)
			continue
		}

		agentID = hb.AgentID

		// Register or update agent
		agentsMu.Lock()
		agents[agentID] = &Agent{
			Conn:          conn,
			LastHeartbeat: time.Now().Unix(),
			Metrics:       hb.Metrics,
		}
		agentsMu.Unlock()

		log.Println("✅ Heartbeat received from agent:", agentID)
	}
}

// ---------------------------
// Monitor agents for offline detection
// ---------------------------
func MonitorAgents(timeoutSeconds int64) {
	for {
		time.Sleep(1 * time.Second)
		now := time.Now().Unix()
		agentsMu.Lock()
		for id, agent := range agents {
			if now-agent.LastHeartbeat > timeoutSeconds {
				log.Println("⚠️ Agent offline:", id)
				agent.Conn.Close()
				delete(agents, id)
			}
		}
		agentsMu.Unlock()
	}
}

// ---------------------------
// Main
// ---------------------------
func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Start monitoring agents with 5s timeout
	go MonitorAgents(5)

	http.HandleFunc("/ws", wsHandler)
	log.Printf("🌐 Master server listening on :%s/ws\n", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
