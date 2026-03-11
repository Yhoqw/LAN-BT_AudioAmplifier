// Written By Yazdan Ali Khan and Azlan Ali Khan, 2026

/* connecting our backend to our frontend (Go Backend to Python Frontend)
   We are using ZeroConfig which uses mDNS to "advertise" our service, essentialy to search for devices (Network Devices)
	 then we connect to devices via WebSockets, after that we send the audio in this case to be streamed in realtime

	 For Audio we encode via pcm then we send that data to Slave which then uses oto library to play that audio

	--PS--
	ZeroConf only works for LAN Devices
*/

package main

import (
	"bytes"
	"context" // For Async tasks
	"flag"    // Command line argument parsing
	"fmt"     // Reading Files
	"io"
	"log" // Logging
	"math"
	"net" // For getting free ports
	"net/http" // HTTP client interface
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"

	// Audio Streaming and Playback Libraries
	"github.com/ebitengine/oto/v3"   // Audio Player for PCM Raw Data
	"github.com/faiface/beep/vorbis" // ogg file decoder

	// Network Programming Libraries, this websocket library and the ZeroConfiguration Networking library
	"github.com/gorilla/websocket" // Go Websocket library
	"github.com/grandcat/zeroconf" // Zeroconf library
)


// Command line flags
var (
	uiMode bool
	port int
	masterPort int
)

/*
Upgrades the http to a Websocket
Client sends an HTTP request to the server to upgrade connection used for HTTP to use WebSocket Protocol
*/
var upgrader = websocket.Upgrader{
	// Currently CheckOrigin allows for connection from any origin, (allows all WebSocket Connections regardless of Origin)
	CheckOrigin: func(r *http.Request) bool {
		return true

		/* for safety (mainly in client/server applications this is the standard code written to allow only for connections from the server)
		origin := r.Header.Get("Origin")
		return origin == "http://localhost:<PORT>" */
	},
}

// ===FUNCTIONS===

func main() {	
	flag.BoolVar(&uiMode, "ui", false, "Run in UI mode (Python frontend)")
	flag.IntVar(&port, "port", 0, "Port for UI WebSocket server (0 for auto-assign)")
	flag.IntVar(&masterPort, "master-port", 0, "Port for master WebSocket server (0 for auto-assign)")
	flag.Parse()
	
	var ver int
	if !uiMode {
		fmt.Println("Which version do you want to run Python UI or TUI? 1 or 0")
		fmt.Scan(&ver)
	} else {
		ver = 1
	}

	if ver == 1 {

		//Currently the UI Code and the code written for this TUI is completely different
		//Start Websocket server
		http.HandleFunc("/ws", handleUIWebSocket) // This is to connect the backend and the frontend
		
		// Use provided port or get a free one
		if port == 0 {
			var err error
			port, err = getFreePort()
			if err != nil {
				log.Fatal("Failed to get free port:", err)
			}
		}
		
		PORT := strconv.Itoa(port) // Our Backends PORT
		fmt.Printf("Listening on http://localhost:%s\n", PORT)
		fmt.Printf("Python UI should connect to ws://localhost:%s/ws\n", PORT)
		fmt.Printf("Use --port=%d for subsequent instances\n", port)

		if err := http.ListenAndServe(":"+PORT, nil); err != nil {
			log.Fatal("Server error:", err)
		}
		return
	}

	cli()
}

// Colors for the Terminal
const (
	COLOR_RESET  = "\033[0m"
	COLOR_GREEN  = "\x1b[32m"
	COLOR_RED    = "\033[0;31m"
	COLOR_YELLOW = "\033[33m"
)

func cli() {
	fmt.Printf(COLOR_YELLOW + "Are you Hosting or Connecting to a Device? Press 1 to Host, Any other key to Connect to a Device: " + COLOR_RESET)
	var opt int
	fmt.Scan(&opt)

	// MASTER
	if opt == 1 {
		master()
	} else { // SLAVE
		slave()
	}

}

