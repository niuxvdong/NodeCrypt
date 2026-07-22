package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx             context.Context
	assets          fs.FS
	server          *ChatServer
	discovery       *DiscoveryService
	config          desktopConfig
	configDir       string
	mu              sync.RWMutex
	quitting        bool
	networkStatus   NetworkStatus
	networkStatusAt time.Time
}

type desktopConfig struct {
	NodeID      string             `json:"nodeId"`
	NodeName    string             `json:"nodeName"`
	RecentRooms []recentRoomRecord `json:"recentRooms,omitempty"`
}

type recentRoomRecord struct {
	ID                string `json:"id"`
	NodeURL           string `json:"nodeUrl"`
	UserName          string `json:"userName"`
	RoomName          string `json:"roomName"`
	ProtectedPassword string `json:"protectedPassword,omitempty"`
	UpdatedAt         int64  `json:"updatedAt"`
}

type RecentRoomInfo struct {
	ID          string `json:"id"`
	NodeURL     string `json:"nodeUrl"`
	UserName    string `json:"userName"`
	RoomName    string `json:"roomName"`
	HasPassword bool   `json:"hasPassword"`
	UpdatedAt   int64  `json:"updatedAt"`
}

type RecentRoomCredentials struct {
	Found    bool   `json:"found"`
	NodeURL  string `json:"nodeUrl"`
	UserName string `json:"userName"`
	RoomName string `json:"roomName"`
	Password string `json:"password"`
}

func NewApp(assets fs.FS) *App {
	return &App{assets: assets}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	configRoot, err := os.UserConfigDir()
	if err != nil {
		configRoot = "."
	}
	a.configDir = filepath.Join(configRoot, "NodeCrypt Desktop")
	_ = os.MkdirAll(a.configDir, 0o700)
	a.loadConfig()

	server, err := StartChatServer(a.assets, filepath.Join(a.configDir, "nodecrypt.sqlite"))
	if err != nil {
		runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
			Type:    runtime.ErrorDialog,
			Title:   "NodeCrypt Desktop",
			Message: "本地节点启动失败: " + err.Error(),
		})
		return
	}
	a.server = server
	a.discovery = StartDiscovery(a.config.NodeID, a.config.NodeName, server.Port())
}

func (a *App) shutdown(_ context.Context) {
	stopDesktopTray()
	if a.discovery != nil {
		a.discovery.Close()
	}
	if a.server != nil {
		a.server.Close()
	}
}

func (a *App) showWindow() {
	if a.ctx == nil {
		return
	}
	runtime.WindowShow(a.ctx)
	runtime.WindowUnminimise(a.ctx)
}

func (a *App) HideToBackground() {
	if a.ctx != nil {
		runtime.WindowHide(a.ctx)
	}
}

func (a *App) requestClose(ctx context.Context) bool {
	a.mu.RLock()
	quitting := a.quitting
	a.mu.RUnlock()
	if quitting {
		return false
	}
	answer, err := runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
		Type:          runtime.QuestionDialog,
		Title:         "NodeCrypt Desktop",
		Message:       "是否在后台继续运行？\n\n选择“是”：隐藏窗口，继续提供局域网节点和聊天服务。\n选择“否”：完全退出程序。",
		DefaultButton: "Yes",
	})
	if err != nil || answer == "Yes" {
		runtime.WindowHide(ctx)
		return true
	}
	a.mu.Lock()
	a.quitting = true
	a.mu.Unlock()
	return false
}

func (a *App) loadConfig() {
	data, err := os.ReadFile(filepath.Join(a.configDir, "desktop.json"))
	if err == nil {
		_ = json.Unmarshal(data, &a.config)
	}
	if a.config.NodeID == "" {
		buffer := make([]byte, 16)
		_, _ = rand.Read(buffer)
		a.config.NodeID = hex.EncodeToString(buffer)
	}
	if strings.TrimSpace(a.config.NodeName) == "" {
		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "NodeCrypt"
		}
		a.config.NodeName = hostname
	}
	a.saveConfig()
}

func (a *App) saveConfig() {
	data, _ := json.MarshalIndent(a.config, "", "  ")
	_ = os.WriteFile(filepath.Join(a.configDir, "desktop.json"), data, 0o600)
}

