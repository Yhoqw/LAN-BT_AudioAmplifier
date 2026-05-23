// Written By Yazdan Ali Khan and Azlan Ali Khan, 2026

package main

import (
	"context"
	"fmt"
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

	"github.com/ebitengine/oto/v3"
	"github.com/faiface/beep"
	"github.com/faiface/beep/mp3"
	"github.com/faiface/beep/vorbis"
	"github.com/faiface/beep/wav"
	"github.com/gorilla/websocket"
	"github.com/grandcat/zeroconf"
)

// ── State ──────────────────────────────────────────────────────────────────

var (
	isHost           bool
	connectedClients = make(map[string]*websocket.Conn)
	clientsMu        sync.RWMutex
	discoveredHosts  = make(map[string]string)
	mdnsServer       *zeroconf.Server

	// Audio state — guarded by audioMu
	selectedAudioPath string
	audioSampleRate   beep.SampleRate
	audioTickerStop   chan struct{}
	streamStop        chan struct{}
	streamDone        chan struct{}
	audioMu           sync.Mutex
	audioPosition     int
	audioTotal        int
)

// ── Message protocol ───────────────────────────────────────────────────────

type UIMessage struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

// ── UI WebSocket entry point ───────────────────────────────────────────────

func handleUIWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("UI WebSocket upgrade failed:", err)
		return
	}
	defer conn.Close()

	log.Println("UI connected")
	sendToUI(conn, "status", map[string]any{"message": "Backend ready"})

	for {
		var msg UIMessage
		if err := conn.ReadJSON(&msg); err != nil {
			log.Println("UI disconnected:", err)
			break
		}
		handleUIMessage(conn, msg)
	}
}

// handleUIMessage dispatches commands received from the Python frontend.
func handleUIMessage(conn *websocket.Conn, msg UIMessage) {
	log.Println("UI command:", msg.Type)

	switch msg.Type {
	case "become_host":
		if isHost {
			logMsg(conn, "Already hosting.")
			return
		}
		becomeHost(conn)

	case "scan_devices":
		if isHost {
			logMsg(conn, "Host cannot scan for devices. Switch to client mode first.")
			return
		}
		logMsg(conn, "Starting mDNS discovery…")
		scanDevices(conn)

	case "connect_device":
		if isHost {
			logMsg(conn, "Host cannot connect to other devices.")
			return
		}
		if addr, ok := msg.Data["address"].(string); ok {
			connectToHost(conn, addr)
		}

	case "play":
		if isHost {
			startPlayback(conn)
		} else {
			logMsg(conn, "Only the host can control playback.")
		}

	case "pause":
		if isHost {
			pausePlayback(conn)
		} else {
			logMsg(conn, "Only the host can control playback.")
		}

	case "stop":
		if isHost {
			stopPlayback(conn)
		} else {
			logMsg(conn, "Only the host can control playback.")
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

// ── Host logic ─────────────────────────────────────────────────────────────

func becomeHost(conn *websocket.Conn) {
	isHost = true

	ip, err := getLocalIP()
	if err != nil {
		log.Printf("getLocalIP failed (%v), falling back to 0.0.0.0", err)
		ip = "0.0.0.0"
	}

	audioPort, err := getFreePort()
	if err != nil {
		logMsg(conn, "Could not get a free port; falling back to 9090.")
		audioPort = 9090
	}

	sendToUI(conn, "host_started", map[string]any{
		"address": ip,
		"port":    audioPort,
	})
	logMsg(conn, fmt.Sprintf("Now hosting at %s:%d", ip, audioPort))

	// Audio WebSocket server on  dedicated Mux.
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
			"name":    "Remote client",
			"address": clientAddr,
		})

		// Keep reading so we detect disconnects; clients send no commands.
		for {
			if _, _, err := remoteConn.ReadMessage(); err != nil {
				log.Printf("Remote client %s disconnected: %v", clientAddr, err)
				clientsMu.Lock()
				delete(connectedClients, clientAddr)
				clientsMu.Unlock()
				return
			}
		}
	})

	go func() {
		log.Printf("Audio WebSocket server listening on :%d", audioPort)
		if err := http.ListenAndServe(fmt.Sprintf(":%d", audioPort), hostMux); err != nil {
			log.Printf("Audio server error: %v", err)
		}
	}()

	go startMDNS(audioPort, ip)
}

// ── Client logic ───────────────────────────────────────────────────────────

