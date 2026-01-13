package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// Simple state
var (
	isHost           = false
	connectedClients = make(map[string]*websocket.Conn)
	discoveredHosts  = make(map[string]string)
)

// Websocket upgrader
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// Message types for UI communication
// Added json tags so Go knows how to map Python/JS keys
type UIMessage struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data"`
}

func main() {
	fmt.Println("Backend Starting...")

	// Start Websocket server
	http.HandleFunc("/ws", handleUIWebSocket)

	// Start mDNS advertisement/discovery in background
	go startDiscovery()

	port := "9090"
	fmt.Printf("Listening on http://localhost:%s\n", port)
	fmt.Printf("Python UI should connect to ws://localhost:%s/ws\n", port)

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal("Server error:", err)
	}
}

func handleUIWebSocket(writer http.ResponseWriter, request *http.Request) {
	conn, err := upgrader.Upgrade(writer, request, nil)
	if err != nil {
		log.Println("UI Websocket upgrade failed:", err)
		return
	}
	defer conn.Close()

	fmt.Println("UI connected")

	// Send initial status
	sendToUI(conn, "status", map[string]interface{}{
		"message": "Backend Ready",
	})

	// Handle UI messages
	for {
		var msg UIMessage
		err := conn.ReadJSON(&msg)
		if err != nil {
			fmt.Println("UI disconnected")
			break
		}
		handleUIMessage(conn, msg)
	}
}

func handleUIMessage(conn *websocket.Conn, msg UIMessage) {
	fmt.Println("Received UI command:", msg.Type)

	switch msg.Type {
	case "become_host":
		become_host(conn)

	case "scan_devices":
		scan_devices(conn)

	case "connect_device":
		if addr, ok := msg.Data["address"].(string); ok {
			connectToHost(conn, addr)
		}

	case "play":
		startPlayback(conn) // Fixed: was StartStreaming

	case "pause":
		pausePlayback(conn) // Fixed: was pauseStreaming

	case "stop":
		stopPlayback(conn) // Fixed: was stopStreaming

	case "volume":
		if vol, ok := msg.Data["level"].(float64); ok {
			setVolume(conn, vol)
		}

	case "select_file":
		if path, ok := msg.Data["path"].(string); ok {
			loadAudioFile(conn, path)
		}
	}
}

// --- Logic Functions ---

func become_host(conn *websocket.Conn) {
	isHost = true
	ip := getLocalIP()

	sendToUI(conn, "host_started", map[string]interface{}{
		"address": ip,
		"port":    9090,
	})

	logMsg(conn, "Now hosting at "+ip)
}

func scan_devices(conn *websocket.Conn) {
	fmt.Println("Scanning for devices...")

	// Mock discovery
	discoveredHosts["John's Laptop"] = "192.168.1.100:9090"
	discoveredHosts["Living Room PC"] = "192.168.1.101:9090"

	for name, address := range discoveredHosts {
		sendToUI(conn, "device_found", map[string]interface{}{
			"name":    name,
			"address": address,
			"type":    "host",
		})
	}

	logMsg(conn, fmt.Sprintf("Discovered %d devices", len(discoveredHosts)))
}

func connectToHost(conn *websocket.Conn, address string) {
	isHost = false
	sendToUI(conn, "connected", map[string]interface{}{
		"address": address,
		"name":    "Remote Host",
	})
	logMsg(conn, "Connected to host: "+address)
}

func startPlayback(conn *websocket.Conn) {
	sendToUI(conn, "playback_started", map[string]interface{}{"position": 0.0})
	go func() {
		for i := 0; i <= 100; i++ {
			time.Sleep(500 * time.Millisecond)
			sendToUI(conn, "progress_update", map[string]interface{}{
				"position": float64(i),
				"total":    100.0,
			})
		}
	}()
}

func pausePlayback(conn *websocket.Conn) {
	sendToUI(conn, "playback_paused", map[string]interface{}{})
}

func stopPlayback(conn *websocket.Conn) {
	sendToUI(conn, "playback_stopped", map[string]interface{}{})
}

func setVolume(conn *websocket.Conn, level float64) {
	sendToUI(conn, "volume_changed", map[string]interface{}{"level": level})
}

func loadAudioFile(conn *websocket.Conn, filepath string) {
	sendToUI(conn, "file_loaded", map[string]interface{}{
		"filename": filepath,
		"duration": 180.0,
	})
}

// --- Helpers ---

func getLocalIP() string {
	addrs, _ := net.InterfaceAddrs()
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return "127.0.0.1"
}

func sendToUI(conn *websocket.Conn, msgType string, data map[string]interface{}) {
	msg := UIMessage{Type: msgType, Data: data}
	conn.WriteJSON(msg)
}

func logMsg(conn *websocket.Conn, message string) {
	sendToUI(conn, "log", map[string]interface{}{"message": message})
}

func startDiscovery() {
	fmt.Println("Discovery service started (mock mode)")
}
