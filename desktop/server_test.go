package main

import (
	"crypto"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"
)

type protocolClient struct {
	connection *websocket.Conn
	shared     []byte
}

func TestChatServerRelayAndHistoryWithoutAccount(t *testing.T) {
	server := startTestServer(t, filepath.Join(t.TempDir(), "nodecrypt.sqlite"))
	defer server.Close()

	assertDesktopRuntimeNeedsNoAccount(t, server)
	first := connectProtocolClient(t, server)
	defer first.connection.Close()
	second := connectProtocolClient(t, server)
	defer second.connection.Close()

	writeEncrypted(t, first, map[string]any{"a": "j", "p": "room-hash", "u": "first-user-claim-0000000000000000"})
	assertJoinAccepted(t, readEncrypted(t, first))
	firstList := readEncrypted(t, first)
	if firstList["a"] != "l" || len(firstList["p"].([]any)) != 0 {
		t.Fatalf("unexpected initial member list: %#v", firstList)
	}

	writeEncrypted(t, second, map[string]any{"a": "j", "p": "room-hash", "u": "second-user-claim-000000000000000"})
	assertJoinAccepted(t, readEncrypted(t, second))
	firstList = readEncrypted(t, first)
	secondList := readEncrypted(t, second)
	firstPeers := firstList["p"].([]any)
	secondPeers := secondList["p"].([]any)
	if len(firstPeers) != 1 || len(secondPeers) != 1 {
		t.Fatalf("expected one peer per client: %#v %#v", firstList, secondList)
	}
	firstID := secondPeers[0].(string)
	secondID := firstPeers[0].(string)

	writeEncrypted(t, first, map[string]any{"a": "c", "c": secondID, "p": "encrypted-peer-payload"})
	relayed := readEncrypted(t, second)
	if relayed["a"] != "c" || relayed["c"] != firstID || relayed["p"] != "encrypted-peer-payload" {
		t.Fatalf("unexpected relay: %#v", relayed)
	}

	token := strings.Repeat("t", 43)
	envelope := map[string]any{
		"v": 1, "i": "message-1", "ts": time.Now().UnixMilli(),
		"n": "history-nonce", "c": "client-side-ciphertext",
	}
	writeEncrypted(t, first, map[string]any{"a": "s", "k": token, "p": envelope})
	writeEncrypted(t, first, map[string]any{"a": "h", "k": token, "p": map[string]any{"l": 50}})
	history := readEncrypted(t, first)
	page := history["p"].(map[string]any)
	messages := page["m"].([]any)
	if history["a"] != "h" || page["r"] != "ok" || len(messages) != 1 {
		t.Fatalf("unexpected history response: %#v", history)
	}
	stored := messages[0].(map[string]any)
	if stored["i"] != "message-1" || stored["c"] != "client-side-ciphertext" || stored["s"] == nil {
		t.Fatalf("unexpected stored envelope: %#v", stored)
	}

	writeEncrypted(t, second, map[string]any{"a": "h", "k": strings.Repeat("x", 43), "p": map[string]any{}})
	denied := readEncrypted(t, second)
	deniedPage := denied["p"].(map[string]any)
	if deniedPage["r"] != "room_not_found" || len(deniedPage["m"].([]any)) != 0 {
		t.Fatalf("history token should have been rejected: %#v", denied)
	}
}

func TestChatServerRejectsDuplicateUsernameClaim(t *testing.T) {
	server := startTestServer(t, filepath.Join(t.TempDir(), "nodecrypt.sqlite"))
	defer server.Close()
	first := connectProtocolClient(t, server)
	defer first.connection.Close()
	second := connectProtocolClient(t, server)
	defer second.connection.Close()
	claim := "same-user-claim-00000000000000000000"
	writeEncrypted(t, first, map[string]any{"a": "j", "p": "same-room", "u": claim})
	assertJoinAccepted(t, readEncrypted(t, first))
	_ = readEncrypted(t, first)
	writeEncrypted(t, second, map[string]any{"a": "j", "p": "same-room", "u": claim})
	rejected := readEncrypted(t, second)
	payload := rejected["p"].(map[string]any)
	if rejected["a"] != "j" || payload["o"] != false || payload["r"] != "username_taken" {
		t.Fatalf("duplicate username claim was not rejected: %#v", rejected)
	}
	otherPassword := connectProtocolClient(t, server)
	defer otherPassword.connection.Close()
	writeEncrypted(t, otherPassword, map[string]any{
		"a": "j", "p": "same-room", "g": "different-password-scope-00000000000", "u": claim,
	})
	assertJoinAccepted(t, readEncrypted(t, otherPassword))
	isolatedList := readEncrypted(t, otherPassword)
	if len(isolatedList["p"].([]any)) != 0 {
		t.Fatalf("different password scope was not isolated: %#v", isolatedList)
	}
}

