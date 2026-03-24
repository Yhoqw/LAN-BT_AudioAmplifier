package main

import (
	"context"
	"fmt" // Reading Files
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/faiface/beep"
	"github.com/faiface/beep/mp3"
	"github.com/faiface/beep/vorbis"
	"github.com/faiface/beep/wav"

	"github.com/ebitengine/oto/v3"

	"github.com/gorilla/websocket" // Go Websocket library
	"github.com/grandcat/zeroconf" // Zeroconf library
)

// Simple state
var (
	isHost           = false                            // should decide who gets to serve the audio
	connectedClients = make(map[string]*websocket.Conn) // map of connections to our device
	clientsMu        sync.RWMutex
	discoveredHosts  = make(map[string]string)          // In theory sends mDNS and then maps those that give a response
	mdnsServer       *zeroconf.Server                   // Server is to be setup using Zero ZeroConfiguration

	// Audio state
	selectedAudioPath string
	audioSampleRate   beep.SampleRate
	audioTickerStop chan struct{}
	streamStop      chan struct{}
	streamDone      chan struct{}
	audioMu         sync.Mutex
	audioPosition   int // Current position in samples
	audioTotal      int // Estimated total samples in stream
)

// Message types for UI communication
type UIMessage struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

// Connected from Main
func handleUIWebSocket(writer http.ResponseWriter, request *http.Request) {
	// --- Networking Logic ---
	conn, err := upgrader.Upgrade(writer, request, nil) // Conn is going to handle the connection and upgrade the connection to a WebSocket
	if err != nil {
		log.Println("UI Websocket upgrade failed:", err)
		return
	}
	defer conn.Close() // defer calls conn.close before the function is returned (in case of exit)

	// --- UI Logic ---
	fmt.Println("UI connected")

	// Send initial status
	sendToUI(conn, "status", map[string]any{
		"message": "Backend Ready",
	})

	// If we're a host, notify about this client connection
	if isHost {
		clientIP := strings.Split(request.RemoteAddr, ":")[0]
		sendToUI(conn, "client_found", map[string]any{
			"name": "Client", "address": clientIP,
		})
	}

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

/*
handleUIMessage() checks to see what has been selected from the frontend
most of these features should only be configurable for the Host
*/
func handleUIMessage(conn *websocket.Conn, msg UIMessage) {
	fmt.Println("Received UI command:", msg.Type)

	switch msg.Type {
	case "become_host":
		if isHost {
			logMsg(conn, "Already Host!")
			return
		}
		become_host(conn)

	case "scan_devices":
		if isHost {
			logMsg(conn, "Host Cannot Scan for Devices! Please switch to client mode.")
			return
		}
		logMsg(conn, "Starting mDNS device discovery...")
		scan_devices(conn)

	case "connect_device":
		if isHost {
			logMsg(conn, "Host Cannot Connect to Other Devices!")
			return
		}

		if addr, ok := msg.Data["address"].(string); ok {
			connectToHost(conn, addr)
		}

	case "play":
		if isHost {
			startPlayback(conn)
			sendTestPacket(conn)
		} else {
			logMsg(conn, "Only Host can Alter Playback!")
		}
	case "pause":
		if isHost {
			pausePlayback(conn)
		} else {
			logMsg(conn, "Only Host can Alter Playback!")
		}
	case "stop":
		if isHost {
			stopPlayback(conn)
		} else {
			logMsg(conn, "Only Host can Alter Playback")
		}

	case "volume":
		if vol, ok := msg.Data["level"].(float64); ok {
			setVolume(conn, vol)
		}

	case "select_file":
		if isHost {
			if path, ok := msg.Data["path"].(string); ok {
				loadAudioFile(conn, path)
			}
		}
	}
}

// --- Logic Functions ---

func become_host(conn *websocket.Conn) {
	isHost = true
	ip, err := getLocalIP()
	if err != nil {
		log.Printf("failed to get IP: %v using fallback", err)
		ip = "0.0.0.0"
	} else {
		log.Printf("Got local IP: %s", ip)
	}

	port, err := getFreePort()
	if err != nil {
		logMsg(conn, fmt.Sprintf("Could not Get Free Port, fallback on Port 9090"))
		port = 9090 // fallback
	} else {
		log.Printf("Got Free Port: %d", port)
	}

	sendToUI(conn, "host_started", map[string]any{
		"address": ip,
		"port":    port,
	})

	logMsg(conn, fmt.Sprintf("Now hosting at %s:%d", ip, port))

	// Start a WebSocket server on the advertised port for remote client connections
	hostMux := http.NewServeMux()
	hostMux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		remoteConn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println("Remote client upgrade failed:", err)
			return
		}
		defer remoteConn.Close()

		clientAddr := r.RemoteAddr
		log.Printf("Remote client connected: %s", clientAddr)
		clientsMu.Lock()
		connectedClients[clientAddr] = remoteConn
		clientsMu.Unlock()

		sendToUI(conn, "client_connected", map[string]any{
			"name":    "Remote Client",
			"address": clientAddr,
		})

		// Listen for messages from the remote client
		for {
			var msg UIMessage
			if err := remoteConn.ReadJSON(&msg); err != nil {
				log.Printf("Remote client %s disconnected: %v", clientAddr, err)
				clientsMu.Lock()
				delete(connectedClients, clientAddr)
				clientsMu.Unlock()
				return
			}
			handleUIMessage(conn, msg)
		}
	})
	go func() {
		log.Printf("Host WebSocket server listening on :%d", port)
		if err := http.ListenAndServe(fmt.Sprintf(":%d", port), hostMux); err != nil {
			log.Printf("Host server error: %v", err)
		}
	}()

	// starting mDNS with this port (registers the service)
	go startDiscovery(port)
}

