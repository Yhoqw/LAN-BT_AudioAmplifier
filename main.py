from tkinter import *
from tkinter import ttk, scrolledtext, messagebox, filedialog, simpledialog
from unittest import case

import websocket, json, threading

# Setting up UI
root = Tk()
root.geometry("1000x700")
root.title("Audio Amplifier")

# State variables
is_host = False
is_connected_to_host = False
is_streaming = False
is_connected_devices = []
discovered_devices = []
current_track = None    

# Global UI references
status_var = None
devices_tree = None
connected_listbox = None
track_label = None
progress_var = None
play_btn = None
log_text = None

#Webscoket stuff
volume_scale = None
ws = None
ws_lock = threading.Lock()

# UI colors
bg_color = "#f5f5f5"
accent_color = "#0a84ff"
secondary_bg_color = "#e9e9e9"
fg_color = "#000000"

def setup_ui(root):
    """Setup the main UI components."""

    global status_var, devices_tree, connected_listbox, track_label
    global progress_var, play_btn, log_text, volume_scale
    
    # Set background
    root.configure(bg=bg_color)
    
    # Main container
    main_frame = ttk.Frame(root, padding="10")
    main_frame.grid(row=0, column=0, sticky="nsew")
    
    # Configure grid weights
    root.columnconfigure(0, weight=1)
    root.rowconfigure(0, weight=1)
    main_frame.columnconfigure(1, weight=1)
    main_frame.rowconfigure(2, weight=0)
    
    # Title
    Label(main_frame, 
          text="Audio Amplifier", 
          font=("Arial", 16, "bold"), 
          bg=bg_color, 
          fg=accent_color).grid(row=0, 
                                column=0, 
                                columnspan=3, 
                                pady=(0, 10))
    
    # Status Bar
    status_var = StringVar(value="Ready to connect")
    status_frame = Frame(main_frame, 
                         bg=secondary_bg_color, 
                         relief="sunken", 
                         bd=1)
    
    status_frame.grid(row=1, 
                      column=0, 
                      columnspan=3, 
                      sticky="ew", 
                      pady=(0, 10))
    
    status_indicator = Canvas(status_frame, 
                              width=12, 
                              height=12, 
                              bg="red", 
                              highlightthickness=0)
                              
    status_indicator.pack(side=LEFT, 
                          padx=(10, 5), 
                          pady=5)
    
    status_label = Label(status_frame, 
                         textvariable=status_var, 
                         bg=secondary_bg_color)
    
    status_label.pack(side=LEFT, 
                      padx=5, 
                      pady=5)
    
    # Control buttons frame
    btn_frame = Frame(main_frame, bg=bg_color)
    btn_frame.grid(row=2, 
                   column=0, 
                   columnspan=3, 
                   pady=(0, 10), 
                   sticky="w")
    
    Button(btn_frame, text="Be Host", 
           command=become_host, 
           bg=accent_color, 
           fg="white",
           padx=15, 
           pady=5).pack(side=LEFT, padx=5)
    
    Button(btn_frame, text="Scan Devices", 
           command=scan_devices, 
           bg=secondary_bg_color,
           padx=15, 
           pady=5).pack(side=LEFT, padx=5)
    
    # Button(btn_frame, text="Connect", 
    #        command=connect_to_host, 
    #        bg=secondary_bg_color,
    #        padx=15, 
    #        pady=5).pack(side=LEFT, padx=5)
    
    # Button(btn_frame, text="Disconnect", 
    #        command=disconnect, 
    #        bg=secondary_bg_color,
    #        padx=15, 
    #        pady=5).pack(side=LEFT, padx=5)
    
    # Devices List frame
    devices_frame = LabelFrame(main_frame, 
                               text="Available Devices", 
                               padx=5, 
                               pady=5)
    devices_frame.grid(row=3, 
                       column=0, 
                       columnspan=2, 
                       sticky="nsew", 
                       padx=(0, 5))
    devices_frame.configure(bg=bg_color)
    
    ## Treeview for devices
    tree_frame = Frame(devices_frame, bg=bg_color)
    tree_frame.pack(fill=BOTH, expand=True)
    
    devices_tree = ttk.Treeview(tree_frame, 
                                columns=("name", "type", "status", "address"), 
                                show="headings", 
                                height=12)
    
    devices_tree.heading("name", text="Device Name")
    devices_tree.heading("type", text="Type")
    devices_tree.heading("status", text="Status")
    devices_tree.heading("address", text="Address")
    
    devices_tree.column("name", width=150)
    devices_tree.column("type", width=120)
    devices_tree.column("status", width=120)
    devices_tree.column("address", width=180)
    
    scrollbar = Scrollbar(tree_frame, 
                          orient=VERTICAL, 
                          command=devices_tree.yview)
    
    devices_tree.configure(yscrollcommand=scrollbar.set)
    
    devices_tree.pack(side=LEFT, fill=BOTH, expand=True)
    scrollbar.pack(side=RIGHT, fill=Y)
    
    # Connected Devices panel
    connected_frame = LabelFrame(main_frame, 
                                 text="Connected Devices", 
                                 padx=10, 
                                 pady=10)
    
    connected_frame.grid(row=3, column=2, sticky="nsew")
    connected_frame.configure(bg=bg_color)
    
    connected_listbox = Listbox(connected_frame, height=8, bg="white")
    connected_listbox.pack(fill=BOTH, expand=True)
    
    # Audio controls frame
    audio_frame = LabelFrame(main_frame, 
                             text="Audio Controls", 
                             padx=10, 
                             pady=10)
    
    audio_frame.grid(row=4, 
                     column=0, 
                     columnspan=3, 
                     sticky="ew", 
                     pady=(10, 0))
    audio_frame.configure(bg=bg_color)
    
    # Now playing label
    track_label = Label(audio_frame, 
                        text="No audio file selected", 
                        bg=bg_color)
    
    track_label.grid(row=0, 
                     column=0, 
                     columnspan=3, 
                     sticky="w", 
                     pady=(0, 10))
    
    # Progress bar
    progress_var = DoubleVar()
    progress_bar = ttk.Progressbar(audio_frame, 
                                   variable=progress_var, 
                                   length=400)
    
    progress_bar.grid(row=1, 
                      column=0, 
                      columnspan=3, 
                      sticky="ew", 
                      pady=(0, 5))
    
    # Time labels
    time_frame = Frame(audio_frame, bg=bg_color)
    time_frame.grid(row=2, 
                    column=0, 
                    columnspan=3, 
                    sticky="ew", 
                    pady=(0, 10))
    
    current_time = Label(time_frame, text="0:00", bg=bg_color)
    current_time.pack(side=LEFT)
    
    Label(time_frame, text=" / ", bg=bg_color).pack(side=LEFT)
    
    total_time = Label(time_frame, text="0:00", bg=bg_color)
    total_time.pack(side=LEFT)
    
    # Control buttons
    control_frame = Frame(audio_frame, bg=bg_color)
    control_frame.grid(row=3, 
                       column=0, 
                       columnspan=3, 
                       pady=(0, 10))
    
    play_btn = Button(control_frame, 
                      text="▶ Play", 
                      command=toggle_playback, 
                      bg=accent_color, 
                      fg="white", 
                      width=8, 
                      state=DISABLED)
    
    play_btn.pack(side=LEFT, padx=2)
    
    Button(control_frame, 
           text="⏹ Stop", 
           command=stop_streaming, 
           bg=secondary_bg_color,
           width=8).pack(side=LEFT, padx=2)
    
    # File selection button
    Button(audio_frame, 
           text="📁 Select Audio File", 
           command=select_audio_file,
           bg=secondary_bg_color).grid(row=4, 
                                       column=0, 
                                       columnspan=3, 
                                       pady=(5, 0))
    
    # Volume controls
    volume_frame = Frame(audio_frame, bg=bg_color)
    volume_frame.grid(row=5, 
                      column=0, 
                      columnspan=3, 
                      pady=(10, 0), 
                      sticky="w")
    
    Label(volume_frame, text="Volume:", bg=bg_color).pack(side=LEFT, padx=(0, 5))
    
    volume_scale = Scale(volume_frame,
                         from_=0, 
                         to=100, 
                         orient=HORIZONTAL, 
                         length=150, 
                         bg=bg_color,
                         command=lambda v: set_volume(float(v)))
    volume_scale.set(70)
    volume_scale.pack(side=LEFT, padx=5)

    # Log window
    log_frame = LabelFrame(main_frame, 
                           text="Activity Log", 
                           padx=10, 
                           pady=10)
    
    log_frame.grid(row=5, 
                   column=0, 
                   columnspan=3, 
                   sticky="nsew", 
                   pady=(10, 0))
    
    log_frame.configure(bg=bg_color)
    
    log_text = scrolledtext.ScrolledText(log_frame, height=6, bg="white")
    log_text.pack(fill=BOTH, expand=True)
    
    # Configure grid weights for resizing
    main_frame.rowconfigure(3, weight=1)
    main_frame.rowconfigure(5, weight=0)
    main_frame.columnconfigure(0, weight=1)
    main_frame.columnconfigure(1, weight=1)
    main_frame.columnconfigure(2, weight=1)
    
    # Add mock devices
    
    # Initial log message
    log_message("Application started. Ready to connect devices.")

