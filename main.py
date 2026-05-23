# Written By Yazdan Ali Khan and Azlan Ali Khan, 2026

import argparse
import datetime
import json
import threading
from tkinter import *
from tkinter import ttk, scrolledtext, messagebox, filedialog, simpledialog

import websocket

# ── Window ─────────────────────────────────────────────────────────────────

root = Tk()
root.geometry("1000x700")
root.title("Audio Amplifier")

# ── App state ──────────────────────────────────────────────────────────────

is_host = False
is_connected_to_host = False
is_streaming = False
connected_devices = []
discovered_devices = []
current_track = None

# ── UI references (populated in setup_ui) ─────────────────────────────────

status_var = None
devices_tree = None
connected_listbox = None
track_label = None
progress_var = None
play_btn = None
log_text = None
volume_scale = None

host_btn = None
scan_btn = None
direct_connect_btn = None
connect_btn = None
disconnect_btn = None

# ── WebSocket ──────────────────────────────────────────────────────────────

ws = None
ws_lock = threading.Lock()
backend_port = 9090

# ── Theme ──────────────────────────────────────────────────────────────────

BG = "#f5f5f5"
ACCENT = "#0a84ff"
SECONDARY_BG = "#e9e9e9"
FG = "#000000"


# ── UI setup ───────────────────────────────────────────────────────────────