// Handles the Server Side
func master() {

	// Use provided port or get a free one
	if masterPort == 0 {
		var err error
		masterPort, err = getFreePort()
		if err != nil {
			panic(err)
		}
	}

	// Create unique service name with port and timestamp
	instanceName := fmt.Sprintf("GoZeroconf-%d-%d", masterPort, time.Now().Unix()%10000)

	// Zeroconf Registration (Advertisement)
	server, err := zeroconf.Register(
		instanceName,
		"_workstation._tcp",
		"local.",
		masterPort,
		[]string{"txtv=0", "lo=1", "la=2"},
		nil,
	)
	if err != nil {
		panic(err)
	}
	defer server.Shutdown()

	// This is the Websocket listener and the handler for the MASTER
	http.HandleFunc("/ws", wsHandler)

	go func() {
		log.Printf("Websocket server listening on :%d", masterPort)
		log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", masterPort), nil))
	}()

	// Wait for interrupt or timeout
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	select {
	case <-sig:
		break
	case <-time.After(time.Second * 280): // this server will timeout in 280 seconds and shut down
	}

	log.Println("Shutting down.")
}

func slave() {

	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		log.Fatalln("failed to initialize the resolver:", err.Error())
	}

	entries := make(chan *zeroconf.ServiceEntry)

	// Used to store a record of the services
	var services []*zeroconf.ServiceEntry
	var mu sync.Mutex

	// WaitGroup
	var wg sync.WaitGroup

	// ================================= NETWORK DISCOVERY =========================================
	// Service-type processor
	go func(results <-chan *zeroconf.ServiceEntry) {

		discovered := make(map[string]bool)
		for entry := range results {
			// Tells us the service Type
			serviceType := strings.TrimSuffix(entry.Instance, ".local")
			if !discovered[serviceType] {
				discovered[serviceType] = true

				log.Printf(COLOR_GREEN+"Found %s service"+COLOR_RESET, serviceType)

				// Channel and context for instance resolution
				instanceChan := make(chan *zeroconf.ServiceEntry)
				newctx, cancelctx := context.WithTimeout(context.Background(), 5*time.Second)

				// Goroutine to process instances
				wg.Add(1)
				go func(ch <-chan *zeroconf.ServiceEntry) {
					defer wg.Done()
					defer cancelctx()

					for inst := range ch {
						log.Printf(COLOR_GREEN + "📡 Service Instance Found!" + COLOR_RESET)
						log.Printf("   Instance: %s", inst.Instance)
						log.Printf("   IP: %v", inst.AddrIPv4)
						log.Printf("   Port: %d", inst.Port)
						log.Printf("   HostName: %s", inst.HostName)

						// Add the entry to services list
						mu.Lock()
						services = append(services, inst)
						mu.Unlock()
					}
				}(instanceChan)

				// Start browsing instances of this service type
				go resolver.Browse(newctx, serviceType, "local", instanceChan)
			}

			// Phase 1 log (service type discovery)
			log.Printf("Discovered service type: %s", serviceType)
		}

		if len(discovered) == 0 {
			log.Println(COLOR_RED + "No services found!" + COLOR_RESET)
			log.Println("\tTips: check firewall, or enable service discovery, or make sure other devices advertise")
		} else {
			log.Printf("Found %d different service types", len(discovered))
		}
	}(entries)

	// Browse for all service types
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Println("🔍 Browsing for ALL services on network...")
	err = resolver.Browse(ctx, "_services._dns-sd._udp", "local", entries)
	if err != nil {
		log.Fatalln("Failed to browse:", err.Error())
	}

	// Wait for main browse to finish
	<-ctx.Done()

	// Wait for all instance resolution goroutines to finish
	wg.Wait()

	// ======================================= DEVICE CONNECTION ================================================

	log.Println(COLOR_YELLOW + "✅ Discovery complete" + COLOR_RESET)

	fmt.Println("Which of these services do you want to connect to? ")

	// Service and IP Selection, do-while Loop equivalent written to allow for safe selection
	var I int
	var n int
	var ipno int

	for {
		for i, s := range services {
			fmt.Printf("[%d] %s (%v:%d)\n", i, s.Instance, s.AddrIPv4, s.Port)
			I++
		}
		fmt.Scan(&n)

		if n <= I {

			fmt.Printf("Which ip of these do you want to select?")
			for {
				fmt.Scan(&ipno)

				if ipno <= I {
					break
				}
				fmt.Println(COLOR_YELLOW + "Cannot select out of range! Try Again: " + COLOR_RESET)
			}
			break
		}
		fmt.Println(COLOR_YELLOW + "Cannot select out of range! Try Again: " + COLOR_RESET)
	}

	// Here initiate the WebSocket Connection
	wsPort := services[n].Port
	ip := services[n].AddrIPv4[ipno].String()

	url := fmt.Sprintf("ws://%s:%d/ws", ip, wsPort)
	log.Println("Connecting to ", url)

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		log.Fatal("Dial error:", err)
	}
	defer conn.Close()

	//----Initializing Audio and playing the bytes Sent by the Server----

	// Initialize Audio Context with unique configuration to avoid conflicts
	op := &oto.NewContextOptions{}
	op.SampleRate = 44100
	op.ChannelCount = 2
	op.Format = oto.FormatSignedInt16LE
	
	// Add buffer size to reduce conflicts
	op.BufferSize = 8192

	audioCtx, ready, err := oto.NewContext(op)
	if err != nil {
		log.Printf("Failed to create audio context: %v", err)
		return
	}
	<-ready
	log.Println("oto Ready")

	pr, pw := io.Pipe()

	go func() {
		silence := make([]byte, 44100*4) // 1 second of silence
		pw.Write(silence)

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					log.Println("Stream Finished Cleanly")
				} else {
					log.Println("Connection Error: ", err)
				}

				pw.Close()
				return
			}
			pw.Write(data)
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