#  ---- UI Functions ----

def become_host():
    ws_send("become_host", {})

def scan_devices():
    """Prompt user to enter IP address directly."""

    dialog = simpledialog.askstring(
        "Connect to Host",
        "Enter the IP address of the host:\n(format: 192.168.1.100:9090)",
        parent=root
    )
    
    if dialog:
        address = dialog.strip()
        if address:
            log_message(f"Attempting to connect to {address}...")
            ws_send("connect_device", {"address": address})
        else:
            messagebox.showwarning("Empty input", "Please enter a valid IP address")

def connect_to_host():                  # Related button is currently disconnected
    selection = devices_tree.selection()
    
    if not selection:
        messagebox.showwarning("No selection", "Please select a device to connect to")
        return
    
    item = selection[0]
    values = devices_tree.item(item, "values")
    device_name = values[0]
    address = values[3]
    ws_send("connect_device", {"address": address})

def disconnect():                       # Related button is currently disconnected
    global is_host, is_connected_to_host, is_streaming, is_connected_devices
    is_host = False
    is_connected_to_host = False
    is_streaming = False
    status_var.set("Disconnected")
    is_connected_devices = []
    update_connected_list()
    log_message("Disconnected from all devices")

def select_audio_file():
    global current_track
    filename = filedialog.askopenfilename(
        title="Select audio file",
        filetypes=[("Audio files", "*.mp3 *.wav *.flac *.ogg"), ("All files", "*.*")]
    )
    
    if filename:
        current_track = filename
        track_name = filename.split('/')[-1]
        track_label.config(text=f"Now playing: {track_name}")
        log_message(f"Loaded audio file: {track_name}")
        play_btn.config(state=NORMAL)
        ws_send("select_file", {"path": filename})