def setup_ui(root):
    global status_var, devices_tree, connected_listbox, track_label
    global progress_var, play_btn, log_text, volume_scale
    global host_btn, scan_btn, direct_connect_btn, connect_btn, disconnect_btn

    root.configure(bg=BG)

    main_frame = ttk.Frame(root, padding="10")
    main_frame.grid(row=0, column=0, sticky="nsew")

    root.columnconfigure(0, weight=1)
    root.rowconfigure(0, weight=1)
    main_frame.columnconfigure(1, weight=1)
    main_frame.rowconfigure(2, weight=0)

    # Title
    Label(main_frame, text="Audio Amplifier", font=("Arial", 16, "bold"),
          bg=BG, fg=ACCENT).grid(row=0, column=0, columnspan=3, pady=(0, 10))

    # Status bar
    status_var = StringVar(value="Ready to connect")
    status_frame = Frame(main_frame, bg=SECONDARY_BG, relief="sunken", bd=1)
    status_frame.grid(row=1, column=0, columnspan=3, sticky="ew", pady=(0, 10))

    Canvas(status_frame, width=12, height=12, bg="red",
           highlightthickness=0).pack(side=LEFT, padx=(10, 5), pady=5)
    Label(status_frame, textvariable=status_var,
          bg=SECONDARY_BG).pack(side=LEFT, padx=5, pady=5)

    # Control buttons
    btn_frame = Frame(main_frame, bg=BG)
    btn_frame.grid(row=2, column=0, columnspan=3, pady=(0, 10), sticky="w")

    host_btn = Button(btn_frame, text="Be Host", command=become_host,
                      bg=ACCENT, fg="white", padx=15, pady=5)
    host_btn.pack(side=LEFT, padx=5)

    scan_btn = Button(btn_frame, text="Scan Devices", command=scan_devices,
                      bg=SECONDARY_BG, padx=15, pady=5)
    scan_btn.pack(side=LEFT, padx=5)

    direct_connect_btn = Button(btn_frame, text="Direct Connect",
                                command=direct_connect,
                                bg=SECONDARY_BG, padx=15, pady=5)
    direct_connect_btn.pack(side=LEFT, padx=5)

    connect_btn = Button(btn_frame, text="Connect", command=connect_to_host,
                         bg=SECONDARY_BG, padx=15, pady=5)
    connect_btn.pack(side=LEFT, padx=5)

    disconnect_btn = Button(btn_frame, text="Disconnect", command=disconnect,
                            bg=SECONDARY_BG, padx=15, pady=5)
    disconnect_btn.pack(side=LEFT, padx=5)

    # Available devices
    devices_frame = LabelFrame(main_frame, text="Available Devices", padx=5, pady=5, bg=BG)
    devices_frame.grid(row=3, column=0, columnspan=2, sticky="nsew", padx=(0, 5))

    tree_frame = Frame(devices_frame, bg=BG)
    tree_frame.pack(fill=BOTH, expand=True)

    devices_tree = ttk.Treeview(tree_frame,
                                columns=("name", "type", "status", "address"),
                                show="headings", height=12)
    for col, label, width in [
        ("name", "Device Name", 150),
        ("type", "Type", 120),
        ("status", "Status", 120),
        ("address", "Address", 180),
    ]:
        devices_tree.heading(col, text=label)
        devices_tree.column(col, width=width)

    scrollbar = Scrollbar(tree_frame, orient=VERTICAL, command=devices_tree.yview)
    devices_tree.configure(yscrollcommand=scrollbar.set)
    devices_tree.pack(side=LEFT, fill=BOTH, expand=True)
    scrollbar.pack(side=RIGHT, fill=Y)

    # Connected devices
    connected_frame = LabelFrame(main_frame, text="Connected Devices",
                                 padx=10, pady=10, bg=BG)
    connected_frame.grid(row=3, column=2, sticky="nsew")

    connected_listbox = Listbox(connected_frame, height=8, bg="white")
    connected_listbox.pack(fill=BOTH, expand=True)

    # Audio controls
    audio_frame = LabelFrame(main_frame, text="Audio Controls",
                             padx=10, pady=10, bg=BG)
    audio_frame.grid(row=4, column=0, columnspan=3, sticky="ew", pady=(10, 0))

    track_label = Label(audio_frame, text="No audio file selected", bg=BG)
    track_label.grid(row=0, column=0, columnspan=3, sticky="w", pady=(0, 10))

    progress_var = DoubleVar()
    ttk.Progressbar(audio_frame, variable=progress_var,
                    length=400).grid(row=1, column=0, columnspan=3,
                                     sticky="ew", pady=(0, 5))

    time_frame = Frame(audio_frame, bg=BG)
    time_frame.grid(row=2, column=0, columnspan=3, sticky="ew", pady=(0, 10))
    Label(time_frame, text="0:00", bg=BG).pack(side=LEFT)
    Label(time_frame, text=" / ", bg=BG).pack(side=LEFT)
    Label(time_frame, text="0:00", bg=BG).pack(side=LEFT)

    control_frame = Frame(audio_frame, bg=BG)
    control_frame.grid(row=3, column=0, columnspan=3, pady=(0, 10))

    play_btn = Button(control_frame, text="▶ Play", command=toggle_playback,
                      bg=ACCENT, fg="white", width=8, state=DISABLED)
    play_btn.pack(side=LEFT, padx=2)

    Button(control_frame, text="⏹ Stop", command=stop_streaming,
           bg=SECONDARY_BG, width=8).pack(side=LEFT, padx=2)

    Button(audio_frame, text="📁 Select Audio File",
           command=select_audio_file,
           bg=SECONDARY_BG).grid(row=4, column=0, columnspan=3, pady=(5, 0))

    volume_frame = Frame(audio_frame, bg=BG)
    volume_frame.grid(row=5, column=0, columnspan=3, pady=(10, 0), sticky="w")

    Label(volume_frame, text="Volume:", bg=BG).pack(side=LEFT, padx=(0, 5))
    volume_scale = Scale(volume_frame, from_=0, to=100, orient=HORIZONTAL,
                         length=150, bg=BG,
                         command=lambda v: set_volume(float(v)))
    volume_scale.set(70)
    volume_scale.pack(side=LEFT, padx=5)

    # Activity log
    log_frame = LabelFrame(main_frame, text="Activity Log", padx=10, pady=10, bg=BG)
    log_frame.grid(row=5, column=0, columnspan=3, sticky="nsew", pady=(10, 0))

    log_text = scrolledtext.ScrolledText(log_frame, height=6, bg="white")
    log_text.pack(fill=BOTH, expand=True)

    main_frame.rowconfigure(3, weight=1)
    main_frame.rowconfigure(5, weight=0)
    main_frame.columnconfigure(0, weight=1)
    main_frame.columnconfigure(1, weight=1)
    main_frame.columnconfigure(2, weight=1)

    log_message("Application started. Ready to connect.")