func (a *App) ListNodes() []NodeInfo {
	if a.server == nil {
		return nil
	}
	a.mu.RLock()
	nodeID := a.config.NodeID
	nodeName := a.config.NodeName
	a.mu.RUnlock()
	addresses := localIPv4Addresses()
	if len(addresses) == 0 {
		addresses = []string{"127.0.0.1"}
	}
	nodes := []NodeInfo{{
		ID:        nodeID,
		Name:      nodeName,
		URL:       fmt.Sprintf("http://127.0.0.1:%d", a.server.Port()),
		Address:   addresses[0],
		Addresses: addresses,
		Port:      a.server.Port(),
		Local:     true,
	}}
	if a.discovery != nil {
		nodes = append(nodes, a.discovery.Nodes()...)
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Local != nodes[j].Local {
			return nodes[i].Local
		}
		return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
	})
	return nodes
}

func localIPv4Addresses() []string {
	if addresses, handled := platformLocalIPv4Addresses(); handled {
		return addresses
	}
	return genericLocalIPv4Addresses()
}

func genericLocalIPv4Addresses() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	addresses := make([]string, 0)
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 || networkInterface.Flags&net.FlagLoopback != 0 {
			continue
		}
		interfaceAddresses, err := networkInterface.Addrs()
		if err != nil {
			continue
		}
		for _, interfaceAddress := range interfaceAddresses {
			ip, _, err := net.ParseCIDR(interfaceAddress.String())
			if err != nil || !isUsableLocalIPv4(ip) {
				continue
			}
			address := ip.To4().String()
			if !seen[address] {
				seen[address] = true
				addresses = append(addresses, address)
			}
		}
	}
	sort.SliceStable(addresses, func(i, j int) bool {
		left := net.ParseIP(addresses[i])
		right := net.ParseIP(addresses[j])
		if left.IsPrivate() != right.IsPrivate() {
			return left.IsPrivate()
		}
		return addresses[i] < addresses[j]
	})
	return addresses
}

func isUsableLocalIPv4(ip net.IP) bool {
	return ip != nil && ip.To4() != nil && !ip.IsLoopback() && !ip.IsUnspecified() &&
		!ip.IsMulticast() && !ip.IsLinkLocalUnicast()
}

func (a *App) SetNodeName(name string) bool {
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 2 || len([]rune(name)) > 32 {
		return false
	}
	a.mu.Lock()
	a.config.NodeName = name
	a.saveConfig()
	a.mu.Unlock()
	if a.discovery != nil {
		a.discovery.SetName(name)
	}
	return true
}

func (a *App) RefreshDiscovery() {
	if a.discovery != nil {
		a.discovery.Announce()
	}
}

func (a *App) GetNetworkStatus() NetworkStatus {
	a.mu.RLock()
	if !a.networkStatusAt.IsZero() && time.Since(a.networkStatusAt) < 30*time.Second {
		status := a.networkStatus
		a.mu.RUnlock()
		return status
	}
	a.mu.RUnlock()
	addresses := localIPv4Addresses()
	primaryIP := ""
	if len(addresses) > 0 {
		primaryIP = addresses[0]
	}
	status := queryWindowsNetworkStatus(primaryIP)
	status.SystemDiscoveryEnabled = a.discovery != nil && a.discovery.SystemDiscoveryEnabled()
	a.mu.Lock()
	a.networkStatus = status
	a.networkStatusAt = time.Now()
	a.mu.Unlock()
	return status
}

// StoreLocalHistory keeps an encrypted copy in the SQLite database owned by this EXE.
func (a *App) StoreLocalHistory(channel, token string, version int64, messageID string, timestamp int64, nonce, ciphertext string) bool {
	if a.server == nil || channel == "" || len(channel) > 512 || version != 1 || messageID == "" ||
		len(messageID) > 128 || timestamp <= 0 || nonce == "" || len(nonce) > 64 || ciphertext == "" {
		return false
	}
	payload := map[string]any{
		"v":  version,
		"i":  messageID,
		"ts": timestamp,
		"n":  nonce,
		"c":  ciphertext,
	}
	return a.server.storeHistory(channel, token, historyEnvelope{
		Version: version, MessageID: messageID, Timestamp: timestamp,
		Nonce: nonce, Cipher: ciphertext, Payload: payload,
	})
}

