package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	_ "modernc.org/sqlite"
)

const (
	preferredChatPort       = 8788
	historyPageSize         = 50
	historyMaxMessageBytes  = 5 * 1024 * 1024
	historyMaxResponseBytes = 6 * 1024 * 1024
	historyLimitPerRoom     = 5000
	maxWebSocketMessage     = 8 * 1024 * 1024
)

type ChatServer struct {
	assets     fs.FS
	db         *sql.DB
	privateKey *rsa.PrivateKey
	publicKey  string
	listener   net.Listener
	httpServer *http.Server

	mu       sync.RWMutex
	clients  map[string]*chatClient
	channels map[string][]string

	closeOnce sync.Once
}

type chatClient struct {
	id             string
	connection     *websocket.Conn
	writeMu        sync.Mutex
	shared         []byte
	channel        string
	historyChannel string
	userClaim      string
	seen           time.Time
}

type historyEnvelope struct {
	Version   int64
	MessageID string
	Timestamp int64
	Nonce     string
	Cipher    string
	Payload   map[string]any
}

type HistoryPage struct {
	Messages []map[string]any `json:"m"`
	Before   *int64           `json:"b"`
	HasMore  bool             `json:"x"`
	Status   string           `json:"r"`
}

type outboundMessage struct {
	client *chatClient
	key    []byte
	body   map[string]any
}

var websocketUpgrader = websocket.Upgrader{
	HandshakeTimeout: 8 * time.Second,
	ReadBufferSize:   4096,
	WriteBufferSize:  4096,
	CheckOrigin: func(request *http.Request) bool {
		origin := request.Header.Get("Origin")
		if origin == "" {
			return true
		}
		return origin == "http://"+request.Host || origin == "https://"+request.Host || origin == "http://wails.localhost"
	},
}

func StartChatServer(assets fs.FS, databasePath string) (*ChatServer, error) {
	if err := ensureDirectory(databasePath); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := initialiseDatabase(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("generate RSA key: %w", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("encode RSA key: %w", err)
	}

	listener, err := listenChatPort()
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("listen: %w", err)
	}

	server := &ChatServer{
		assets:     assets,
		db:         db,
		privateKey: privateKey,
		publicKey:  base64.StdEncoding.EncodeToString(publicDER),
		listener:   listener,
		clients:    make(map[string]*chatClient),
		channels:   make(map[string][]string),
	}
	server.httpServer = &http.Server{
		Handler:           server,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 * 1024,
	}
	go func() {
		if serveErr := server.httpServer.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Printf("NodeCrypt desktop server stopped: %v", serveErr)
		}
	}()
	return server, nil
}

func listenChatPort() (net.Listener, error) {
	for port := preferredChatPort; port < preferredChatPort+20; port++ {
		listener, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", port))
		if err == nil {
			return listener, nil
		}
	}
	return net.Listen("tcp4", "0.0.0.0:0")
}

func ensureDirectory(filename string) error {
	directory := filepath.Dir(filename)
	if directory == "" {
		return errors.New("invalid database path")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	return nil
}

func initialiseDatabase(db *sql.DB) error {
	const pragmas = `
		PRAGMA journal_mode = WAL;
		PRAGMA synchronous = NORMAL;
		PRAGMA foreign_keys = ON;
	`
	if _, err := db.Exec(pragmas); err != nil {
		return fmt.Errorf("configure database: %w", err)
	}
	hasMessages, err := databaseTableExists(db, "messages")
	if err != nil {
		return fmt.Errorf("inspect database: %w", err)
	}
	if hasMessages {
		hasAuthToken, columnErr := databaseColumnExists(db, "messages", "auth_token")
		if columnErr != nil {
			return fmt.Errorf("inspect message schema: %w", columnErr)
		}
		if !hasAuthToken {
			if migrationErr := migrateHistorySchemaV2(db); migrationErr != nil {
				return migrationErr
			}
		}
	}
	const schema = `
		CREATE TABLE IF NOT EXISTS rooms (
			channel TEXT NOT NULL,
			auth_token TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (channel, auth_token)
		);
		CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel TEXT NOT NULL,
			auth_token TEXT NOT NULL,
			message_id TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			payload TEXT NOT NULL,
			UNIQUE(channel, auth_token, message_id)
		);
		CREATE INDEX IF NOT EXISTS idx_messages_room_id ON messages(channel, auth_token, id DESC);
		PRAGMA user_version = 2;
	`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("initialise database: %w", err)
	}
	return nil
}