# ── Button state management ─────────────────────────────────────────────────

def update_buttons():
    """Single source of truth for button visibility based on app state."""
    if is_host:
        scan_btn.pack_forget()
        direct_connect_btn.pack_forget()
        connect_btn.pack_forget()
        disconnect_btn.pack_forget()
        connected_listbox.pack(fill=BOTH, expand=True)
    else:
        connected_listbox.pack_forget()
        if is_connected_to_host:
            scan_btn.pack_forget()
            direct_connect_btn.pack_forget()
            connect_btn.pack_forget()
            disconnect_btn.pack(side=LEFT, padx=5)
        else:
            scan_btn.pack(side=LEFT, padx=5)
            direct_connect_btn.pack(side=LEFT, padx=5)
            connect_btn.pack(side=LEFT, padx=5)
            disconnect_btn.pack_forget()


# ── UI action handlers ─────────────────────────────────────────────────────

def become_host():
    global is_host
    if not is_host:
        is_host = True
        host_btn.config(text="Be Client", command=become_client)
        update_buttons()
        ws_send("become_host", {})
        log_message("Switched to HOST mode.")


def become_client():
    global is_host
    if is_host:
        is_host = False
        host_btn.config(text="Be Host", command=become_host)
        update_buttons()
        log_message("Switched to CLIENT mode.")


def scan_devices():
    log_message("Scanning for devices via mDNS…")
    ws_send("scan_devices", {})


def direct_connect():
    dialog = simpledialog.askstring(
        "Direct Connect",
        "Enter host IP and port:\n(e.g. 192.168.1.100:9090)",
        parent=root,
    )
    if dialog:
        address = dialog.strip()
        if address:
            log_message(f"Connecting to {address}…")
            ws_send("connect_device", {"address": address})
        else:
            messagebox.showwarning("Empty input", "Please enter a valid address.")


def connect_to_host():
    selection = devices_tree.selection()
    if not selection:
        messagebox.showwarning("No selection", "Please select a device to connect to.")
        return
    values = devices_tree.item(selection[0], "values")
    address = values[3]
    ws_send("connect_device", {"address": address})


def disconnect():
    global is_host, is_connected_to_host, is_streaming, connected_devices
    is_host = False
    is_connected_to_host = False
    is_streaming = False
    connected_devices = []
    status_var.set("Disconnected")
    update_connected_list()
    update_buttons()
    log_message("Disconnected.")


def select_audio_file():
    global current_track
    filename = filedialog.askopenfilename(
        title="Select audio file",
        filetypes=[("Audio files", "*.mp3 *.wav *.flac *.ogg"), ("All files", "*.*")],
    )
    if filename:
        current_track = filename
        track_label.config(text=f"Now playing: {filename.split('/')[-1]}")
        log_message(f"Loaded: {filename.split('/')[-1]}")
        play_btn.config(state=NORMAL)
        ws_send("select_file", {"path": filename})


def toggle_playback():
    if not is_streaming:
        start_streaming()
    else:
        pause_streaming()


def start_streaming():
    global is_streaming
    is_streaming = True
    play_btn.config(text="⏸ Pause")
    progress_var.set(0)
    log_message("Streaming started.")
    ws_send("play", {})


def pause_streaming():
    global is_streaming
    is_streaming = False
    play_btn.config(text="▶ Play")
    log_message("Streaming paused.")
    ws_send("pause", {})


def stop_streaming():
    global is_streaming
    is_streaming = False
    play_btn.config(text="▶ Play")
    progress_var.set(0)
    log_message("Streaming stopped.")
    ws_send("stop", {})


def set_volume(level):
    ws_send("volume", {"level": level})


# ── Helpers ────────────────────────────────────────────────────────────────