func scan_devices(conn *websocket.Conn) {
	fmt.Println("Scanning for devices...")
	mdnsBrowseOnce(conn)

	// Also send any hosts already in discoveredHosts map
	for name, address := range discoveredHosts {
		sendToUI(conn, "device_found", map[string]any{
			"name":    name,
			"address": address,
			"type":    "host",
		})
	}

	if len(discoveredHosts) == 0 {
		logMsg(conn, "No devices found via mDNS. Try 'Direct Connect' with manual IP.")
	} else {
		logMsg(conn, fmt.Sprintf("Found %d device(s) via mDNS discovery", len(discoveredHosts)))
	}
}

func connectToHost(conn *websocket.Conn, address string) {
	isHost = false
	u := url.URL{Scheme: "ws", Host: address, Path: "/ws"}

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		logMsg(conn, "Remote connect failed: "+err.Error())
		return
	}
	clientsMu.Lock()
	connectedClients[address] = c
	clientsMu.Unlock()

	// Notify the host that a client connected

	ip, err := getLocalIP()
	if err != nil {
		log.Printf("failed to get IP: %v using fallback", err)
		ip = "0.0.0.0"
	}
	hostNotification := UIMessage{
		Type: "client_connected",
		Data: map[string]any{
			"name":    "Remote Client",
			"address": ip,
		},
	}
	if err := c.WriteJSON(hostNotification); err != nil {
		logMsg(conn, "Failed to notify host: "+err.Error())
		return
	}

	sendToUI(conn, "connected", map[string]any{
		"address": address,
		"name":    "Remote Host",
	})
	logMsg(conn, "Connected to host: "+address)
	go startClientAudio(c)
}

func startPlayback(conn *websocket.Conn) {
	audioMu.Lock()
	if selectedAudioPath == "" {
		audioMu.Unlock()
		logMsg(conn, "No audio file loaded")
		return
	}
	path := selectedAudioPath

	// Stop any previous stream before starting a new one.
	if streamStop != nil {
		close(streamStop)
	}
	streamStop = make(chan struct{})
	streamDone = make(chan struct{})
	audioPosition = 0
	stopSignal := streamStop
	doneSignal := streamDone
	audioMu.Unlock()
	sendToUI(conn, "playback_started", map[string]any{})
	go streamAudioToClients(conn, path, stopSignal, doneSignal)

	// Start progress ticker
	audioMu.Lock()
	if audioTickerStop != nil {
		close(audioTickerStop)
	}
	audioTickerStop = make(chan struct{})
	audioMu.Unlock()
	go progressTicker(conn)
}

func pausePlayback(conn *websocket.Conn) {
	audioMu.Lock()
	if streamStop != nil {
		close(streamStop)
		streamStop = nil
	}
	audioMu.Unlock()
	sendToUI(conn, "playback_paused", map[string]any{})
}

func stopPlayback(conn *websocket.Conn) {
	audioMu.Lock()
	defer audioMu.Unlock()
	if streamStop != nil {
		close(streamStop)
		streamStop = nil
	}
	if audioTickerStop != nil {
		close(audioTickerStop)
		audioTickerStop = nil
	}
	audioPosition = 0
	sendToUI(conn, "playback_stopped", map[string]any{})
}

func setVolume(conn *websocket.Conn, level float64) {
	// Volume is currently controlled on the playback client side.
	sendToUI(conn, "volume_changed", map[string]any{"level": level})
}

