//go:build windows

package main

import (
	"encoding/binary"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	nodeCryptServiceType    = "_nodecrypt._tcp.local"
	dnsQueryRequestVersion1 = 1
	dnsRequestPending       = 9506
	dnsTypePTR              = 12
)

type systemDiscovery interface {
	Close()
	SetName(string)
}

type dnsServiceCancel struct {
	Reserved uintptr
}

type dnsServiceRegisterRequest struct {
	Version         uint32
	InterfaceIndex  uint32
	ServiceInstance uintptr
	Callback        uintptr
	Context         uintptr
	Credentials     uintptr
	UnicastEnabled  uint32
	_               uint32
}

type dnsServiceBrowseRequest struct {
	Version        uint32
	InterfaceIndex uint32
	QueryName      uintptr
	Callback       uintptr
	Context        uintptr
}

type dnsServiceResolveRequest struct {
	Version        uint32
	InterfaceIndex uint32
	QueryName      uintptr
	Callback       uintptr
	Context        uintptr
}

type dnsServiceInstance struct {
	InstanceName   uintptr
	HostName       uintptr
	IPv4Address    uintptr
	IPv6Address    uintptr
	Port           uint16
	Priority       uint16
	Weight         uint16
	_              uint16
	PropertyCount  uint32
	_              uint32
	Keys           uintptr
	Values         uintptr
	InterfaceIndex uint32
	_              uint32
}

type dnsRecord struct {
	Next       uintptr
	Name       uintptr
	Type       uint16
	DataLength uint16
	Flags      uint32
	TTL        uint32
	Reserved   uint32
	Data       uintptr
}

type nativeDNSDiscovery struct {
	localID string
	onNode  func(NodeInfo)
	context uintptr

	registerRequest dnsServiceRegisterRequest
	registerCancel  dnsServiceCancel
	browseRequest   dnsServiceBrowseRequest
	browseCancel    dnsServiceCancel
	browseName      []uint16
	serviceInstance uintptr

	mu      sync.Mutex
	targets map[string]bool
	pending map[string]*dnsResolveOperation
	closed  atomic.Bool
	done    chan struct{}
}

type dnsResolveOperation struct {
	discovery *nativeDNSDiscovery
	target    string
	context   uintptr
	queryName []uint16
	request   dnsServiceResolveRequest
	cancel    dnsServiceCancel
}

var (
	dnsAPI                       = windows.NewLazySystemDLL("dnsapi.dll")
	procDnsServiceConstruct      = dnsAPI.NewProc("DnsServiceConstructInstance")
	procDnsServiceFree           = dnsAPI.NewProc("DnsServiceFreeInstance")
	procDnsServiceRegister       = dnsAPI.NewProc("DnsServiceRegister")
	procDnsServiceDeregister     = dnsAPI.NewProc("DnsServiceDeRegister")
	procDnsServiceRegisterCancel = dnsAPI.NewProc("DnsServiceRegisterCancel")
	procDnsServiceBrowse         = dnsAPI.NewProc("DnsServiceBrowse")
	procDnsServiceBrowseCancel   = dnsAPI.NewProc("DnsServiceBrowseCancel")
	procDnsServiceResolve        = dnsAPI.NewProc("DnsServiceResolve")
	procDnsServiceResolveCancel  = dnsAPI.NewProc("DnsServiceResolveCancel")
	dnsBrowseCallbackPointer     = windows.NewCallback(dnsBrowseCallback)
	dnsResolveCallbackPointer    = windows.NewCallback(dnsResolveCallback)
	dnsRegisterCallbackPointer   = windows.NewCallback(dnsRegisterCallback)
	dnsContextSequence           atomic.Uintptr
	dnsDiscoveryContexts         sync.Map
	dnsResolveContexts           sync.Map
)

func startSystemDiscovery(id, name string, port int, onNode func(NodeInfo)) systemDiscovery {
	for _, procedure := range []*windows.LazyProc{
		procDnsServiceConstruct, procDnsServiceFree, procDnsServiceRegister,
		procDnsServiceDeregister, procDnsServiceRegisterCancel, procDnsServiceBrowse,
		procDnsServiceBrowseCancel, procDnsServiceResolve, procDnsServiceResolveCancel,
	} {
		if procedure.Find() != nil {
			return nil
		}
	}
	discovery := &nativeDNSDiscovery{
		localID: id,
		onNode:  onNode,
		context: nextDNSContext(),
		targets: make(map[string]bool),
		pending: make(map[string]*dnsResolveOperation),
		done:    make(chan struct{}),
	}
	dnsDiscoveryContexts.Store(discovery.context, discovery)
	if !discovery.register(name, port) || !discovery.browse() {
		discovery.Close()
		return nil
	}
	go discovery.refreshLoop()
	return discovery
}

func nextDNSContext() uintptr {
	return dnsContextSequence.Add(1)
}