func databaseTableExists(db *sql.DB, table string) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count)
	return count > 0, err
}

func databaseColumnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var index, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&index, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func migrateHistorySchemaV2(db *sql.DB) error {
	const migration = `
		BEGIN IMMEDIATE;
		CREATE TABLE rooms_v2 (
			channel TEXT NOT NULL,
			auth_token TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (channel, auth_token)
		);
		INSERT OR IGNORE INTO rooms_v2 (channel, auth_token, created_at)
			SELECT channel, auth_token, created_at FROM rooms;
		CREATE TABLE messages_v2 (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel TEXT NOT NULL,
			auth_token TEXT NOT NULL,
			message_id TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			payload TEXT NOT NULL,
			UNIQUE(channel, auth_token, message_id)
		);
		INSERT OR IGNORE INTO messages_v2 (id, channel, auth_token, message_id, created_at, payload)
			SELECT messages.id, messages.channel, rooms.auth_token, messages.message_id, messages.created_at, messages.payload
			FROM messages INNER JOIN rooms ON rooms.channel = messages.channel;
		DROP TABLE messages;
		DROP TABLE rooms;
		ALTER TABLE rooms_v2 RENAME TO rooms;
		ALTER TABLE messages_v2 RENAME TO messages;
		CREATE INDEX idx_messages_room_id ON messages(channel, auth_token, id DESC);
		PRAGMA user_version = 2;
		COMMIT;
	`
	if _, err := db.Exec(migration); err != nil {
		_, _ = db.Exec(`ROLLBACK`)
		return fmt.Errorf("migrate history database: %w", err)
	}
	return nil
}

func (s *ChatServer) Port() int {
	if s == nil || s.listener == nil {
		return 0
	}
	return s.listener.Addr().(*net.TCPAddr).Port
}

func (s *ChatServer) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		s.mu.RLock()
		clients := make([]*chatClient, 0, len(s.clients))
		for _, client := range s.clients {
			clients = append(clients, client)
		}
		s.mu.RUnlock()
		for _, client := range clients {
			_ = client.connection.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.httpServer.Shutdown(ctx)
		_ = s.db.Close()
	})
}

func (s *ChatServer) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if websocket.IsWebSocketUpgrade(request) {
		s.handleWebSocket(response, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/api/") {
		s.handleAPI(response, request)
		return
	}
	s.serveAsset(response, request)
}

func (s *ChatServer) handleAPI(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/api/runtime" && request.Method == http.MethodGet {
		writeJSON(response, http.StatusOK, map[string]any{"authRequired": false, "mode": "desktop"})
		return
	}
	writeJSON(response, http.StatusNotFound, map[string]any{"error": "not_found"})
}

func writeJSON(response http.ResponseWriter, status int, body any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(body)
}

func (s *ChatServer) serveAsset(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		response.Header().Set("Allow", "GET, HEAD")
		response.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	assetName := strings.TrimPrefix(path.Clean("/"+request.URL.Path), "/")
	if assetName == "" || assetName == "." {
		assetName = "index.html"
	}
	if !fs.ValidPath(assetName) {
		response.WriteHeader(http.StatusBadRequest)
		return
	}
	body, err := fs.ReadFile(s.assets, assetName)
	if err != nil {
		assetName = "index.html"
		body, err = fs.ReadFile(s.assets, assetName)
	}
	if err != nil {
		http.Error(response, "Frontend assets are unavailable.", http.StatusServiceUnavailable)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(assetName))
	if contentType == "" {
		contentType = http.DetectContentType(body)
	}
	response.Header().Set("Content-Type", contentType)
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Referrer-Policy", "same-origin")
	if assetName == "index.html" {
		response.Header().Set("Cache-Control", "no-cache")
	} else {
		response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	response.Header().Set("Content-Length", strconv.Itoa(len(body)))
	response.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = response.Write(body)
	}
}

