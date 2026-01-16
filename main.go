package main

import (
	"context"
	"fmt"
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
	"github.com/faiface/beep/wav"
	"github.com/gorilla/websocket"
	"github.com/grandcat/zeroconf"
)

// Simple state
var (
	isHost           = false
	connectedClients = make(map[string]*websocket.Conn)
	discoveredHosts  = make(map[string]string)
	mdnsServer       *zeroconf.Server

	// Audio state
	audioBuf        *beep.Buffer
	audioFormat     beep.Format
	audioCtrl       *beep.Ctrl
	audioVol        *effects.Volume
	audioSampleRate beep.SampleRate
	audioProgress   *progressStreamer
	audioTickerStop chan struct{}
	audioMu         sync.Mutex
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
		startPlayback(conn)

	case "pause":
		pausePlayback(conn)

	case "stop":
		stopPlayback(conn)

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
	mdnsBrowseOnce(conn)

	for name, address := range discoveredHosts {
		sendToUI(conn, "device_found", map[string]interface{}{
			"name":    name,
			"address": address,
			"type":    "host",
		})
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
	sendToUI(conn, "connected", map[string]interface{}{
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
	if audioTickerStop != nil {
		close(audioTickerStop)
		audioTickerStop = nil
	}
	stream := audioBuf.Streamer(0, audioBuf.Len())
	audioProgress = &progressStreamer{s: stream, total: audioBuf.Len()}
	audioCtrl = &beep.Ctrl{Streamer: audioProgress, Paused: false}
	if audioVol == nil {
		audioVol = &effects.Volume{Streamer: audioCtrl, Base: 2, Volume: 0, Silent: false}
	} else {
		audioVol.Streamer = audioCtrl
	}
	if audioSampleRate == 0 {
		audioSampleRate = audioFormat.SampleRate
	}
	if err := initSpeakerOnce(audioSampleRate); err != nil {
		logMsg(conn, "Audio init failed: "+err.Error())
		return
	}
	done := make(chan bool, 1)
	speaker.Play(beep.Seq(audioVol, beep.Callback(func() {
		done <- true
	})))
	sendToUI(conn, "playback_started", map[string]interface{}{"position": 0.0})
	audioTickerStop = make(chan struct{})
	go func(stop <-chan struct{}) {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				pos := float64(audioProgress.samplesPlayed)
				total := float64(audioProgress.total)
				sendToUI(conn, "progress_update", map[string]interface{}{
					"position": pos,
					"total":    total,
				})
			case <-done:
				sendToUI(conn, "playback_stopped", map[string]interface{}{})
				return
			}
		}
	}(audioTickerStop)
}

func pausePlayback(conn *websocket.Conn) {
	audioMu.Lock()
	defer audioMu.Unlock()
	if audioCtrl != nil {
		audioCtrl.Paused = true
	}
	sendToUI(conn, "playback_paused", map[string]interface{}{})
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
	sendToUI(conn, "playback_stopped", map[string]interface{}{})
}

func setVolume(conn *websocket.Conn, level float64) {
	audioMu.Lock()
	defer audioMu.Unlock()
	if audioVol != nil {
		// Map 0..100 to -5..0 (approx 1/32 to full volume)
		gain := (level - 100.0) / 20.0
		audioVol.Volume = gain
	}
	sendToUI(conn, "volume_changed", map[string]interface{}{"level": level})
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
	sendToUI(conn, "file_loaded", map[string]interface{}{
		"filename": filepath,
		"duration": durationSec,
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
	name, _ := os.Hostname()
	ip := getLocalIP()
	instance := fmt.Sprintf("%s-%s", name, ip)
	server, err := zeroconf.Register(
		instance,
		"_lan-bt-audio._tcp",
		"local.",
		9090,
		[]string{"path=/ws"},
		nil,
	)
	if err != nil {
		fmt.Println("mDNS register failed:", err)
		return
	}
	mdnsServer = server
	fmt.Println("mDNS service advertised")
}

func mdnsBrowseOnce(conn *websocket.Conn) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		fmt.Println("mDNS resolver create failed:", err)
		return
	}
	entries := make(chan *zeroconf.ServiceEntry)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func(results <-chan *zeroconf.ServiceEntry) {
		for entry := range results {
			addr := ""
			if len(entry.AddrIPv4) > 0 {
				addr = fmt.Sprintf("%s:%d", entry.AddrIPv4[0].String(), entry.Port)
			} else if len(entry.AddrIPv6) > 0 {
				addr = fmt.Sprintf("[%s]:%d", entry.AddrIPv6[0].String(), entry.Port)
			}
			sendToUI(conn, "device_found", map[string]interface{}{
				"name":    entry.Instance,
				"address": addr,
				"type":    "host",
			})
		}
	}(entries)
	err = resolver.Browse(ctx, "_lan-bt-audio._tcp", "local.", entries)
	if err != nil {
		fmt.Println("mDNS browse failed:", err)
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
