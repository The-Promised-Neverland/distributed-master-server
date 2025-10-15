package main

import (
	"log"
	"net/http"
	"os"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("❌ Upgrade failed:", err)
		return
	}
	defer conn.Close()

	log.Println("✅ Agent connected!")

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Println("⚠️ Connection closed:", err)
			break
		}
		log.Println("📩 Received from agent:", string(msg))
	}
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080" // fallback for local dev
	}

	http.HandleFunc("/ws", wsHandler)
	log.Printf("🌐 Master server listening on :%s/ws\n", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