func (s *ChatServer) handleWebSocket(response http.ResponseWriter, request *http.Request) {
	connection, err := websocketUpgrader.Upgrade(response, request, nil)
	if err != nil {
		return
	}
	connection.SetReadLimit(maxWebSocketMessage)

	client := &chatClient{
		id:         s.newClientID(),
		connection: connection,
		seen:       time.Now(),
	}
	s.removeStaleClients()
	s.mu.Lock()
	s.clients[client.id] = client
	s.mu.Unlock()
	defer s.disconnectClient(client)

	keyMessage, _ := json.Marshal(map[string]any{"type": "server-key", "key": s.publicKey})
	if err := client.writeText(keyMessage); err != nil {
		return
	}
	for {
		messageType, message, err := connection.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.TextMessage {
			continue
		}
		s.mu.Lock()
		if current := s.clients[client.id]; current != nil {
			current.seen = time.Now()
		}
		shared := append([]byte(nil), client.shared...)
		s.mu.Unlock()

		if string(message) == "ping" {
			_ = client.writeText([]byte("pong"))
			continue
		}
		if len(shared) == 0 {
			if len(message) < 2048 {
				if err := s.establishTransport(client, message); err != nil {
					return
				}
			}
			continue
		}
		if len(message) <= maxWebSocketMessage {
			s.processEncryptedMessage(client, string(message), shared)
		}
	}
}

func (client *chatClient) writeText(message []byte) error {
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	_ = client.connection.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return client.connection.WriteMessage(websocket.TextMessage, message)
}

func (s *ChatServer) newClientID() string {
	for {
		buffer := make([]byte, 8)
		if _, err := rand.Read(buffer); err != nil {
			panic(err)
		}
		id := hex.EncodeToString(buffer)
		s.mu.RLock()
		_, exists := s.clients[id]
		s.mu.RUnlock()
		if !exists {
			return id
		}
	}
}

func (s *ChatServer) removeStaleClients() {
	threshold := time.Now().Add(-60 * time.Second)
	s.mu.RLock()
	stale := make([]*chatClient, 0)
	for _, client := range s.clients {
		if client.seen.Before(threshold) {
			stale = append(stale, client)
		}
	}
	s.mu.RUnlock()
	for _, client := range stale {
		_ = client.connection.Close()
	}
}

func (s *ChatServer) establishTransport(client *chatClient, peerHex []byte) error {
	peerBytes, err := hex.DecodeString(string(peerHex))
	if err != nil {
		return err
	}
	peerPublic, err := ecdh.P384().NewPublicKey(peerBytes)
	if err != nil {
		return err
	}
	privateKey, err := ecdh.P384().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	shared, err := privateKey.ECDH(peerPublic)
	if err != nil || len(shared) < 40 {
		return errors.New("ECDH key agreement failed")
	}
	publicKey := privateKey.PublicKey().Bytes()
	hash := sha256.Sum256(publicKey)
	signature, err := rsa.SignPKCS1v15(rand.Reader, s.privateKey, crypto.SHA256, hash[:])
	if err != nil {
		return err
	}

	s.mu.Lock()
	if s.clients[client.id] == nil {
		s.mu.Unlock()
		return errors.New("client disconnected")
	}
	client.shared = append([]byte(nil), shared[8:40]...)
	s.mu.Unlock()
	response := hex.EncodeToString(publicKey) + "|" + base64.StdEncoding.EncodeToString(signature)
	return client.writeText([]byte(response))
}

func (s *ChatServer) processEncryptedMessage(client *chatClient, message string, shared []byte) {
	decrypted, err := decryptTransport(message, shared)
	if err != nil {
		return
	}
	action, ok := nonEmptyString(decrypted["a"])
	if !ok {
		return
	}
	switch action {
	case "j":
		s.handleJoinChannel(client, decrypted)
	case "c":
		s.handleClientMessage(client, decrypted)
	case "w":
		s.handleChannelMessage(client, decrypted)
	case "s":
		s.handleStoreHistory(client, decrypted)
	case "h":
		s.handleHistoryRequest(client, decrypted)
	}
}

