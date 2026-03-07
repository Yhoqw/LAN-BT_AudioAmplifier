package main

import(
	"fmt"				// Reading Files
	"strings"
	"log"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"os"
	"time"
	"context"

	"github.com/faiface/beep"
	"github.com/faiface/beep/effects"
	"github.com/faiface/beep/mp3"
	"github.com/faiface/beep/speaker"
	"github.com/faiface/beep/wav"

	"github.com/gorilla/websocket"	// Go Websocket library 
	"github.com/grandcat/zeroconf"	// Zeroconf library

)


// Simple state
var (
	isHost           = false															// should decide who gets to serve the audio
	connectedClients = make(map[string]*websocket.Conn)		// map of connections to our device 
	discoveredHosts  = make(map[string]string)						// In theory sends mDNS and then maps those that give a response
	mdnsServer       *zeroconf.Server											// Server is to be setup using Zero ZeroConfiguration

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

// Message types for UI communication
type UIMessage struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

func handleUIWebSocket(writer http.ResponseWriter, request *http.Request) {
	conn, err := upgrader.Upgrade(writer, request, nil)												// Conn is going to handle the connection and upgrade the connection to a WebSocket
	if err != nil {
		log.Println("UI Websocket upgrade failed:", err)
		return
	}
	defer conn.Close()																												// defer calls conn.close before the function is returned

	fmt.Println("UI connected")

	// Send initial status
	sendToUI(conn, "status", map[string]any {
		"message": "Backend Ready",
	})

	// If we're a host, notify about this client connection
	if isHost {
		clientIP := strings.Split(request.RemoteAddr, ":")[0]
		sendToUI(conn, "client_found", map[string]any {
			"name":    "Client",
			"address": clientIP,
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

// this checks to see what has been selected from the frontend
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

		sendTestPacket(conn)

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
	ip, err := getLocalIP()
	if err != nil {
		log.Printf("failed to get IP: %v using fallback", err)
		ip = "0.0.0.0"
	}

	sendToUI(conn, "host_started", map[string]any {
		"address": ip,
		"port":    9090,
	})

	logMsg(conn, "Now hosting at "+ip)
}

func scan_devices(conn *websocket.Conn) {
	fmt.Println("Scanning for devices...")
	mdnsBrowseOnce(conn)

	for name, address := range discoveredHosts {
		sendToUI(conn, "device_found", map[string]any {
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

	// Notify the host that a client connected

	ip, err := getLocalIP()
	if err != nil {
		log.Printf("failed to get IP: %v using fallback", err)
		ip = "0.0.0.0"
	}
	hostNotification := UIMessage{
		Type: "client_connected",
		Data: map[string]any {
			"name":    "Remote Client",
			"address": ip,
		},
	}
	if err := c.WriteJSON(hostNotification); err != nil {
		logMsg(conn, "Failed to notify host: "+err.Error())
		return
	}

	sendToUI(conn, "connected", map[string]any {
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
	sendToUI(conn, "playback_started", map[string]any {"position": 0.0})
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
				sendToUI(conn, "progress_update", map[string]any {
					"position": pos,
					"total":    total,
				})
			case <-done:
				sendToUI(conn, "playback_stopped", map[string]any{} )
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
	sendToUI(conn, "playback_paused", map[string]any{} )
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
	sendToUI(conn, "volume_changed", map[string]any {"level": level})
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
	sendToUI(conn, "file_loaded", map[string]any {
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

func sendToUI(conn *websocket.Conn, msgType string, data map[string]any ) {
	msg := UIMessage{Type: msgType, Data: data}
	conn.WriteJSON(msg)
}

func logMsg(conn *websocket.Conn, message string) {
	sendToUI(conn, "log", map[string]any {"message": message})
}

func startDiscovery() {
	name, _ := os.Hostname()
	ip, err := getLocalIP()													// getting our own IP
	instance := fmt.Sprintf("%s-%s", name, ip)

	var portInt int = 9090

	// Create an identifier to be discovered
	server, err := zeroconf.Register(
		instance,
		"_lan-bt-audio._tcp",
		"local.",
		portInt,
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
			sendToUI(conn, "device_found", map[string]any {
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

func sendTestPacket(conn *websocket.Conn) {
	audioMu.Lock()
	defer audioMu.Unlock()

	// Send to UI
	testMsg := fmt.Sprintf("TEST_PACKET_%d", time.Now().UnixNano())
	sendToUI(conn, "test_packet", map[string]any {
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
			Data: map[string]any {
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