// Test Function To ensure the Audio Context has been properly initialized and oto library implementation is working as intended
func playTestTone(audioCtx *oto.Context) {
	sampleRate := 44100
	duration := 2      // seconds
	frequency := 440.0 // Hz (A4 note)
	numSamples := sampleRate * duration

	pcm := make([]byte, numSamples*4) // *4 for stereo int16

	for i := range numSamples {
		t := float64(i) / float64(sampleRate)
		sample := int16(math.Sin(2*math.Pi*frequency*t) * 32767 * 0.3) // 0.3 = volume

		// Left channel
		pcm[i*4+0] = byte(sample)
		pcm[i*4+1] = byte(sample >> 8)
		// Right channel
		pcm[i*4+2] = byte(sample)
		pcm[i*4+3] = byte(sample >> 8)
	}

	player := audioCtx.NewPlayer(bytes.NewReader(pcm))
	player.Play()

	for player.IsPlaying() {
		time.Sleep(100 * time.Millisecond)
	}

	log.Println("Test tone done — audio pipeline works!")
}

// ---------------------------------------------------------------------------------------------------------------------------------------------------------------

// Is the WebSocket Handler used by the Master to send the Audio as Raw PCM Data via StreamAudio(conn) function
func wsHandler(w http.ResponseWriter, r *http.Request) {

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}
	defer conn.Close()

	log.Println("Client Connected")
	streamAudio(conn, "STOMP")

	err = conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "Stream Finished"),
	)
	if err != nil {
		log.Println("Close Handshake error: ", err)
	}
	time.Sleep(500 * time.Millisecond)
	log.Println("Client Disconnected")
}

// The Audio is streamed from the Master and the stream is sent as PCM Data to the Slave (Client) to "Read"/Play
func streamAudio(conn *websocket.Conn, file string) {

	f, err := os.Open(file + ".ogg")
	if err != nil {
		log.Fatal(err)
		return
	}
	defer f.Close()

	streamer, format, err := vorbis.Decode(f)
	if err != nil {
		log.Fatal(err)
	}
	defer streamer.Close()

	fmt.Printf("Streaming: %v Hz, %v Channels\n", format.SampleRate, format.NumChannels)

	/* using pcm to send the audio data to the other devices (Encoding)
	[][2] since the audio is split between left and right channels (stereo sound)*/
	buffer := make([][2]float64, 1024)

	for {
		// Streams the Audio
		n, ok := streamer.Stream(buffer)
		if !ok {
			break
		}

		pcm := make([]byte, n*4)

		for i := range n {
			left := int16(buffer[i][0] * 32767)
			right := int16(buffer[i][1] * 32767)

			pcm[i*4+0] = byte(left)
			pcm[i*4+1] = byte(left >> 8)
			pcm[i*4+2] = byte(right)
			pcm[i*4+3] = byte(right >> 8)
		}

		// Sending the Stream Data
		err = conn.WriteMessage(websocket.BinaryMessage, pcm)
		if err != nil {
			log.Println("Write error:", err)
			return
		}
		log.Printf("Sent chunk: %d bytes", len(pcm))

		// To ensure that the entire stream data is sent in time (and not all at once)
		sleepDuration := time.Duration(float64(n) / float64(format.SampleRate) * float64(time.Second))
		time.Sleep(sleepDuration)
	}

	log.Println("Finished Streaming. Waiting for buffer to clear..")
	time.Sleep(2 * time.Second)
}

func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0") // :0 asks OS to pick a free port
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