func (s *ChatServer) handleJoinChannel(client *chatClient, decrypted map[string]any) {
	historyChannel, ok := nonEmptyString(decrypted["p"])
	if !ok || len(historyChannel) > 512 {
		return
	}
	channel := historyChannel
	if roomScope, scopeOK := nonEmptyString(decrypted["g"]); scopeOK && len(roomScope) >= 32 && len(roomScope) <= 128 {
		channel += ":" + roomScope
	}
	userClaim, claimOK := nonEmptyString(decrypted["u"])
	if !claimOK || len(userClaim) < 32 || len(userClaim) > 128 {
		// Older clients did not send an opaque username claim.
		userClaim = client.id
	}
	s.mu.Lock()
	if s.clients[client.id] == nil || client.channel != "" {
		s.mu.Unlock()
		return
	}
	for _, memberID := range s.channels[channel] {
		member := s.clients[memberID]
		if member != nil && member.userClaim == userClaim {
			shared := append([]byte(nil), client.shared...)
			s.mu.Unlock()
			s.sendEncrypted(client, shared, map[string]any{
				"a": "j",
				"p": map[string]any{"o": false, "r": "username_taken"},
			})
			return
		}
	}
	client.channel = channel
	client.historyChannel = historyChannel
	client.userClaim = userClaim
	s.channels[channel] = append(s.channels[channel], client.id)
	shared := append([]byte(nil), client.shared...)
	s.mu.Unlock()
	s.sendEncrypted(client, shared, map[string]any{
		"a": "j",
		"p": map[string]any{"o": true},
	})
	s.broadcastMemberList(channel)
}

func (s *ChatServer) handleClientMessage(client *chatClient, decrypted map[string]any) {
	payload, payloadOK := nonEmptyString(decrypted["p"])
	targetID, targetOK := nonEmptyString(decrypted["c"])
	if !payloadOK || !targetOK {
		return
	}
	s.mu.RLock()
	target := s.clients[targetID]
	channel := client.channel
	valid := channel != "" && target != nil && target.channel == channel && len(target.shared) == 32
	var targetKey []byte
	if valid {
		targetKey = append([]byte(nil), target.shared...)
	}
	s.mu.RUnlock()
	if valid {
		s.sendEncrypted(target, targetKey, map[string]any{"a": "c", "p": payload, "c": client.id})
	}
}

func (s *ChatServer) handleChannelMessage(client *chatClient, decrypted map[string]any) {
	payloads, ok := decrypted["p"].(map[string]any)
	if !ok {
		return
	}
	s.mu.RLock()
	channel := client.channel
	messages := make([]outboundMessage, 0, len(payloads))
	if channel != "" {
		for targetID, value := range payloads {
			payload, payloadOK := nonEmptyString(value)
			target := s.clients[targetID]
			if !payloadOK || target == nil || target.channel != channel || len(target.shared) != 32 {
				continue
			}
			messages = append(messages, outboundMessage{
				client: target,
				key:    append([]byte(nil), target.shared...),
				body:   map[string]any{"a": "c", "p": payload, "c": client.id},
			})
		}
	}
	s.mu.RUnlock()
	for _, message := range messages {
		s.sendEncrypted(message.client, message.key, message.body)
	}
}

func (s *ChatServer) broadcastMemberList(channel string) {
	s.mu.RLock()
	members := append([]string(nil), s.channels[channel]...)
	messages := make([]outboundMessage, 0, len(members))
	for _, memberID := range members {
		client := s.clients[memberID]
		if client == nil || client.channel != channel || len(client.shared) != 32 {
			continue
		}
		others := make([]string, 0, len(members)-1)
		for _, otherID := range members {
			if otherID != memberID {
				others = append(others, otherID)
			}
		}
		messages = append(messages, outboundMessage{
			client: client,
			key:    append([]byte(nil), client.shared...),
			body:   map[string]any{"a": "l", "p": others},
		})
	}
	s.mu.RUnlock()
	for _, message := range messages {
		s.sendEncrypted(message.client, message.key, message.body)
	}
}