func loadAudioFile(conn *websocket.Conn, filepath string) {

	fmt.Printf("Audio File is being loaded! File Path is: %s", filepath)

	audioMu.Lock()
	defer audioMu.Unlock()

	f, err := os.Open(filepath)
	if err != nil {
		logMsg(conn, "Open failed: "+err.Error())
		return
	}
	defer f.Close()

	ext := strings.ToLower(filepathExt(filepath))
	var (
		stream beep.StreamSeekCloser
		format beep.Format
	)
	switch ext {
	case ".wav":
		stream, format, err = wav.Decode(f)
	case ".mp3":
		stream, format, err = mp3.Decode(f)
	case ".ogg":
		stream, format, err = vorbis.Decode(f)
	default:
		logMsg(conn, "Unsupported format: "+ext)
		return
	}
	if err != nil {
		logMsg(conn, "Decode failed: "+err.Error())
		return
	}
	defer stream.Close()

	audioSampleRate = format.SampleRate
	audioTotal = stream.Len()
	selectedAudioPath = filepath

	durationSec := 0.0
	if format.SampleRate > 0 && audioTotal > 0 {
		durationSec = float64(audioTotal) / float64(format.SampleRate)
	}

	sendToUI(conn, "file_loaded", map[string]any{
		"filename": filepath,
		"duration": durationSec,
	})
	logMsg(conn, fmt.Sprintf("Loaded audio file: %s", filepath))
}

// --- Helpers ---

func getLocalIP() (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, iface := range interfaces {

		// Skip down interfaces and loopback
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ip != nil && !ip.IsLoopback() && ip.To4() != nil {
				return ip.String(), nil
			}
		}
	}

	return "", fmt.Errorf("no valid IP found")
}

func sendToUI(conn *websocket.Conn, msgType string, data map[string]any) {
	msg := UIMessage{Type: msgType, Data: data}
	conn.WriteJSON(msg)
}

func logMsg(conn *websocket.Conn, message string) {
	sendToUI(conn, "log", map[string]any{"message": message})
}

func startDiscovery(port int) {
	name, _ := os.Hostname()
	ip, err := getLocalIP() // getting our own IP
	if err != nil {
		log.Printf("failed to get IP: %v using fallback", err)
		ip = "0.0.0.0"
	}

	// Create unique instance name with hostname, IP, and timestamp to avoid conflicts
	instance := fmt.Sprintf("%s-%s-%d", name, ip, time.Now().Unix()%10000)

	// Create an identifier to be discovered
	server, err := zeroconf.Register(
		instance,
		"_lan-bt-audio._tcp",
		"local.",
		port,
		[]string{"path=/ws"},
		nil,
	)
	if err != nil {
		fmt.Println("mDNS register failed:", err)
		return
	}
	mdnsServer = server
	fmt.Printf("mDNS service advertised as: %s\n", instance)
}

func mdnsBrowseOnce(conn *websocket.Conn) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		logMsg(conn, "mDNS resolver create failed: "+err.Error())
		return
	}

	entries := make(chan *zeroconf.ServiceEntry)

	var wg sync.WaitGroup

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // 5*time.Second is timeout
	defer cancel()

	go func(results <-chan *zeroconf.ServiceEntry) {
		for entry := range results {
			addr := ""
			if len(entry.AddrIPv4) > 0 {
				addr = fmt.Sprintf("%s:%d", entry.AddrIPv4[0].String(), entry.Port)
			} else if len(entry.AddrIPv6) > 0 {
				addr = fmt.Sprintf("[%s]:%d", entry.AddrIPv6[0].String(), entry.Port)
			}
			if addr != "" {
				// Store in discoveredHosts map
				discoveredHosts[entry.Instance] = addr
				sendToUI(conn, "device_found", map[string]any{
					"name":    entry.Instance,
					"address": addr,
					"type":    "host",
				})
			}
		}
	}(entries)

	err = resolver.Browse(ctx, "_lan-bt-audio._tcp", "local.", entries)
	if err != nil {
		logMsg(conn, "mDNS browse failed: "+err.Error())
		return
	}
	<-ctx.Done()

	wg.Wait()
}

func filepathExt(p string) string {
	// robust ext for both / and \ paths
	base := p
	if i := strings.LastIndexAny(base, "/\\"); i >= 0 {
		base = base[i+1:]
	}
	return strings.ToLower(filepath.Ext(base))
}

func sendTestPacket(conn *websocket.Conn) {
	// Send to UI
	testMsg := fmt.Sprintf("TEST_PACKET_%d", time.Now().UnixNano())
	sendToUI(conn, "test_packet", map[string]any{
		"message":   testMsg,
		"timestamp": time.Now().Unix(),
	})

	// Send to all connected remote hosts
	clientsMu.RLock()
	snapshot := make(map[string]*websocket.Conn, len(connectedClients))
	for addr, remoteConn := range connectedClients {
		snapshot[addr] = remoteConn
	}
	clientsMu.RUnlock()

	for addr, remoteConn := range snapshot {
		ip, err := getLocalIP()
		if err != nil {
			log.Printf("Failed to get ip: %v using fallback", err)
			ip = "0.0.0.0"
		}
		msg := UIMessage{
			Type: "test_packet_received",
			Data: map[string]any{
				"from":      ip,
				"message":   testMsg,
				"timestamp": time.Now().Unix(),
			},
		}
		if err := remoteConn.WriteJSON(msg); err != nil {
			logMsg(conn, fmt.Sprintf("Failed to send test packet to %s: %s", addr, err.Error()))
		} else {
			logMsg(conn, fmt.Sprintf("Test packet sent to %s", addr))
		}
	}
}