func (d *nativeDNSDiscovery) register(name string, port int) bool {
	shortID := d.localID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	serviceName, _ := windows.UTF16PtrFromString("NodeCrypt-" + shortID + "." + nodeCryptServiceType)
	hostName, _ := windows.UTF16PtrFromString("nodecrypt-" + shortID + ".local")
	localAddresses := localIPv4Addresses()
	primaryAddress := ""
	if len(localAddresses) > 0 {
		primaryAddress = localAddresses[0]
	}
	keys := []string{"nodeId", "nodeName", "nodeAddress", "nodeAddresses"}
	values := []string{d.localID, name, primaryAddress, strings.Join(localAddresses, ",")}
	keyBuffers := make([][]uint16, len(keys))
	valueBuffers := make([][]uint16, len(values))
	keyPointers := make([]uintptr, len(keys))
	valuePointers := make([]uintptr, len(values))
	for index := range keys {
		keyBuffers[index], _ = windows.UTF16FromString(keys[index])
		valueBuffers[index], _ = windows.UTF16FromString(values[index])
		keyPointers[index] = uintptr(unsafe.Pointer(&keyBuffers[index][0]))
		valuePointers[index] = uintptr(unsafe.Pointer(&valueBuffers[index][0]))
	}
	var ipv4 uint32
	var ipv4Pointer uintptr
	if primaryAddress != "" {
		if parsed := net.ParseIP(primaryAddress).To4(); parsed != nil {
			ipv4 = binary.LittleEndian.Uint32(parsed)
			ipv4Pointer = uintptr(unsafe.Pointer(&ipv4))
		}
	}
	instance, _, _ := procDnsServiceConstruct.Call(
		uintptr(unsafe.Pointer(serviceName)), uintptr(unsafe.Pointer(hostName)), ipv4Pointer, 0,
		uintptr(uint16(port)), 0, 0, uintptr(len(keys)),
		uintptr(unsafe.Pointer(&keyPointers[0])), uintptr(unsafe.Pointer(&valuePointers[0])),
	)
	if instance == 0 {
		return false
	}
	d.serviceInstance = instance
	d.registerRequest = dnsServiceRegisterRequest{
		Version:         dnsQueryRequestVersion1,
		ServiceInstance: instance,
		Callback:        dnsRegisterCallbackPointer,
		Context:         d.context,
	}
	status, _, _ := procDnsServiceRegister.Call(
		uintptr(unsafe.Pointer(&d.registerRequest)), uintptr(unsafe.Pointer(&d.registerCancel)),
	)
	return status == 0 || status == dnsRequestPending
}

func (d *nativeDNSDiscovery) browse() bool {
	d.browseName, _ = windows.UTF16FromString(nodeCryptServiceType)
	d.browseRequest = dnsServiceBrowseRequest{
		Version:   dnsQueryRequestVersion1,
		QueryName: uintptr(unsafe.Pointer(&d.browseName[0])),
		Callback:  dnsBrowseCallbackPointer,
		Context:   d.context,
	}
	status, _, _ := procDnsServiceBrowse.Call(
		uintptr(unsafe.Pointer(&d.browseRequest)), uintptr(unsafe.Pointer(&d.browseCancel)),
	)
	return status == 0 || status == dnsRequestPending
}

func dnsRegisterCallback(status, context, _ uintptr) uintptr {
	return 0
}

func dnsBrowseCallback(status, context, recordPointer uintptr) uintptr {
	value, ok := dnsDiscoveryContexts.Load(context)
	if !ok || status != 0 || recordPointer == 0 {
		return 0
	}
	discovery := value.(*nativeDNSDiscovery)
	for recordPointer != 0 {
		record := (*dnsRecord)(unsafe.Pointer(recordPointer))
		if record.Type == dnsTypePTR && record.Data != 0 {
			target := windows.UTF16PtrToString((*uint16)(unsafe.Pointer(record.Data)))
			if strings.HasSuffix(strings.ToLower(strings.TrimSuffix(target, ".")), "."+nodeCryptServiceType) {
				deleted := record.Flags&4 != 0
				discovery.setTarget(target, deleted)
			}
		}
		recordPointer = record.Next
	}
	return 0
}

func (d *nativeDNSDiscovery) setTarget(target string, deleted bool) {
	if d.closed.Load() {
		return
	}
	d.mu.Lock()
	if deleted {
		delete(d.targets, target)
		d.mu.Unlock()
		return
	}
	d.targets[target] = true
	d.mu.Unlock()
	d.resolve(target)
}

func (d *nativeDNSDiscovery) resolve(target string) {
	if d.closed.Load() {
		return
	}
	d.mu.Lock()
	if d.pending[target] != nil {
		d.mu.Unlock()
		return
	}
	operation := &dnsResolveOperation{
		discovery: d,
		target:    target,
		context:   nextDNSContext(),
	}
	operation.queryName, _ = windows.UTF16FromString(target)
	operation.request = dnsServiceResolveRequest{
		Version:   dnsQueryRequestVersion1,
		QueryName: uintptr(unsafe.Pointer(&operation.queryName[0])),
		Callback:  dnsResolveCallbackPointer,
		Context:   operation.context,
	}
	d.pending[target] = operation
	d.mu.Unlock()
	dnsResolveContexts.Store(operation.context, operation)
	status, _, _ := procDnsServiceResolve.Call(
		uintptr(unsafe.Pointer(&operation.request)), uintptr(unsafe.Pointer(&operation.cancel)),
	)
	if status != 0 && status != dnsRequestPending {
		operation.finish()
	}
}