func (s *ChatServer) disconnectClient(client *chatClient) {
	s.mu.Lock()
	current := s.clients[client.id]
	if current == nil {
		s.mu.Unlock()
		return
	}
	channel := current.channel
	delete(s.clients, client.id)
	if channel != "" {
		members := s.channels[channel]
		filtered := members[:0]
		for _, memberID := range members {
			if memberID != client.id {
				filtered = append(filtered, memberID)
			}
		}
		if len(filtered) == 0 {
			delete(s.channels, channel)
		} else {
			s.channels[channel] = filtered
		}
	}
	s.mu.Unlock()
	_ = client.connection.Close()
	if channel != "" {
		s.broadcastMemberList(channel)
	}
}

func (s *ChatServer) handleStoreHistory(client *chatClient, decrypted map[string]any) {
	envelope, ok := parseHistoryEnvelope(decrypted["p"])
	token, tokenOK := nonEmptyString(decrypted["k"])
	if !ok || !tokenOK || len(token) < 32 || len(token) > 128 {
		return
	}
	s.mu.RLock()
	channel := client.historyChannel
	s.mu.RUnlock()
	if channel == "" {
		return
	}
	s.storeHistory(channel, token, envelope)
}

func (s *ChatServer) storeHistory(channel, token string, envelope historyEnvelope) bool {
	if s.historyAccessStatus(channel, token, true) != "ok" {
		return false
	}
	payload, err := json.Marshal(envelope.Payload)
	if err != nil || len(payload) > historyMaxMessageBytes {
		return false
	}
	_, err = s.db.Exec(`
		INSERT OR IGNORE INTO messages (channel, auth_token, message_id, created_at, payload) VALUES (?, ?, ?, ?, ?)
	`, channel, token, envelope.MessageID, envelope.Timestamp, string(payload))
	if err != nil {
		return false
	}
	_, _ = s.db.Exec(`
		DELETE FROM messages WHERE channel = ? AND auth_token = ? AND id NOT IN (
			SELECT id FROM messages WHERE channel = ? AND auth_token = ? ORDER BY id DESC LIMIT ?
		)
	`, channel, token, channel, token, historyLimitPerRoom)
	return true
}

func (s *ChatServer) handleHistoryRequest(client *chatClient, decrypted map[string]any) {
	requestPayload, ok := decrypted["p"].(map[string]any)
	token, tokenOK := nonEmptyString(decrypted["k"])
	if !ok || !tokenOK {
		return
	}
	s.mu.RLock()
	channel := client.historyChannel
	shared := append([]byte(nil), client.shared...)
	s.mu.RUnlock()
	if channel == "" || len(shared) != 32 {
		return
	}
	before := int64(1<<53 - 1)
	if value, valid := safePositiveInteger(requestPayload["b"]); valid {
		before = value
	}
	limit := historyPageSize
	if value, valid := safeInteger(requestPayload["l"]); valid {
		limit = int(value)
	}
	if limit < 1 {
		limit = 1
	} else if limit > 100 {
		limit = 100
	}
	page := s.loadHistory(channel, token, before, limit)
	s.sendEncrypted(client, shared, map[string]any{"a": "h", "p": page})
}