func scanDevices(conn *websocket.Conn) {
	mdnsBrowseOnce(conn)

	// Surface any hosts found in a previous scan that are still in the map.
	for name, address := range discoveredHosts {
		sendToUI(conn, "device_found", map[string]any{
			"name":    name,
			"address": address,
			"type":    "host",
		})
	}

	if len(discoveredHosts) == 0 {
		logMsg(conn, "No devices found. Try 'Direct Connect' with a manual IP.")
	} else {
		logMsg(conn, fmt.Sprintf("Found %d device(s).", len(discoveredHosts)))
	}
}

func connectToHost(conn *websocket.Conn, address string) {
	isHost = false
	u := url.URL{Scheme: "ws", Host: address, Path: "/ws"}

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		logMsg(conn, "Connection failed: "+err.Error())
		return
	}

	clientsMu.Lock()
	connectedClients[address] = c
	clientsMu.Unlock()

	sendToUI(conn, "connected", map[string]any{
		"address": address,
		"name":    "Remote host",
	})
	logMsg(conn, "Connected to host: "+address)

	go startClientAudio(c)
}

// ── Playback control ───────────────────────────────────────────────────────

func startPlayback(conn *websocket.Conn) {
	audioMu.Lock()
	if selectedAudioPath == "" {
		audioMu.Unlock()
		logMsg(conn, "No audio file loaded.")
		return
	}
	path := selectedAudioPath

	if streamStop != nil {
		close(streamStop)
	}
	streamStop = make(chan struct{})
	streamDone = make(chan struct{})
	audioPosition = 0
	stop := streamStop
	done := streamDone

	if audioTickerStop != nil {
		close(audioTickerStop)
	}
	audioTickerStop = make(chan struct{})
	audioMu.Unlock()

	sendToUI(conn, "playback_started", map[string]any{})
	go streamAudioToClients(conn, path, stop, done)
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
	// Volume is applied on the client side; we just echo it back to the UI.
	sendToUI(conn, "volume_changed", map[string]any{"level": level})
}

// loadAudioFile validates and stores the selected audio path + metadata.
// It opens the file only long enough to read the header, then closes it —
// streamAudioToClients opens it again when playback actually starts.
func loadAudioFile(conn *websocket.Conn, path string) {
	f, err := os.Open(path)
	if err != nil {
		logMsg(conn, "Could not open file: "+err.Error())
		return
	}
	defer f.Close()

	ext := strings.ToLower(filepath.Ext(path))
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

	audioMu.Lock()
	audioSampleRate = format.SampleRate
	audioTotal = stream.Len()
	selectedAudioPath = path
	audioMu.Unlock()

	durationSec := 0.0
	if format.SampleRate > 0 && stream.Len() > 0 {
		durationSec = float64(stream.Len()) / float64(format.SampleRate)
	}

	sendToUI(conn, "file_loaded", map[string]any{
		"filename": path,
		"duration": durationSec,
	})
	logMsg(conn, fmt.Sprintf("Loaded: %s", filepath.Base(path)))
}

// ── Audio streaming ────────────────────────────────────────────────────────

// samplesToPCM converts decoded float64 stereo samples to signed 16-bit
// little-endian PCM bytes (4 bytes per frame: L_lo, L_hi, R_lo, R_hi).
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

// streamAudioToClients opens the audio file and broadcasts PCM chunks to
// every connected client until the file ends or stop is closed.
func streamAudioToClients(uiConn *websocket.Conn, filePath string, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	f, err := os.Open(filePath)
	if err != nil {
		logMsg(uiConn, "Open failed: "+err.Error())
		return
	}
	defer f.Close()

	ext := strings.ToLower(filepath.Ext(filePath))
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

	// Send stream metadata to clients before any PCM so they can set up
	// their audio context correctly.
	info := UIMessage{
		Type: "stream_info",
		Data: map[string]any{
			"sample_rate":   int(format.SampleRate),
			"channel_count": format.NumChannels,
			"format":        "pcm_s16le",
		},
	}
	clientsMu.RLock()
	for addr, c := range connectedClients {
		if err := c.WriteJSON(info); err != nil {
			log.Println("stream_info error to", addr, err)
		}
	}
	clientsMu.RUnlock()

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
				if err := c.WriteMessage(websocket.BinaryMessage, pcm); err != nil {
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

		sleep := time.Duration(float64(n) / float64(format.SampleRate) * float64(time.Second))
		time.Sleep(sleep)
	}

	sendToUI(uiConn, "playback_stopped", map[string]any{})
}

func progressTicker(conn *websocket.Conn) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	audioMu.Lock()
	tickerStop := audioTickerStop
	audioMu.Unlock()

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
		case <-tickerStop:
			return
		}
	}
}

