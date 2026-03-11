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
	"github.com/faiface/beep/effects"
	"github.com/faiface/beep/mp3"
	"github.com/faiface/beep/speaker"
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
	discoveredHosts  = make(map[string]string)          // In theory sends mDNS and then maps those that give a response
	mdnsServer       *zeroconf.Server                   // Server is to be setup using Zero ZeroConfiguration

	// Audio state            (TODO Refactor this code)
	audioBuf        *beep.Buffer
	audioFormat     beep.Format
	audioCtrl       *beep.Ctrl
	audioVol        *effects.Volume
	audioSampleRate beep.SampleRate
	audioProgress   *progressStreamer
	audioTickerStop chan struct{}
	audioMu         sync.Mutex
	audioPosition   int  // Current position in samples
	audioTotal      int  // Total samples in buffer
)

// Audio Stream
type PCMChunk struct {
	Type string `json:"type"`
	Data []byte `json:"data"`
}

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
	}

	port, err := getFreePort()
	if err != nil {
		port = 9090 // fallback
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
		clientAddr := r.RemoteAddr
		log.Printf("Remote client connected: %s", clientAddr)
		connectedClients[clientAddr] = remoteConn

		sendToUI(conn, "client_connected", map[string]any{
			"name":    "Remote Client",
			"address": clientAddr,
		})

		// Listen for messages from the remote client
		for {
			var msg UIMessage
			if err := remoteConn.ReadJSON(&msg); err != nil {
				log.Printf("Remote client %s disconnected: %v", clientAddr, err)
				delete(connectedClients, clientAddr)
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

	// starting mDNS with this port
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
	connectedClients[address] = c

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
}

func startPlayback(conn *websocket.Conn) {
	audioMu.Lock()
	defer audioMu.Unlock()

	if audioBuf == nil {
		logMsg(conn, "No audio file loaded")
		return
	}

	stream := audioBuf.Streamer(0, audioBuf.Len())
	
	// Reset position and total for progress tracking
	audioPosition = 0
	audioTotal = audioBuf.Len()

	go streamAudioToClient(stream, conn)

	sendToUI(conn, "playback_started", map[string]any{})

	// Start progress ticker
	if audioTickerStop != nil {
		close(audioTickerStop)
	}
	audioTickerStop = make(chan struct{})
	go progressTicker(conn)

	// TODO Update Audio States that we removed
}

func pausePlayback(conn *websocket.Conn) {
	audioMu.Lock()
	defer audioMu.Unlock()
	if audioCtrl != nil {
		audioCtrl.Paused = true
	}
	sendToUI(conn, "playback_paused", map[string]any{})
}

func stopPlayback(conn *websocket.Conn) {
	audioMu.Lock()
	defer audioMu.Unlock()
	if audioCtrl != nil {
		audioCtrl.Paused = true
	}
	if audioTickerStop != nil {
		close(audioTickerStop)
		audioTickerStop = nil
	}
	sendToUI(conn, "playback_stopped", map[string]any{})
}

func setVolume(conn *websocket.Conn, level float64) {
	audioMu.Lock()
	defer audioMu.Unlock()
	if audioVol != nil {
		// Map 0..100 to -5..0 (approx 1/32 to full volume)
		gain := (level - 100.0) / 20.0
		audioVol.Volume = gain
	}
	sendToUI(conn, "volume_changed", map[string]any{"level": level})
}

func loadAudioFile(conn *websocket.Conn, filepath string) {

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

	audioFormat = format
	audioBuf = beep.NewBuffer(format)
	audioBuf.Append(stream)
	durationSec := float64(audioBuf.Len()) / float64(format.SampleRate)
	sendToUI(conn, "file_loaded", map[string]any{
		"filename": filepath,
		"duration": durationSec,
	})
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)  // Increased timeout
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
}

func initSpeakerOnce(sr beep.SampleRate) error {
	// speaker.Init is safe to call multiple times? We guard by a try-init.
	// If already initialized, calling again may panic; so we use recover.
	defer func() {
		_ = recover()
	}()
	return speaker.Init(sr, sr.N(time.Second/10))
}

type progressStreamer struct {
	s             beep.Streamer
	samplesPlayed int
	total         int
}

func (p *progressStreamer) Stream(samples [][2]float64) (int, bool) {
	n, ok := p.s.Stream(samples)
	p.samplesPlayed += n
	return n, ok
}

func (p *progressStreamer) Err() error { return nil }

func filepathExt(p string) string {
	// robust ext for both / and \ paths
	base := p
	if i := strings.LastIndexAny(base, "/\\"); i >= 0 {
		base = base[i+1:]
	}
	return strings.ToLower(filepath.Ext(base))
}

func sendTestPacket(conn *websocket.Conn) {
	audioMu.Lock()
	defer audioMu.Unlock()

	// Send to UI
	testMsg := fmt.Sprintf("TEST_PACKET_%d", time.Now().UnixNano())
	sendToUI(conn, "test_packet", map[string]any{
		"message":   testMsg,
		"timestamp": time.Now().Unix(),
	})

	// Send to all connected remote hosts
	for addr, remoteConn := range connectedClients {
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
func samplesToPCM(samples [][2]float64) []byte {
	pcm := make([]byte, len(samples)*4)

	for i, s := range samples {

		l := int16(s[0] * 32767)
		r := int16(s[1] * 32767)

		j := i * 4

		pcm[j] = byte(l)
		pcm[j+1] = byte(l >> 8)

		pcm[j+2] = byte(r)
		pcm[j+3] = byte(r >> 8)
	}
	return pcm
}

// Host Streams Audio to Client
func streamAudioToClient(stream beep.Streamer, conn *websocket.Conn) {

	buffer := make([][2]float64, 1024)

	for {
		n, ok := stream.Stream(buffer)

		if n > 0 {
			// Update position for progress tracking
			audioMu.Lock()
			audioPosition += n
			//currentPos := audioPosition
			//totalPos := audioTotal
			audioMu.Unlock()
			
			pcm := samplesToPCM(buffer[:n])

			msg := PCMChunk{
				Type: "audio_chunk",
				Data: pcm,
			}

			for addr, c := range connectedClients {
				err := c.WriteJSON(msg)

				if err != nil {
					fmt.Println("stream error to", addr, err)
					delete(connectedClients, addr) // SUS Code (Might be unecessary)
				}
			}
		}

		if !ok {
			break
		}

		// Sleep to match audio rate
		time.Sleep(time.Duration(float64(n) / float64(audioFormat.SampleRate) * float64(time.Second)))
	}
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
			var msg PCMChunk
			err := conn.ReadJSON(&msg)
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					log.Println("Stream Finished Cleanly: ", err)
				} else {
					log.Println("Connection Error ", err)
				}
				pw.Close()
				return
			}

			if msg.Type == "audio_chunk" {
				_, err := pw.Write(msg.Data)
				if err != nil {
					log.Println("Pipe Write Failed!: ", err)
					return
				}
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