func (s *ChatServer) loadHistory(channel, token string, before int64, limit int) HistoryPage {
	page := HistoryPage{Messages: []map[string]any{}, Status: s.historyAccessStatus(channel, token, false)}
	if page.Status != "ok" {
		return page
	}
	if before <= 0 || before > 1<<53-1 {
		before = 1<<53 - 1
	}
	if limit < 1 {
		limit = historyPageSize
	} else if limit > 100 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT id, payload FROM messages WHERE channel = ? AND auth_token = ? AND id < ? ORDER BY id DESC LIMIT ?
	`, channel, token, before, limit+1)
	if err != nil {
		page.Status = "query_failed"
		return page
	}
	defer rows.Close()
	type historyRow struct {
		id      int64
		payload map[string]any
		bytes   int
	}
	selected := make([]historyRow, 0, limit)
	rowCount := 0
	responseBytes := 0
	for rows.Next() {
		rowCount++
		var id int64
		var payloadJSON string
		if err := rows.Scan(&id, &payloadJSON); err != nil {
			page.Status = "query_failed"
			return page
		}
		if len(selected) >= limit {
			continue
		}
		if len(selected) > 0 && responseBytes+len(payloadJSON) > historyMaxResponseBytes {
			continue
		}
		var payload map[string]any
		if json.Unmarshal([]byte(payloadJSON), &payload) != nil {
			continue
		}
		selected = append(selected, historyRow{id: id, payload: payload, bytes: len(payloadJSON)})
		responseBytes += len(payloadJSON)
	}
	if rows.Err() != nil {
		page.Status = "query_failed"
		return page
	}
	messages := make([]map[string]any, 0, len(selected))
	for index := len(selected) - 1; index >= 0; index-- {
		selected[index].payload["s"] = selected[index].id
		messages = append(messages, selected[index].payload)
	}
	var nextBefore *int64
	if len(selected) > 0 {
		value := selected[len(selected)-1].id
		nextBefore = &value
	}
	page.Messages = messages
	page.Before = nextBefore
	page.HasMore = rowCount > len(selected)
	return page
}

func parseHistoryEnvelope(value any) (historyEnvelope, bool) {
	payload, ok := value.(map[string]any)
	if !ok {
		return historyEnvelope{}, false
	}
	version, versionOK := safeInteger(payload["v"])
	messageID, idOK := nonEmptyString(payload["i"])
	timestamp, timestampOK := safePositiveInteger(payload["ts"])
	nonce, nonceOK := nonEmptyString(payload["n"])
	ciphertext, cipherOK := nonEmptyString(payload["c"])
	if !versionOK || version != 1 || !idOK || len(messageID) > 128 || !timestampOK ||
		!nonceOK || len(nonce) > 64 || !cipherOK {
		return historyEnvelope{}, false
	}
	return historyEnvelope{
		Version: version, MessageID: messageID, Timestamp: timestamp,
		Nonce: nonce, Cipher: ciphertext, Payload: payload,
	}, true
}

func (s *ChatServer) historyAccessStatus(channel, token string, createIfMissing bool) string {
	if len(token) < 32 || len(token) > 128 {
		return "invalid_request"
	}
	if createIfMissing {
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO rooms (channel, auth_token, created_at) VALUES (?, ?, ?)`,
			channel, token, time.Now().UnixMilli()); err != nil {
			return "query_failed"
		}
	}
	var exactToken string
	if err := s.db.QueryRow(`SELECT auth_token FROM rooms WHERE channel = ? AND auth_token = ?`, channel, token).Scan(&exactToken); err == nil {
		return "ok"
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "query_failed"
	}
	return "room_not_found"
}

func (s *ChatServer) sendEncrypted(client *chatClient, key []byte, message map[string]any) {
	encrypted, err := encryptTransport(message, key)
	if err != nil {
		return
	}
	_ = client.writeText([]byte(encrypted))
}

func encryptTransport(message map[string]any, key []byte) (string, error) {
	if len(key) != 32 {
		return "", errors.New("invalid transport key")
	}
	plaintext, err := json.Marshal(message)
	if err != nil {
		return "", err
	}
	if remainder := len(plaintext) % aes.BlockSize; remainder != 0 {
		plaintext = append(plaintext, make([]byte, aes.BlockSize-remainder)...)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	ciphertext := make([]byte, len(plaintext))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, plaintext)
	return base64.StdEncoding.EncodeToString(iv) + "|" + base64.StdEncoding.EncodeToString(ciphertext), nil
}

func decryptTransport(message string, key []byte) (map[string]any, error) {
	if len(key) != 32 {
		return nil, errors.New("invalid transport key")
	}
	parts := strings.Split(message, "|")
	if len(parts) != 2 {
		return nil, errors.New("invalid encrypted message")
	}
	iv, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil || len(iv) != aes.BlockSize {
		return nil, errors.New("invalid IV")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil || len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("invalid ciphertext")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)
	plaintext = bytes.TrimRight(plaintext, "\x00")
	var result map[string]any
	if err := json.Unmarshal(plaintext, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func nonEmptyString(value any) (string, bool) {
	text, ok := value.(string)
	return text, ok && text != ""
}

func safeInteger(value any) (int64, bool) {
	number, ok := value.(float64)
	if !ok || number > float64(1<<53-1) || number < float64(-(1<<53-1)) || number != float64(int64(number)) {
		return 0, false
	}
	return int64(number), true
}

func safePositiveInteger(value any) (int64, bool) {
	number, ok := safeInteger(value)
	return number, ok && number > 0
}