// startClientAudio receives raw PCM from the host and plays it via oto.
// The client audio context is hardcoded to 44100 Hz stereo signed int16.
func startClientAudio(conn *websocket.Conn) {
	op := &oto.NewContextOptions{
		SampleRate:   44100,
		ChannelCount: 2,
		Format:       oto.FormatSignedInt16LE,
		BufferSize:   8192,
	}

	audioCtx, ready, err := oto.NewContext(op)
	if err != nil {
		log.Printf("oto context failed: %v", err)
		return
	}
	<-ready
	log.Println("oto ready")

	pr, pw := io.Pipe()

	go func() {
		// Prime the buffer with one second of silence to prevent underruns.
		silence := make([]byte, 44100*4)
		pw.Write(silence)

		for {
			msgType, data, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					log.Println("Stream finished cleanly.")
				} else {
					log.Println("Connection error:", err)
				}
				pw.Close()
				return
			}
			// Skip control frames (e.g. stream_info JSON); only forward PCM.
			if msgType != websocket.BinaryMessage {
				continue
			}
			if _, err := pw.Write(data); err != nil {
				log.Println("Pipe write failed:", err)
				return
			}
		}
	}()

	player := audioCtx.NewPlayer(pr)
	player.Play()
	log.Println("Player started.")

	for player.IsPlaying() {
		time.Sleep(50 * time.Millisecond)
	}
	log.Println("Playback finished.")
}

// ── mDNS ───────────────────────────────────────────────────────────────────

// startMDNS registers the host service so clients on the same LAN can
// discover it. The specific LAN interface is passed to zeroconf so the
// advertised address matches the interface clients can actually reach.
func startMDNS(audioPort int, lanIP string) {
	hostname, _ := os.Hostname()
	instance := fmt.Sprintf("%s-%s-%d", hostname, lanIP, time.Now().Unix()%10000)

	// Find the interface whose address matches our LAN IP so zeroconf
	// advertises on the right adapter (avoids VPN / VM bridge confusion).
	ifaces, err := net.Interfaces()
	if err != nil {
		log.Printf("Could not enumerate interfaces: %v — advertising on all", err)
		ifaces = nil
	}
	var selectedIfaces []net.Interface
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.String() == lanIP {
				selectedIfaces = append(selectedIfaces, iface)
			}
		}
	}

	server, err := zeroconf.Register(
		instance,
		"_lan-bt-audio._tcp",
		"local.",
		audioPort,
		[]string{"path=/ws"},
		selectedIfaces, // nil = all interfaces (safe fallback)
	)
	if err != nil {
		log.Println("mDNS registration failed:", err)
		return
	}
	mdnsServer = server
	log.Printf("mDNS advertised as: %s on port %d", instance, audioPort)
}

func mdnsBrowseOnce(conn *websocket.Conn) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		logMsg(conn, "mDNS resolver failed: "+err.Error())
		return
	}

	entries := make(chan *zeroconf.ServiceEntry)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for entry := range entries {
			addr := ""
			if len(entry.AddrIPv4) > 0 {
				addr = fmt.Sprintf("%s:%d", entry.AddrIPv4[0], entry.Port)
			} else if len(entry.AddrIPv6) > 0 {
				addr = fmt.Sprintf("[%s]:%d", entry.AddrIPv6[0], entry.Port)
			}
			if addr == "" {
				continue
			}
			discoveredHosts[entry.Instance] = addr
			sendToUI(conn, "device_found", map[string]any{
				"name":    entry.Instance,
				"address": addr,
				"type":    "host",
			})
		}
	}()

	if err := resolver.Browse(ctx, "_lan-bt-audio._tcp", "local.", entries); err != nil {
		logMsg(conn, "mDNS browse failed: "+err.Error())
		return
	}

	<-ctx.Done()
	wg.Wait()
}

// ── Helpers ────────────────────────────────────────────────────────────────

// getLocalIP returns the first non-loopback IPv4 address found on an
// active network interface.
func getLocalIP() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
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
	return "", fmt.Errorf("no valid LAN IP found")
}

func sendToUI(conn *websocket.Conn, msgType string, data map[string]any) {
	if err := conn.WriteJSON(UIMessage{Type: msgType, Data: data}); err != nil {
		log.Printf("sendToUI (%s) failed: %v", msgType, err)
	}
}

func logMsg(conn *websocket.Conn, message string) {
	sendToUI(conn, "log", map[string]any{"message": message})
}
