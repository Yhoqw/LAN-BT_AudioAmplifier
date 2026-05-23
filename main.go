// Written By Yazdan Ali Khan and Azlan Ali Khan, 2026

/*
   Connecting the Go backend to the Python frontend via WebSockets.
   ZeroConf uses mDNS to advertise the host service on the local network.
   Audio is encoded as raw PCM and streamed to connected clients in real time.

   ZeroConf only works for LAN devices on the same network.
*/

package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"

	"github.com/gorilla/websocket"
)

var port int

// upgrader promotes HTTP connections to WebSocket.
// CheckOrigin allows all origins — fine for a local LAN application.
var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func main() {
	flag.IntVar(&port, "port", 0, "Port for UI WebSocket server (0 = auto-assign)")
	flag.Parse()

	if port == 0 {
		var err error
		port, err = getFreePort()
		if err != nil {
			log.Fatal("Failed to get free port:", err)
		}
	}

	// Dedicated mux for the UI connection
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", handleUIWebSocket)

	addr := ":" + strconv.Itoa(port)
	fmt.Printf("Backend listening on http://localhost%s\n", addr)
	fmt.Printf("Python UI should connect to ws://localhost%s/ws\n", addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal("Server error:", err)
	}
}

// getFreePort asks the OS to pick an available TCP port.
func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
