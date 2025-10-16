package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type HostMetrics struct {
	CPUUsage    float64 `json:"cpu_usage"`
	MemoryUsage float64 `json:"memory_usage"`
	DiskUsage   float64 `json:"disk_usage"`
	Hostname    string  `json:"hostname"`
	OS          string  `json:"os"`
	Uptime      uint64  `json:"uptime"`
}

// Heartbeat sent by agent
type Heartbeat struct {
	AgentID     string       `json:"agent_id"`
	HostMetrics *HostMetrics `json:"host_metrics"`
	Timestamp   int64        `json:"timestamp"`
}

// Generic message wrapper
type Message struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

// Agent info stored on server
type Agent struct {
	Conn     *websocket.Conn
	LastSeen time.Time
	Mutex    sync.Mutex
}

var (
	upgrader  = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	agents    = make(map[string]*Agent)
	agentsMux sync.Mutex
)

const heartbeatTimeout = 20 * time.Second

// --- Pretty printing helpers ---
func formatFloat(f float64) string {
	return fmt.Sprintf("%.2f", f)
}

func formatUptime(uptime uint64) string {
	d := time.Duration(uptime) * time.Second
	return d.String()
}

// --- WebSocket handler ---
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
				agentsMux.Lock()
				delete(agents, agentID)
				agentsMux.Unlock()
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

			// Update agent info
			agentsMux.Lock()
			agents[agentID] = &Agent{
				Conn:     conn,
				LastSeen: time.Now(),
			}
			agentsMux.Unlock()

			// Print heartbeat nicely
			metricsStr := ""
			if hb.HostMetrics != nil {
				metricsStr = fmt.Sprintf(
					"CPU: %s%% | RAM: %s%% | Disk: %s%% | Host: %s | OS: %s | Uptime: %s",
					formatFloat(hb.HostMetrics.CPUUsage),
					formatFloat(hb.HostMetrics.MemoryUsage),
					formatFloat(hb.HostMetrics.DiskUsage),
					hb.HostMetrics.Hostname,
					hb.HostMetrics.OS,
					formatUptime(hb.HostMetrics.Uptime),
				)
			}

			log.Printf("✅ Heartbeat from agent %s | %s", agentID, metricsStr)

		default:
			log.Println("⚠️ Unknown message type:", baseMsg.Type)
		}
	}
}

// --- Monitor agents periodically ---
func monitorAgents() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		agentsMux.Lock()
		for id, ag := range agents {
			ag.Mutex.Lock()
			if now.Sub(ag.LastSeen) > heartbeatTimeout {
				log.Printf("⚠️ Agent %s disconnected (offline for > %v)", id, heartbeatTimeout)
				delete(agents, id)
			}
			ag.Mutex.Unlock()
		}
		agentsMux.Unlock()
	}
}

// --- Main ---
func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/ws", wsHandler)

	go monitorAgents()

	log.Printf("🌐 Master server listening on :%s/ws", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
