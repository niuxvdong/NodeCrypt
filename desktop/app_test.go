package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormaliseNodeAddress(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		valid    bool
	}{
		{name: "IP and port", input: "192.168.1.20:8788", expected: "http://192.168.1.20:8788/", valid: true},
		{name: "room share link", input: "http://10.0.0.8:8788/?r=room&p=secret", expected: "http://10.0.0.8:8788/?r=room&p=secret", valid: true},
		{name: "loopback", input: "127.0.0.1:8788", expected: "http://127.0.0.1:8788/", valid: true},
		{name: "default port", input: "192.168.1.20", expected: "http://192.168.1.20:8788/", valid: true},
		{name: "host name", input: "nodecrypt.local:8788", valid: false},
		{name: "HTTPS unsupported", input: "https://192.168.1.20:8788", valid: false},
		{name: "multicast address", input: "239.255.42.99:8788", valid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, valid := normaliseNodeAddress(test.input)
			if valid != test.valid || actual != test.expected {
				t.Fatalf("normaliseNodeAddress(%q) = %q, %v; want %q, %v", test.input, actual, valid, test.expected, test.valid)
			}
		})
	}
}

func TestUsableLocalIPv4RejectsAutomaticPrivateAddress(t *testing.T) {
	if isUsableLocalIPv4(net.ParseIP("169.254.20.4")) {
		t.Fatal("link-local address must not be advertised as a LAN endpoint")
	}
	if !isUsableLocalIPv4(net.ParseIP("192.168.3.115")) {
		t.Fatal("private LAN address should be advertised")
	}
}

func TestFindReachableNodeURLTriesAllCandidates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/runtime" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"mode":"desktop","authRequired":false}`))
	}))
	defer server.Close()

	actual, ok := findReachableNodeURL([]string{"http://127.0.0.1:1", server.URL})
	if !ok || actual != server.URL {
		t.Fatalf("reachable candidate was not selected: %q, %v", actual, ok)
	}
}

func TestProbeNodeEndpointRejectsUnrelatedHTTPServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte(`{"mode":"other"}`))
	}))
	defer server.Close()

	if probeNodeEndpoint(t.Context(), server.URL) {
		t.Fatal("unrelated HTTP server must not be accepted as a NodeCrypt node")
	}
}

func TestRecentRoomsRoundTrip(t *testing.T) {
	app := &App{configDir: t.TempDir()}
	if !app.SaveRecentRoom("192.168.1.20:8788", "Alice", "General", "secret") {
		t.Fatal("recent room was not saved")
	}
	rooms := app.ListRecentRooms()
	if len(rooms) != 1 || rooms[0].UserName != "Alice" || rooms[0].RoomName != "General" || !rooms[0].HasPassword {
		t.Fatalf("unexpected recent rooms: %#v", rooms)
	}
	credentials := app.GetRecentRoom(rooms[0].ID)
	if !credentials.Found || credentials.Password != "secret" || credentials.NodeURL != "http://192.168.1.20:8788" {
		t.Fatalf("unexpected recent room credentials: %#v", credentials)
	}
	if !app.DeleteRecentRoom(rooms[0].ID) || len(app.ListRecentRooms()) != 0 {
		t.Fatal("recent room was not deleted")
	}
}
