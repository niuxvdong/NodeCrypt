package main

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

const (
	discoveryMagic = "nodecrypt-desktop-v1"
	discoveryGroup = "239.255.42.99:42429"
)

type NodeInfo struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	URL       string   `json:"url"`
	Address   string   `json:"address"`
	Addresses []string `json:"addresses"`
	Port      int      `json:"port"`
	Local     bool     `json:"local"`
	LastSeen  int64    `json:"lastSeen"`
}

type discoveryPacket struct {
	Magic string `json:"magic"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	Port  int    `json:"port"`
}

type discoveredNode struct {
	info NodeInfo
	seen time.Time
}

type DiscoveryService struct {
	id      string
	name    string
	port    int
	group   *net.UDPAddr
	reader  *net.UDPConn
	writer  *net.UDPConn
	nodes   map[string]discoveredNode
	mu      sync.RWMutex
	closed  chan struct{}
	closeMu sync.Once
	system  systemDiscovery
}

func StartDiscovery(id, name string, port int) *DiscoveryService {
	group, err := net.ResolveUDPAddr("udp4", discoveryGroup)
	if err != nil {
		return nil
	}
	d := &DiscoveryService{
		id:     id,
		name:   name,
		port:   port,
		group:  group,
		nodes:  make(map[string]discoveredNode),
		closed: make(chan struct{}),
	}
	d.reader, _ = net.ListenMulticastUDP("udp4", nil, group)
	d.writer, _ = net.DialUDP("udp4", nil, group)
	if d.reader != nil {
		_ = d.reader.SetReadBuffer(64 * 1024)
		go d.readLoop()
	}
	d.system = startSystemDiscovery(id, name, port, d.acceptSystemNode)
	go d.announceLoop()
	d.Announce()
	return d
}

func (d *DiscoveryService) acceptSystemNode(info NodeInfo) {
	if info.ID == "" || info.ID == d.id || info.Address == "" || info.Port < 1 {
		return
	}
	info.LastSeen = time.Now().UnixMilli()
	d.mu.Lock()
	d.nodes[info.ID] = discoveredNode{info: info, seen: time.Now()}
	d.mu.Unlock()
}

func (d *DiscoveryService) announceLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			d.Announce()
			d.prune()
		case <-d.closed:
			return
		}
	}
}

func (d *DiscoveryService) readLoop() {
	buffer := make([]byte, 2048)
	for {
		_ = d.reader.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, remote, err := d.reader.ReadFromUDP(buffer)
		if err != nil {
			select {
			case <-d.closed:
				return
			default:
				continue
			}
		}
		var packet discoveryPacket
		if json.Unmarshal(buffer[:n], &packet) != nil || packet.Magic != discoveryMagic || packet.ID == "" || packet.ID == d.id || packet.Port < 1 {
			continue
		}
		address := remote.IP.String()
		info := NodeInfo{
			ID:        packet.ID,
			Name:      packet.Name,
			URL:       fmt.Sprintf("http://%s:%d", address, packet.Port),
			Address:   address,
			Addresses: []string{address},
			Port:      packet.Port,
			LastSeen:  time.Now().UnixMilli(),
		}
		d.mu.Lock()
		d.nodes[packet.ID] = discoveredNode{info: info, seen: time.Now()}
		d.mu.Unlock()
	}
}

func (d *DiscoveryService) Announce() {
	if d == nil || d.writer == nil {
		return
	}
	d.mu.RLock()
	packet := discoveryPacket{Magic: discoveryMagic, ID: d.id, Name: d.name, Port: d.port}
	d.mu.RUnlock()
	data, _ := json.Marshal(packet)
	_, _ = d.writer.Write(data)
}

func (d *DiscoveryService) SetName(name string) {
	d.mu.Lock()
	d.name = name
	d.mu.Unlock()
	if d.system != nil {
		d.system.SetName(name)
	}
	d.Announce()
}

func (d *DiscoveryService) SystemDiscoveryEnabled() bool {
	return d != nil && d.system != nil
}

func (d *DiscoveryService) Nodes() []NodeInfo {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	nodes := make([]NodeInfo, 0, len(d.nodes))
	for _, node := range d.nodes {
		nodes = append(nodes, node.info)
	}
	return nodes
}

func (d *DiscoveryService) prune() {
	d.mu.Lock()
	defer d.mu.Unlock()
	threshold := time.Now().Add(-7 * time.Second)
	for id, node := range d.nodes {
		if node.seen.Before(threshold) {
			delete(d.nodes, id)
		}
	}
}

func (d *DiscoveryService) Close() {
	if d == nil {
		return
	}
	d.closeMu.Do(func() {
		close(d.closed)
		if d.system != nil {
			d.system.Close()
		}
		if d.reader != nil {
			_ = d.reader.Close()
		}
		if d.writer != nil {
			_ = d.writer.Close()
		}
	})
}