def toggle_playback():
    global is_streaming
    if not is_streaming:
        start_streaming()
    else:
        pause_streaming()

def start_streaming():
    global is_streaming
    is_streaming = True
    play_btn.config(text="⏸ Pause")
    progress_var.set(0)
    log_message("Audio streaming started")
    ws_send("play", {})

def pause_streaming():
    global is_streaming
    is_streaming = False
    play_btn.config(text="▶ Play")
    log_message("Audio streaming paused")
    ws_send("pause", {})

def stop_streaming():
    global is_streaming
    is_streaming = False
    play_btn.config(text="▶ Play")
    progress_var.set(0)
    log_message("Audio streaming stopped")
    ws_send("stop", {})

def set_volume(level):
    ws_send("volume", {"level": level})

def update_connected_list():
    global connected_listbox
    if connected_listbox:
        connected_listbox.delete(0, END)
        for device in is_connected_devices:
            connected_listbox.insert(END, device)

def log_message(message):
    global log_text
    import datetime
    timestamp = datetime.datetime.now().strftime("%H:%M:%S")
    log_text.insert(END, f"[{timestamp}] {message}\n")
    log_text.see(END)

def connect_backend():
    global ws
    try:
        ws = websocket.create_connection("ws://localhost:9090/ws")
        log_message("Connected to backend")

    except Exception as e:
        status_var.set("Backend connection failed")
        log_message(str(e))
        return
    
    t = threading.Thread(target=ws_listener, daemon=True)
    t.start()