// LoadLocalHistory reads only encrypted envelopes; plaintext never crosses into Go.
func (a *App) LoadLocalHistory(channel, token string, before int64, limit int) HistoryPage {
	if a.server == nil || channel == "" || len(channel) > 512 {
		return HistoryPage{Messages: []map[string]any{}, Status: "unavailable"}
	}
	return a.server.loadHistory(channel, token, before, limit)
}

func (a *App) SaveRecentRoom(nodeURL, userName, roomName, password string) bool {
	nodeURL = strings.TrimSpace(nodeURL)
	userName = strings.TrimSpace(userName)
	roomName = strings.TrimSpace(roomName)
	if len([]rune(userName)) < 1 || len([]rune(userName)) > 20 ||
		len([]rune(roomName)) < 1 || len([]rune(roomName)) > 15 || len([]rune(password)) > 15 {
		return false
	}
	normalised, ok := normaliseNodeAddress(nodeURL)
	if !ok {
		return false
	}
	parsed, err := url.Parse(normalised)
	if err != nil {
		return false
	}
	nodeOrigin := (&url.URL{Scheme: "http", Host: parsed.Host}).String()
	protectedPassword, err := protectRecentRoomSecret(password)
	if err != nil {
		return false
	}
	digest := sha256.Sum256([]byte(strings.ToLower(nodeOrigin) + "\x00" + strings.ToLower(userName) + "\x00" + roomName))
	record := recentRoomRecord{
		ID: hex.EncodeToString(digest[:16]), NodeURL: nodeOrigin, UserName: userName,
		RoomName: roomName, ProtectedPassword: protectedPassword, UpdatedAt: time.Now().UnixMilli(),
	}
	a.mu.Lock()
	rooms := make([]recentRoomRecord, 0, 12)
	rooms = append(rooms, record)
	for _, existing := range a.config.RecentRooms {
		if existing.ID != record.ID && len(rooms) < 12 {
			rooms = append(rooms, existing)
		}
	}
	a.config.RecentRooms = rooms
	a.saveConfig()
	a.mu.Unlock()
	return true
}

func (a *App) ListRecentRooms() []RecentRoomInfo {
	a.mu.RLock()
	records := append([]recentRoomRecord(nil), a.config.RecentRooms...)
	a.mu.RUnlock()
	result := make([]RecentRoomInfo, 0, len(records))
	for _, record := range records {
		result = append(result, RecentRoomInfo{
			ID: record.ID, NodeURL: record.NodeURL, UserName: record.UserName,
			RoomName: record.RoomName, HasPassword: record.ProtectedPassword != "", UpdatedAt: record.UpdatedAt,
		})
	}
	return result
}

func (a *App) GetRecentRoom(id string) RecentRoomCredentials {
	a.mu.RLock()
	var selected *recentRoomRecord
	for index := range a.config.RecentRooms {
		if a.config.RecentRooms[index].ID == id {
			copy := a.config.RecentRooms[index]
			selected = &copy
			break
		}
	}
	a.mu.RUnlock()
	if selected == nil {
		return RecentRoomCredentials{}
	}
	password, err := unprotectRecentRoomSecret(selected.ProtectedPassword)
	if err != nil {
		return RecentRoomCredentials{}
	}
	return RecentRoomCredentials{
		Found: true, NodeURL: selected.NodeURL, UserName: selected.UserName,
		RoomName: selected.RoomName, Password: password,
	}
}

func (a *App) OpenRecentRoom(id string) bool {
	a.mu.RLock()
	var nodeURL string
	for _, record := range a.config.RecentRooms {
		if record.ID == id {
			nodeURL = record.NodeURL
			break
		}
	}
	a.mu.RUnlock()
	if nodeURL == "" {
		return false
	}
	parsed, err := url.Parse(nodeURL)
	if err != nil {
		return false
	}
	parameters := parsed.Query()
	parameters.Set("_recent", id)
	parsed.RawQuery = parameters.Encode()
	return a.openChat(parsed.String())
}

func (a *App) DeleteRecentRoom(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for index, record := range a.config.RecentRooms {
		if record.ID == id {
			a.config.RecentRooms = append(a.config.RecentRooms[:index], a.config.RecentRooms[index+1:]...)
			a.saveConfig()
			return true
		}
	}
	return false
}