func dnsResolveCallback(status, context, instancePointer uintptr) uintptr {
	value, ok := dnsResolveContexts.Load(context)
	if !ok {
		return 0
	}
	operation := value.(*dnsResolveOperation)
	defer operation.finish()
	if status != 0 || instancePointer == 0 || operation.discovery.closed.Load() {
		return 0
	}
	instance := (*dnsServiceInstance)(unsafe.Pointer(instancePointer))
	info := NodeInfo{Port: int(instance.Port), LastSeen: time.Now().UnixMilli()}
	properties := dnsServiceProperties(instance)
	info.ID = properties["nodeId"]
	info.Name = properties["nodeName"]
	if info.ID == "" || info.ID == operation.discovery.localID || info.Port < 1 {
		procDnsServiceFree.Call(instancePointer)
		return 0
	}
	addresses := make([]string, 0, 4)
	if instance.IPv4Address != 0 {
		raw := *(*[4]byte)(unsafe.Pointer(instance.IPv4Address))
		addresses = append(addresses, net.IPv4(raw[0], raw[1], raw[2], raw[3]).String())
	}
	if instance.HostName != 0 {
		hostName := windows.UTF16PtrToString((*uint16)(unsafe.Pointer(instance.HostName)))
		if resolvedAddresses, err := net.LookupIP(strings.TrimSuffix(hostName, ".")); err == nil {
			for _, address := range resolvedAddresses {
				if address.To4() != nil {
					addresses = append(addresses, address.To4().String())
				}
			}
		}
	}
	addresses = append(addresses, strings.Split(properties["nodeAddresses"], ",")...)
	addresses = append(addresses, properties["nodeAddress"])
	info.Addresses = uniqueIPv4Addresses(addresses)
	procDnsServiceFree.Call(instancePointer)
	if len(info.Addresses) == 0 {
		return 0
	}
	info.Address = info.Addresses[0]
	info.URL = "http://" + net.JoinHostPort(info.Address, strconv.Itoa(info.Port))
	operation.discovery.onNode(info)
	return 0
}

func dnsServiceProperties(instance *dnsServiceInstance) map[string]string {
	properties := make(map[string]string)
	if instance.PropertyCount == 0 || instance.Keys == 0 || instance.Values == 0 || instance.PropertyCount > 32 {
		return properties
	}
	keys := unsafe.Slice((*uintptr)(unsafe.Pointer(instance.Keys)), int(instance.PropertyCount))
	values := unsafe.Slice((*uintptr)(unsafe.Pointer(instance.Values)), int(instance.PropertyCount))
	for index := range keys {
		if keys[index] != 0 && values[index] != 0 {
			key := windows.UTF16PtrToString((*uint16)(unsafe.Pointer(keys[index])))
			value := windows.UTF16PtrToString((*uint16)(unsafe.Pointer(values[index])))
			properties[key] = value
		}
	}
	return properties
}

func (operation *dnsResolveOperation) finish() {
	dnsResolveContexts.Delete(operation.context)
	operation.discovery.mu.Lock()
	if operation.discovery.pending[operation.target] == operation {
		delete(operation.discovery.pending, operation.target)
	}
	operation.discovery.mu.Unlock()
}

func (d *nativeDNSDiscovery) refreshLoop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			d.mu.Lock()
			targets := make([]string, 0, len(d.targets))
			for target := range d.targets {
				targets = append(targets, target)
			}
			d.mu.Unlock()
			for _, target := range targets {
				d.resolve(target)
			}
		case <-d.done:
			return
		}
	}
}

func (d *nativeDNSDiscovery) SetName(string) {}

func (d *nativeDNSDiscovery) Close() {
	if d == nil || d.closed.Swap(true) {
		return
	}
	close(d.done)
	procDnsServiceBrowseCancel.Call(uintptr(unsafe.Pointer(&d.browseCancel)))
	procDnsServiceDeregister.Call(uintptr(unsafe.Pointer(&d.registerRequest)), uintptr(unsafe.Pointer(&d.registerCancel)))
	procDnsServiceRegisterCancel.Call(uintptr(unsafe.Pointer(&d.registerCancel)))
	d.mu.Lock()
	operations := make([]*dnsResolveOperation, 0, len(d.pending))
	for _, operation := range d.pending {
		operations = append(operations, operation)
	}
	d.mu.Unlock()
	for _, operation := range operations {
		procDnsServiceResolveCancel.Call(uintptr(unsafe.Pointer(&operation.cancel)))
		operation.finish()
	}
	dnsDiscoveryContexts.Delete(d.context)
}
