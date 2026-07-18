package main

import "testing"

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