def ws_send(msg_type, data):
    global ws
    if ws is None:
        return
    payload = {"type": msg_type, "data": data}
    try:
        with ws_lock:
            ws.send(json.dumps(payload))
    except Exception as e:
        log_message(str(e))

def ws_listener():
    global ws
    while True:
        try:
            raw = ws.recv()
            obj = json.loads(raw)
            root.after(0, 
                       lambda: handle_backend_message(obj))

        except Exception as e:
            error_msg = str(e)
            root.after(0,
                       lambda msg=error_msg: log_message(msg))
            break

def handle_backend_message(msg):
    """Handle incoming messages from the backend."""

    Backend_message_type = msg.get("Type") or msg.get("type")              #Type
    Backend_message_data = msg.get("Data") or msg.get("data") or {}        #Data

    match Backend_message_type:
        case "status":
            status_var.set(Backend_message_data.get("message", ""))

        case "host_started":
            global is_host
            is_host = True
            status_var.set("Hosting")

        case "device_found":
            name = Backend_message_data.get("name", "")
            addr = Backend_message_data.get("address", "")
            typ = Backend_message_data.get("type", "")

            # Check if device already exists
            existing_items = devices_tree.get_children()
            device_exists = any(
                devices_tree.item(item)["values"][3] == addr 
                for item in existing_items
            )

            if not device_exists:
                devices_tree.insert("", END, values=(name, 
                                                     typ, 
                                                     "Available", 
                                                     addr))
    
        case "connected":
            global is_connected_to_host, is_connected_devices
            is_connected_to_host = True
            name = Backend_message_data.get("name", "Remote Host")
            status_var.set(f"Connected to: {name}")
            if name not in is_connected_devices:
                is_connected_devices.append(name)
                update_connected_list()

        case "playback_started":
            global is_streaming
            is_streaming = True
            play_btn.config(text= "⏸ Pause")
            progress_var.set(0)

        case "playback_paused":
            is_streaming = False
            play_btn.config(text= "▶ Play")

        case "playback_stopped":
            is_streaming = False
            play_btn.config(text= "▶ Play")
            progress_var.set(0)

        case "progress_update":
            pos = Backend_message_data.get("position", 0.0)
            tot = Backend_message_data.get("total", 100.0)
            val = (pos / tot) * 100.0 if tot else 0.0
            progress_var.set(min(100.0, max(0.0, val)))

        case "volume_changed":
            try:
                if volume_scale:
                    volume_scale.set(int(Backend_message_data.get("level")))
            except Exception:
                pass

        case "file_loaded":
            filename = Backend_message_data.get("filename", "")
            track_name = filename.split('/')[-1].split('\\')[-1]
            track_label.config(text=f"Now playing: {track_name}")
            play_btn.config(state=NORMAL)

        case "log":
            log_message(Backend_message_data.get("message", ""))

        case "test_packet":
            msg_content = Backend_message_data.get("message", "")
            
            log_message(f"Packet sent: {msg_content}")
        
        case "test_packet_received":
            send = Backend_message_data.get("from", "Unknown")
            msg_content = Backend_message_data.get("message", "")

            log_message(f" Test packet received from {send}: {msg_content}")

        case "client_connected":
            client_name = Backend_message_data.get("name", "Unknown client")
            client_addr = Backend_message_data.get("address", "")

            log_message(f"Client connected: {client_name} ({client_addr})")

            if client_name not in is_connected_devices:
                is_connected_devices.append(client_name)
                update_connected_list()
        
        case "client_found":
            client_name = Backend_message_data.get("name", "Unknown client")
            client_addr = Backend_message_data.get("address", "")
            log_message(f"Client found the host: {client_name} ({client_addr})")           

        
def main():
    setup_ui(root)
    connect_backend()

if __name__ == "__main__":
    main()
    root.mainloop()