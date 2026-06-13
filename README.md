# ShadowPrompter

**ShadowPrompter** is a research framework for studying **LLM-orchestrated command-and-control (C2) workflows** in controlled laboratory environments.

The project explores how Large Language Models (LLMs) can be used as an orchestration layer inside malware-like C2 architectures. Instead of sending only predefined commands, ShadowPrompter studies scenarios where prompts are sent to an LLM and the model generates the action to be executed by a controlled client.

> **Disclaimer**
>
> This project is intended only for academic, defensive, and controlled cybersecurity research.  
> It must be used exclusively in isolated lab environments and never against third-party systems.

---

## What is ShadowPrompter?

ShadowPrompter is composed of two main components:

- **Client:** implemented in Go, designed to run on controlled Windows or Linux machines.
- **Server:** implemented in Python, providing the C2-like control logic and a web interface.

The server manages connected clients, sends prompts, receives results, stores execution history, and allows switching between different LLM inference backends.

The client receives prompts, sends them to the selected LLM backend, executes the generated response inside the lab environment, and returns the output to the server.

---

## Supported Scenarios

ShadowPrompter includes several experimental scenarios:

### 1. Remote LLM API with Dynamic Prompts

The server sends prompts dynamically to the client.  
The client forwards them to a remote LLM API and executes the model-generated response.

### 2. Remote LLM API with Embedded Prompts

The client contains predefined prompts and uses a remote LLM API to process them.  
This scenario studies more autonomous prompt-driven workflows.

### 3. Local LLM with Dynamic Prompts

The server sends prompts to the client, but inference is performed locally using an Ollama-based LLM service.

### 4. Local LLM with Embedded Prompts

The client contains predefined prompts and processes them using a local LLM.

### 5. LLM Master Scenario

One controlled client acts as an **LLM Master**, exposing a local model to other clients in the internal lab network.

### 6. Integrated Adaptive Mode

A unified mode that allows switching at runtime between:

- Remote API inference.
- Local client-side inference.
- LLM Master inference.

This is the recommended mode for comparing different inference strategies.

---

## Project Setup

Before running ShadowPrompter, the Python environment, cryptographic keys, configuration files, and Go client binaries must be prepared.

### 1. Set Up the Python Environment

Create a Python virtual environment for the server and utility scripts, including `generateKeys.py`:

```bash
python -m venv venv
```

Activate the virtual environment.

On Linux:

```bash
source venv/bin/activate
```

On Windows PowerShell:

```powershell
.\venv\Scripts\Activate.ps1
```

On Windows CMD:

```cmd
venv\Scripts\activate.bat
```

Then install the required dependencies:

```bash
pip install -r requirements.txt
```

### 2. Generate Cryptographic Keys

The project includes a `generateKeys.py` script used to generate the cryptographic keys required by the framework.

Run it after installing the Python dependencies:

```bash
python generateKeys.py
```

After generating the keys, place them where required by the project:

- The **server private key** must be placed in the root directory where `server.py` is located.
- The **server public key** must be hardcoded in `client.go`.

These keys are used for secure client-server communication and prompt authentication.

### 3. Configure `config.json`

For the **Remote API** scenario, the `config.json` file is mandatory and must be created before running the server.

This file stores the remote API configuration, including the API key and model name.

For the rest of the scenarios, configuration files are created automatically at runtime if they do not already exist. After that, they can be modified directly through the web interface as the corresponding actions are performed.

### 4. Compile the Go Client

The project includes a `compile.sh` file with the Go build commands required to compile the client for different target operating systems.

You can either copy and run the specific command you need from `compile.sh`, depending on the target OS, or execute the full script to compile both Windows and Linux client binaries.

```bash
./compile.sh
```

Running the full script will generate both executables.

The generated client binary should only be executed inside authorized laboratory systems.

---

## How to Use

1. Set up an isolated lab environment with controlled virtual machines.
2. Create and activate the Python virtual environment.
3. Install the server and utility dependencies.
4. Generate the required keys.
5. Place the server private key next to `server.py`.
6. Hardcode the server public key in `client.go`.
7. Create the required `config.json` file if using the Remote API scenario.
8. Compile the Go client for the target operating system using the relevant command from `compile.sh`.
9. Start the Python server and open the web interface.
10. Launch the compiled Go client on one or more lab machines.
11. Select the desired inference backend:
    - Remote API.
    - Local Ollama.
    - LLM Master.
12. Send prompts from the web panel.
13. Review the generated command, execution output, and client history.

All experiments should use benign test data and controlled systems only.

---

## Main Features

- Cross-platform client for Windows and Linux.
- Python server with web-based control panel.
- Remote and local LLM inference support.
- Dynamic and embedded prompt scenarios.
- LLM Master mode for shared internal inference.
- Integrated adaptive mode for runtime backend switching.
- Key generation through `generateKeys.py`.
- Configuration through `config.json`.
- Go client compilation commands through `compile.sh`.
- Client metadata collection.
- Per-client execution history.
- Encrypted client-server communication.
- Prompt authentication and validation.

---

## Defensive Research Goals

ShadowPrompter is designed to help defenders study:

- How LLM-mediated C2 workflows behave.
- How remote and local inference change the detection surface.
- What artifacts are produced by prompt-driven execution.
- How AI-provider traffic can be identified.
- How local model execution appears on endpoints.
- How internal LLM Master traffic may be detected.
- How prompt-generated commands differ across models and operating systems.

---

## Author

**Iván Llorente Cano**  
Master's Degree in Cybersecurity  
Universidad Carlos III de Madrid  
2025–2026

---

## License

This project is part of an academic research work.  
Use it only for authorized, ethical, and controlled experimentation.