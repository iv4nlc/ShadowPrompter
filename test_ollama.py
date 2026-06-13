import requests
import sys

if len(sys.argv) != 2:
    print("Usage: python test_ollama.py <ip_address>")
    sys.exit(1)
    
ip = sys.argv[1]
url = f"http://{ip}:11434/api/chat"

payload = {
    "model": "qwen2.5-coder:7b",
    "messages": [
        {"role": "user", "content": "hi!"}
    ],
    "stream": False
}

response = requests.post(url, json=payload)

if response.status_code == 200:
    data = response.json()
    print(data["message"]["content"])
else:
    print("Error:", response.status_code, response.text)