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

// Message types sent BY AGENT
const (
	AgentMsgHeartbeat = "agent_metrics"
	AgentMsgJobStatus = "agent_job_status"
	AgentMsgUninstall = "agent_uninstall"
)

// Message types sent BY MASTER
const (
	MasterMsgMetricsRequest = "master_metrics_request"
	MasterMsgTaskAssignment = "master_task_assigned"
	MasterMsgRestartAgent   = "master_restart"
	MasterMsgAgentUninstall = "master_uninstall"
)

// Generic message wrapper
type Message struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

// Heartbeat sent by agent
type Metrics struct {
	AgentID     string       `json:"agent_id"`
	HostMetrics *HostMetrics `json:"host_metrics"`
	Timestamp   int64        `json:"timestamp"`
}

// Job status update from agent
type JobStatus struct {
	AgentID string `json:"agent_id"`
	JobID   string `json:"job_id"`
	Status  string `json:"status"` // "started", "completed", "failed"
	Output  string `json:"output,omitempty"`
}

// Uninstallation reason
type UninstallReason struct {
	AgentID   string `json:"agent_id"`
	Reason    string `json:"reason"` // "master_request", "agent_initiated"
	Timestamp int64  `json:"timestamp"`
}

// Agent info stored on server
type Agent struct {
	AgentID     string
	Conn        *websocket.Conn
	LastSeen    time.Time
	Status      string // "online" or "offline"
	HostMetrics *HostMetrics
	Mutex       sync.Mutex
}

var (
	upgrader  = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	agents    = make(map[string]*Agent)
	agentsMux sync.Mutex
)

const heartbeatTimeout = 20 * time.Second

// --- Helpers ---
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
				ag, ok := agents[agentID]
				if ok {
					ag.Mutex.Lock()
					ag.Status = "offline"
					ag.Mutex.Unlock()
					log.Printf("⚠️ Agent %s marked offline", agentID)
				}
				agentsMux.Unlock()
			}
			break
		}

		var baseMsg Message
		if err := json.Unmarshal(msg, &baseMsg); err != nil {
			log.Println("⚠️ Failed to parse message:", err)
			continue
		}

		switch baseMsg.Type {
		case AgentMsgHeartbeat:
			handleHeartbeat(baseMsg.Payload, &agentID, conn)

		case AgentMsgJobStatus:
			handleJobStatus(baseMsg.Payload)

		case AgentMsgUninstall:
			handleUninstallation(baseMsg.Payload)

		default:
			log.Println("⚠️ Unknown message type:", baseMsg.Type)
		}
	}
}

// Handle heartbeat from agent
func handleHeartbeat(payload interface{}, agentID *string, conn *websocket.Conn) {
	payloadBytes, _ := json.Marshal(payload)
	var metrics Metrics
	if err := json.Unmarshal(payloadBytes, &metrics); err != nil {
		log.Println("⚠️ Failed to parse heartbeat:", err)
		return
	}

	*agentID = metrics.AgentID

	agentsMux.Lock()
	ag, ok := agents[metrics.AgentID]
	if !ok {
		ag = &Agent{
			AgentID: metrics.AgentID,
			Conn:    conn,
			Status:  "online",
		}
		agents[metrics.AgentID] = ag
		log.Printf("✅ New agent connected: %s", metrics.AgentID)
	}
	ag.Mutex.Lock()
	ag.Conn = conn
	ag.LastSeen = time.Now()
	ag.Status = "online"
	ag.HostMetrics = metrics.HostMetrics
	ag.Mutex.Unlock()
	agentsMux.Unlock()

	if metrics.HostMetrics != nil {
		metricsStr := fmt.Sprintf(
			"CPU: %s%% | RAM: %s%% | Disk: %s%% | Host: %s | OS: %s | Uptime: %s",
			formatFloat(metrics.HostMetrics.CPUUsage),
			formatFloat(metrics.HostMetrics.MemoryUsage),
			formatFloat(metrics.HostMetrics.DiskUsage),
			metrics.HostMetrics.Hostname,
			metrics.HostMetrics.OS,
			formatUptime(metrics.HostMetrics.Uptime),
		)
		log.Printf("✅ Heartbeat from agent %s | %s", metrics.AgentID, metricsStr)
	}
}

// Handle job status update from agent
func handleJobStatus(payload interface{}) {
	payloadBytes, _ := json.Marshal(payload)
	var jobStatus JobStatus
	if err := json.Unmarshal(payloadBytes, &jobStatus); err != nil {
		log.Println("⚠️ Failed to parse job status:", err)
		return
	}

	log.Printf("📊 Job status from agent %s | JobID: %s | Status: %s | Output: %s",
		jobStatus.AgentID, jobStatus.JobID, jobStatus.Status, jobStatus.Output)
}

// Handle uninstallation from agent
func handleUninstallation(payload interface{}) {
	payloadBytes, _ := json.Marshal(payload)
	var uninstall UninstallReason
	if err := json.Unmarshal(payloadBytes, &uninstall); err != nil {
		log.Println("⚠️ Failed to parse uninstall message:", err)
		return
	}

	agentsMux.Lock()
	ag, ok := agents[uninstall.AgentID]
	if ok {
		ag.Mutex.Lock()
		ag.Status = "offline"
		ag.Mutex.Unlock()
		log.Printf("💀 Agent %s uninstalled (reason: %s) at %d",
			uninstall.AgentID, uninstall.Reason, uninstall.Timestamp)
	} else {
		log.Printf("💀 Agent %s uninstalled but was not in registry (reason: %s)",
			uninstall.AgentID, uninstall.Reason)
	}
	agentsMux.Unlock()
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
			if ag.Status == "online" && now.Sub(ag.LastSeen) > heartbeatTimeout {
				ag.Status = "offline"
				log.Printf("⚠️ Agent %s marked offline due to heartbeat timeout", id)
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