def update_connected_list():
    if connected_listbox:
        connected_listbox.delete(0, END)
        for device in connected_devices:
            connected_listbox.insert(END, device)


def log_message(message):
    timestamp = datetime.datetime.now().strftime("%H:%M:%S")
    log_text.insert(END, f"[{timestamp}] {message}\n")
    log_text.see(END)


# ── WebSocket ──────────────────────────────────────────────────────────────

def connect_backend():
    global ws
    ws_url = f"ws://localhost:{backend_port}/ws"
    try:
        ws = websocket.create_connection(ws_url)
        log_message(f"Connected to backend at {ws_url}")
    except Exception as e:
        status_var.set("Backend connection failed")
        log_message(str(e))
        return
    threading.Thread(target=ws_listener, daemon=True).start()


def ws_send(msg_type, data):
    if ws is None:
        return
    payload = {"type": msg_type, "data": data}
    try:
        with ws_lock:
            ws.send(json.dumps(payload))
    except Exception as e:
        log_message(str(e))


def ws_listener():
    while True:
        try:
            raw = ws.recv()
            obj = json.loads(raw)
            root.after(0, lambda o=obj: handle_backend_message(o))
        except Exception as e:
            error_msg = str(e)
            root.after(0, lambda msg=error_msg: log_message(msg))
            break


def handle_backend_message(msg):
    msg_type = msg.get("Type") or msg.get("type")
    data = msg.get("Data") or msg.get("data") or {}

    match msg_type:
        case "status":
            status_var.set(data.get("message", ""))

        case "host_started":
            global is_host
            is_host = True
            status_var.set("Hosting")

        case "device_found":
            name = data.get("name", "")
            addr = data.get("address", "")
            typ = data.get("type", "")
            existing = devices_tree.get_children()
            already_listed = any(
                devices_tree.item(i)["values"][3] == addr for i in existing
            )
            if not already_listed:
                devices_tree.insert("", END, values=(name, typ, "Available", addr))

        case "connected":
            global is_connected_to_host, connected_devices
            is_connected_to_host = True
            name = data.get("name", "Remote Host")
            status_var.set(f"Connected to: {name}")
            if name not in connected_devices:
                connected_devices.append(name)
                update_connected_list()
            update_buttons()

        case "playback_started":
            global is_streaming
            is_streaming = True
            play_btn.config(text="⏸ Pause")
            progress_var.set(0)

        case "playback_paused":
            is_streaming = False
            play_btn.config(text="▶ Play")

        case "playback_stopped":
            is_streaming = False
            play_btn.config(text="▶ Play")
            progress_var.set(0)

        case "progress_update":
            pos = data.get("position", 0.0)
            tot = data.get("total", 100.0)
            val = (pos / tot * 100.0) if tot else 0.0
            progress_var.set(min(100.0, max(0.0, val)))

        case "volume_changed":
            try:
                if volume_scale:
                    volume_scale.set(int(data.get("level", 70)))
            except Exception:
                pass

        case "file_loaded":
            filename = data.get("filename", "")
            track_name = filename.replace("\\", "/").split("/")[-1]
            track_label.config(text=f"Now playing: {track_name}")
            play_btn.config(state=NORMAL)

        case "log":
            log_message(data.get("message", ""))

        case "client_connected":
            client_name = data.get("name", "Unknown client")
            client_addr = data.get("address", "")
            log_message(f"Client connected: {client_name} ({client_addr})")
            if client_name not in connected_devices:
                connected_devices.append(client_name)
                update_connected_list()

        case "client_found":
            log_message(f"Client found host: {data.get('name', '')} ({data.get('address', '')})")


# ── Entry point ────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description="Audio Amplifier UI")
    parser.add_argument("--port", type=int, default=9090,
                        help="Backend port to connect to (default: 9090)")
    args = parser.parse_args()

    global backend_port
    backend_port = args.port

    setup_ui(root)
    connect_backend()


if __name__ == "__main__":
    main()
    root.mainloop()