func (a *App) ConfigureFirewall() bool {
	configured := requestFirewallConfiguration()
	a.invalidateNetworkStatus()
	return configured
}

func (a *App) RemoveFirewall() bool {
	removed := requestFirewallRemoval()
	a.invalidateNetworkStatus()
	return removed
}

func (a *App) invalidateNetworkStatus() {
	a.mu.Lock()
	a.networkStatusAt = time.Time{}
	a.mu.Unlock()
}

func (a *App) ConnectToNode(url string) bool {
	for _, node := range a.ListNodes() {
		if node.URL == url {
			reachableURL, ok := findReachableNodeURL(nodeCandidateURLs(node))
			return ok && a.openChat(reachableURL)
		}
	}
	return false
}

func (a *App) ConnectToAddress(address string) bool {
	target, ok := normaliseNodeAddress(address)
	if !ok || a.ctx == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	if !probeNodeEndpoint(ctx, target) {
		return false
	}
	return a.openChat(target)
}

func nodeCandidateURLs(node NodeInfo) []string {
	seen := make(map[string]bool)
	candidates := make([]string, 0, len(node.Addresses)+1)
	add := func(candidate string) {
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
			return
		}
		origin := (&url.URL{Scheme: "http", Host: parsed.Host}).String()
		if !seen[origin] {
			seen[origin] = true
			candidates = append(candidates, origin)
		}
	}
	add(node.URL)
	for _, address := range node.Addresses {
		if ip := net.ParseIP(address); ip != nil && ip.To4() != nil && node.Port > 0 {
			add("http://" + net.JoinHostPort(ip.To4().String(), strconv.Itoa(node.Port)))
		}
	}
	return candidates
}

func findReachableNodeURL(candidates []string) (string, bool) {
	if len(candidates) == 0 {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	results := make(chan string, len(candidates))
	for _, candidate := range candidates {
		go func(candidate string) {
			if probeNodeEndpoint(ctx, candidate) {
				results <- candidate
				return
			}
			results <- ""
		}(candidate)
	}
	for range candidates {
		select {
		case candidate := <-results:
			if candidate != "" {
				return candidate, true
			}
		case <-ctx.Done():
			return "", false
		}
	}
	return "", false
}

func probeNodeEndpoint(ctx context.Context, endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
		return false
	}
	parsed.Path = "/api/runtime"
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return false
	}
	dialer := &net.Dialer{Timeout: 1200 * time.Millisecond}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			DisableKeepAlives:     true,
			ResponseHeaderTimeout: 1200 * time.Millisecond,
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	var runtimeInfo struct {
		Mode string `json:"mode"`
	}
	return json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&runtimeInfo) == nil && runtimeInfo.Mode == "desktop"
}

func (a *App) openChat(target string) bool {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Host == "" || a.ctx == nil {
		return false
	}
	endpoint := &url.URL{Scheme: "http", Host: parsed.Host}
	parameters := parsed.Query()
	parameters.Set("_node", endpoint.String())
	destination := "http://wails.localhost/chat/?" + parameters.Encode()
	runtime.WindowExecJS(a.ctx, fmt.Sprintf("window.location.href=%q", destination))
	return true
}

func normaliseNodeAddress(address string) (string, bool) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", false
	}
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	target, err := url.Parse(address)
	if err != nil || strings.ToLower(target.Scheme) != "http" || target.User != nil {
		return "", false
	}
	ip := net.ParseIP(target.Hostname())
	if ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsMulticast() {
		return "", false
	}
	portText := target.Port()
	if portText == "" {
		portText = strconv.Itoa(preferredChatPort)
		target.Host = net.JoinHostPort(ip.To4().String(), portText)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", false
	}
	if target.Path == "" {
		target.Path = "/"
	}
	target.Scheme = "http"
	return target.String(), true
}

func (a *App) ConnectLocal() {
	if a.server != nil {
		a.ConnectToNode(fmt.Sprintf("http://127.0.0.1:%d", a.server.Port()))
	}
}

func (a *App) ShowDiscovery() {
	if a.ctx != nil {
		runtime.WindowExecJS(a.ctx, `window.location.href="http://wails.localhost/"`)
	}
}

func (a *App) Quit() {
	if a.ctx != nil {
		a.mu.Lock()
		a.quitting = true
		a.mu.Unlock()
		runtime.Quit(a.ctx)
	}
}