/*
using pcm to send audio data to other devices (Encoding)
is encoded separately for left and right speakers
*/
func samplesToPCM(buffer [][2]float64, n int) []byte {
	pcm := make([]byte, n*4)

	for i := range n {

		l := int16(buffer[i][0] * 32767)
		r := int16(buffer[i][1] * 32767)

		j := i * 4

		pcm[j] = byte(l)
		pcm[j+1] = byte(l >> 8)

		pcm[j+2] = byte(r)
		pcm[j+3] = byte(r >> 8)
	}
	return pcm
}

// Host streams raw PCM to connected clients as binary WS frames.
func streamAudioToClients(uiConn *websocket.Conn, filePath string, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	f, err := os.Open(filePath)
	if err != nil {
		logMsg(uiConn, "Open failed: "+err.Error())
		return
	}
	defer f.Close()

	ext := strings.ToLower(filepathExt(filePath))
	var (
		stream beep.StreamSeekCloser
		format beep.Format
	)
	switch ext {
	case ".wav":
		stream, format, err = wav.Decode(f)
	case ".mp3":
		stream, format, err = mp3.Decode(f)
	case ".ogg":
		stream, format, err = vorbis.Decode(f)
	default:
		logMsg(uiConn, "Unsupported format: "+ext)
		return
	}
	if err != nil {
		logMsg(uiConn, "Decode failed: "+err.Error())
		return
	}
	defer stream.Close()

	audioMu.Lock()
	audioSampleRate = format.SampleRate
	audioTotal = stream.Len()
	audioPosition = 0
	audioMu.Unlock()

	buffer := make([][2]float64, 1024)

	for {
		select {
		case <-stop:
			return
		default:
		}

		n, ok := stream.Stream(buffer)
		if n == 0 && !ok {
			break
		}

		if n > 0 {
			// Update position for progress tracking
			audioMu.Lock()
			audioPosition += n
			audioMu.Unlock()

			pcm := samplesToPCM(buffer, n)
			clientsMu.RLock()
			snapshot := make(map[string]*websocket.Conn, len(connectedClients))
			for addr, c := range connectedClients {
				snapshot[addr] = c
			}
			clientsMu.RUnlock()

			for addr, c := range snapshot {
				err := c.WriteMessage(websocket.BinaryMessage, pcm)
				if err != nil {
					log.Println("stream error to", addr, err)
					clientsMu.Lock()
					delete(connectedClients, addr)
					clientsMu.Unlock()
				}
			}
		}

		if !ok {
			break
		}

		sleepDuration := time.Duration(float64(n) / float64(format.SampleRate) * float64(time.Second))
		time.Sleep(sleepDuration)
	}

	sendToUI(uiConn, "playback_stopped", map[string]any{})
}

func progressTicker(conn *websocket.Conn) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			audioMu.Lock()
			if audioTotal > 0 {
				sendToUI(conn, "progress_update", map[string]any{
					"position": float64(audioPosition),
					"total":    float64(audioTotal),
				})
			}
			audioMu.Unlock()
		case <-audioTickerStop:
			return
		}
	}
}

func startClientAudio(conn *websocket.Conn) {

	op := &oto.NewContextOptions{}
	op.SampleRate = 44100
	op.ChannelCount = 2
	op.Format = oto.FormatSignedInt16LE

	audioCtx, ready, err := oto.NewContext(op)
	if err != nil {
		panic(err)
	}
	<-ready
	log.Println("oto Ready")

	pr, pw := io.Pipe()

	go func() {
		silence := make([]byte, 44100*4)
		pw.Write(silence)

		for {
			msgType, data, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					log.Println("Stream Finished Cleanly: ", err)
				} else {
					log.Println("Connection Error ", err)
				}
				pw.Close()
				return
			}

			if msgType != websocket.BinaryMessage {
				continue
			}

			_, err = pw.Write(data)
			if err != nil {
				log.Println("Pipe Write Failed!: ", err)
				return
			}
		}
	}()

	player := audioCtx.NewPlayer(pr)
	player.Play()

	log.Println("player started, reading from websocket...")

	for player.IsPlaying() {
		time.Sleep(50 * time.Millisecond) // might not be wanted for this sort of application
	}

	player.Close()
	log.Println("Finished Playing!")


}