func TestDesktopLocalHistoryUsesItsOwnSQLite(t *testing.T) {
	server := startTestServer(t, filepath.Join(t.TempDir(), "nodecrypt.sqlite"))
	defer server.Close()
	app := &App{server: server}
	token := strings.Repeat("l", 43)
	if !app.StoreLocalHistory("room-hash", token, 1, "local-message", time.Now().UnixMilli(), "nonce", "ciphertext") {
		t.Fatal("local history was not stored")
	}
	page := app.LoadLocalHistory("room-hash", token, 0, 50)
	if page.Status != "ok" || len(page.Messages) != 1 || page.Messages[0]["i"] != "local-message" {
		t.Fatalf("unexpected local history page: %#v", page)
	}
	wrongPassword := app.LoadLocalHistory("room-hash", strings.Repeat("x", 43), 0, 50)
	if wrongPassword.Status != "room_not_found" || len(wrongPassword.Messages) != 0 {
		t.Fatalf("wrong password should not read local history: %#v", wrongPassword)
	}
	secondToken := strings.Repeat("x", 43)
	if !app.StoreLocalHistory("room-hash", secondToken, 1, "password-room-message", time.Now().UnixMilli(), "nonce-2", "ciphertext-2") {
		t.Fatal("same room name with a different password token was not stored")
	}
	secondPage := app.LoadLocalHistory("room-hash", secondToken, 0, 50)
	if secondPage.Status != "ok" || len(secondPage.Messages) != 1 || secondPage.Messages[0]["i"] != "password-room-message" {
		t.Fatalf("password-specific history was not isolated: %#v", secondPage)
	}
}

func TestHistorySchemaV2MigratesExistingDatabase(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "nodecrypt.sqlite")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE rooms (channel TEXT PRIMARY KEY, auth_token TEXT NOT NULL, created_at INTEGER NOT NULL);
		CREATE TABLE messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel TEXT NOT NULL,
			message_id TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			payload TEXT NOT NULL,
			UNIQUE(channel, message_id)
		);
		INSERT INTO rooms (channel, auth_token, created_at) VALUES ('old-room', 'old-token-00000000000000000000000000000000', 1);
		INSERT INTO messages (channel, message_id, created_at, payload)
			VALUES ('old-room', 'old-message', 2, '{"v":1,"i":"old-message","ts":2,"n":"n","c":"c"}');
	`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	server := startTestServer(t, databasePath)
	defer server.Close()
	page := server.loadHistory("old-room", "old-token-00000000000000000000000000000000", 0, 50)
	if page.Status != "ok" || len(page.Messages) != 1 || page.Messages[0]["i"] != "old-message" {
		t.Fatalf("existing history was not migrated: %#v", page)
	}
}

func assertJoinAccepted(t *testing.T, response map[string]any) {
	t.Helper()
	payload, ok := response["p"].(map[string]any)
	if response["a"] != "j" || !ok || payload["o"] != true {
		t.Fatalf("join was not accepted: %#v", response)
	}
}

func startTestServer(t *testing.T, databasePath string) *ChatServer {
	t.Helper()
	assets := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>test</title>")},
	}
	server, err := StartChatServer(fs.FS(assets), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func assertDesktopRuntimeNeedsNoAccount(t *testing.T, server *ChatServer) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get("http://127.0.0.1:" + serverPort(server) + "/api/runtime")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("runtime endpoint returned %s", response.Status)
	}
	var runtime struct {
		AuthRequired bool `json:"authRequired"`
	}
	if err := json.NewDecoder(response.Body).Decode(&runtime); err != nil {
		t.Fatal(err)
	}
	if runtime.AuthRequired {
		t.Fatal("desktop runtime unexpectedly requires an account")
	}
}

func connectProtocolClient(t *testing.T, server *ChatServer) *protocolClient {
	t.Helper()
	connection, response, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:"+serverPort(server), nil)
	if err != nil {
		if response != nil {
			t.Fatalf("websocket returned %s: %v", response.Status, err)
		}
		t.Fatal(err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, keyMessage, err := connection.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var keyPayload struct {
		Type string `json:"type"`
		Key  string `json:"key"`
	}
	if json.Unmarshal(keyMessage, &keyPayload) != nil || keyPayload.Type != "server-key" {
		t.Fatalf("unexpected key message: %s", keyMessage)
	}

	privateKey, err := ecdh.P384().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.WriteMessage(websocket.TextMessage, []byte(hex.EncodeToString(privateKey.PublicKey().Bytes()))); err != nil {
		t.Fatal(err)
	}
	_, handshake, err := connection.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(handshake), "|")
	if len(parts) != 2 {
		t.Fatalf("invalid handshake: %s", handshake)
	}
	serverECDHBytes, err := hex.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := base64.StdEncoding.DecodeString(keyPayload.Key)
	if err != nil {
		t.Fatal(err)
	}
	parsedPublic, err := x509.ParsePKIXPublicKey(publicDER)
	if err != nil {
		t.Fatal(err)
	}
	rsaPublic := parsedPublic.(*rsa.PublicKey)
	signature, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(serverECDHBytes)
	if err := rsa.VerifyPKCS1v15(rsaPublic, crypto.SHA256, digest[:], signature); err != nil {
		t.Fatalf("invalid server signature: %v", err)
	}
	serverPublic, err := ecdh.P384().NewPublicKey(serverECDHBytes)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := privateKey.ECDH(serverPublic)
	if err != nil {
		t.Fatal(err)
	}
	return &protocolClient{connection: connection, shared: append([]byte(nil), shared[8:40]...)}
}

func writeEncrypted(t *testing.T, client *protocolClient, message map[string]any) {
	t.Helper()
	encrypted, err := encryptTransport(message, client.shared)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.connection.WriteMessage(websocket.TextMessage, []byte(encrypted)); err != nil {
		t.Fatal(err)
	}
}

func readEncrypted(t *testing.T, client *protocolClient) map[string]any {
	t.Helper()
	_ = client.connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, message, err := client.connection.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := decryptTransport(string(message), client.shared)
	if err != nil {
		t.Fatalf("decrypt %q: %v", message, err)
	}
	return decrypted
}

func serverPort(server *ChatServer) string {
	return strconv.Itoa(server.Port())
}
