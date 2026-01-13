from tkinter import *
from tkinter import ttk, scrolledtext, messagebox, filedialog

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

# UI colors
bg_color = "#f5f5f5"
accent_color = "#0a84ff"
secondary_bg_color = "#e9e9e9"
fg_color = "#000000"

def setup_ui(root):
    """Setup the main UI components."""
    global status_var, devices_tree, connected_listbox, track_label
    global progress_var, play_btn, log_text
    
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
          fg=accent_color).grid(row=0, column=0, columnspan=3, pady=(0, 10))
    
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
    
    Button(btn_frame, text="Connect", 
           command=connect_to_host, 
           bg=secondary_bg_color,
           padx=15, 
           pady=5).pack(side=LEFT, padx=5)
    
    Button(btn_frame, text="Disconnect", 
           command=disconnect, 
           bg=secondary_bg_color,
           padx=15, 
           pady=5).pack(side=LEFT, padx=5)
    
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
    
    # Treeview for devices
    tree_frame = Frame(devices_frame, bg=bg_color)
    tree_frame.pack(fill=BOTH, expand=True)
    
    devices_tree = ttk.Treeview(tree_frame, 
                                columns=("name", "type", "status"), 
                                show="headings", 
                                height=12)
    
    devices_tree.heading("name", text="Device Name")
    devices_tree.heading("type", text="Type")
    devices_tree.heading("status", text="Status")
    
    devices_tree.column("name", width=150)
    devices_tree.column("type", width=120)
    devices_tree.column("status", width=120)
    
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
    progress_bar = ttk.Progressbar(audio_frame, variable=progress_var, length=400)
    
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
    
    Button(control_frame, text="⏮", command=previous_track, bg=secondary_bg_color,
           width=5).pack(side=LEFT, padx=2)
    
    play_btn = Button(control_frame, 
                      text="▶ Play", 
                      command=toggle_playback, 
                      bg=accent_color, 
                      fg="white", 
                      width=8, 
                      state=DISABLED)
    
    play_btn.pack(side=LEFT, padx=2)
    
    Button(control_frame, 
           text="⏭", 
           command=next_track, 
           bg=secondary_bg_color,
           width=5).pack(side=LEFT, padx=2)
    
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
    
    volume_scale = Scale(volume_frame,                                                   # Volume slider
                         from_=0, 
                         to=100, 
                         orient=HORIZONTAL, 
                         length=150, 
                         bg=bg_color)    
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

def become_host():
    global is_host, is_connected_devices
    is_host = True
    status_var.set("Hosting - Waiting for connections")
    is_connected_devices.append("This device (Host)")
    update_connected_list()
    log_message("Started as host device")

def scan_devices():
    log_message("Scanning for nearby devices...")
    # Clear existing devices
    for item in devices_tree.get_children():
        devices_tree.delete(item)

def connect_to_host():
    global is_connected_to_host, is_connected_devices
    selection = devices_tree.selection()
    
    if not selection:
        messagebox.showwarning("No selection", "Please select a device to connect to")
        return
    
    item = selection[0]
    device_name = devices_tree.item(item, "values")[0]
    
    status_var.set(f"Connected to: {device_name}")
    is_connected_to_host = True
    
    if device_name not in is_connected_devices:
        is_connected_devices.append(device_name)
        update_connected_list()
    
    log_message(f"Connected to host: {device_name}")

def disconnect():
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
    update_progress()

def pause_streaming():
    global is_streaming
    is_streaming = False
    play_btn.config(text="▶ Play")
    log_message("Audio streaming paused")

def stop_streaming():
    global is_streaming
    is_streaming = False
    play_btn.config(text="▶ Play")
    progress_var.set(0)
    log_message("Audio streaming stopped")

def previous_track():
    log_message("Previous track")
    progress_var.set(0)

def next_track():
    log_message("Next track")
    progress_var.set(0)

def update_progress():
    global is_streaming
    if is_streaming:
        current = progress_var.get()
        if current < 100:
            progress_var.set(current + 0.5)
            root.after(100, update_progress)
        else:
            stop_streaming()

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

def main():
    setup_ui(root)

if __name__ == "__main__":
    main()
    root.mainloop()