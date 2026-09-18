package resource

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"
)

// Open initializes persistent lease storage and restores active listeners.
func Open(cfg Config) (manager *Manager, err error) {
	manager = &Manager{
		config:  cfg,
		proxies: make(map[int]*proxy),
		changed: make(chan struct{}),
		closed:  make(chan struct{}),
	}
	if manager.store, err = openStore(cfg.DatabasePath); err != nil {
		return
	}
	if err = manager.reconcile(); err != nil {
		_ = manager.store.close()
		return nil, err
	}
	go manager.reapLoop()
	return
}

// Close stops forwarding and closes persistent storage.
func (manager *Manager) Close() (err error) {
	manager.closeOnce.Do(func() {
		close(manager.closed)
		manager.mutex.Lock()
		for port, portProxy := range manager.proxies {
			portProxy.close()
			delete(manager.proxies, port)
		}
		manager.mutex.Unlock()
		err = manager.store.close()
	})
	return
}

// IssueCapability creates an opaque, scoped credential for one agent run.
func (manager *Manager) IssueCapability(userID, runID, workspaceID, targetHost string, ttl time.Duration) (token string, capability Capability, err error) {
	var random []byte = make([]byte, 32)

	if userID == "" || runID == "" || workspaceID == "" {
		err = errors.New("user, run, and workspace identifiers are required")
		return
	}
	if net.ParseIP(targetHost) == nil && targetHost != "localhost" {
		err = errors.New("target host must be an IP address or localhost")
		return
	}
	if ttl <= 0 || ttl > manager.config.MaximumTTL {
		err = fmt.Errorf("capability TTL must be between one second and %s", manager.config.MaximumTTL)
		return
	}
	if _, err = rand.Read(random); err != nil {
		return
	}
	token = "lsrc_" + hex.EncodeToString(random)
	capability = Capability{
		Hash:        hashToken(token),
		UserID:      userID,
		RunID:       runID,
		WorkspaceID: workspaceID,
		TargetHost:  targetHost,
		ExpiresAt:   time.Now().UTC().Add(ttl),
	}
	if capability.ID, err = randomID("cap_"); err == nil {
		err = manager.store.insertCapability(capability)
	}
	return
}

// Authenticate resolves an opaque run capability without storing its plaintext value.
func (manager *Manager) Authenticate(token string) (capability Capability, err error) {
	capability, err = manager.store.capabilityByTokenHash(hashToken(token), time.Now().UTC())
	return
}

// Allocate reserves host ports and forwards them to the capability's target host.
func (manager *Manager) Allocate(ctx context.Context, capability Capability, targetPorts []int, ttl, wait time.Duration) (lease Lease, err error) {
	var (
		deadline time.Time = time.Now().Add(wait)
		changed  <-chan struct{}
		timer    *time.Timer
	)

	if len(targetPorts) == 0 {
		err = errors.New("at least one target port is required")
		return
	}
	if ttl == 0 {
		ttl = manager.config.DefaultTTL
	}
	if ttl < time.Second || ttl > manager.config.MaximumTTL {
		err = fmt.Errorf("lease TTL must be between one second and %s", manager.config.MaximumTTL)
		return
	}
	for _, port := range targetPorts {
		if port < 1 || port > 65535 {
			err = fmt.Errorf("target port %d is invalid", port)
			return
		}
	}

	for {
		manager.mutex.Lock()
		changed = manager.changed
		manager.mutex.Unlock()
		if lease, err = manager.allocateOnce(capability, targetPorts, ttl); !errors.Is(err, ErrExhausted) || wait <= 0 {
			return
		}
		if time.Now().After(deadline) {
			err = ErrExhausted
			return
		}
		timer = time.NewTimer(time.Until(deadline))
		select {
		case <-ctx.Done():
			timer.Stop()
			err = ctx.Err()
			return
		case <-timer.C:
			err = ErrExhausted
			return
		case <-changed:
			timer.Stop()
		}
	}
}

func (manager *Manager) allocateOnce(capability Capability, targetPorts []int, ttl time.Duration) (lease Lease, err error) {
	var (
		active    []*leaseRecord
		ports     []Port
		listeners []net.Listener
		now       time.Time = time.Now().UTC()
	)

	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if _, err = manager.store.capabilityByTokenHash(capability.Hash, now); err != nil {
		return
	}
	if err = manager.removeExpiredLocked(now); err != nil {
		return
	}
	if active, err = manager.store.activeLeases(now); err != nil {
		return
	}
	if err = manager.checkLimits(capability, len(targetPorts), active); err != nil {
		return
	}
	if ports, listeners, err = manager.reserveListeners(targetPorts, active); err != nil {
		return
	}
	defer func() {
		if err != nil {
			for _, listener := range listeners {
				_ = listener.Close()
			}
		}
	}()

	if lease.ID, err = randomID("lease_"); err != nil {
		return
	}
	lease.UserID = capability.UserID
	lease.RunID = capability.RunID
	lease.WorkspaceID = capability.WorkspaceID
	lease.TargetHost = capability.TargetHost
	lease.Ports = ports
	lease.CreatedAt = now
	lease.ExpiresAt = minimumTime(now.Add(ttl), capability.ExpiresAt)
	if err = manager.store.insertLease(lease, capability.Hash); err != nil {
		return
	}
	for index, listener := range listeners {
		var portProxy *proxy = newProxy(listener, net.JoinHostPort(lease.TargetHost, strconv.Itoa(ports[index].TargetPort)))
		manager.proxies[ports[index].HostPort] = portProxy
		go portProxy.serve()
	}
	return
}

// List returns the active leases owned by a capability.
func (manager *Manager) List(capability Capability) (leases []Lease, err error) {
	var records []*leaseRecord
	if records, err = manager.store.leasesForCapability(capability.Hash, time.Now().UTC()); err == nil {
		leases = leasesFromRecords(records)
	}
	return
}

// ListAll returns every active lease for administrative inspection.
func (manager *Manager) ListAll() (leases []Lease, err error) {
	var records []*leaseRecord
	if records, err = manager.store.activeLeases(time.Now().UTC()); err == nil {
		leases = leasesFromRecords(records)
	}
	return
}

// Renew extends an owned lease without exceeding capability or configured limits.
func (manager *Manager) Renew(capability Capability, leaseID string, ttl time.Duration) (lease Lease, err error) {
	var record *leaseRecord

	if ttl <= 0 || ttl > manager.config.MaximumTTL {
		err = fmt.Errorf("lease TTL must be between one second and %s", manager.config.MaximumTTL)
		return
	}
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if _, err = manager.store.capabilityByTokenHash(capability.Hash, time.Now().UTC()); err != nil {
		return
	}
	if record, err = manager.store.leaseByID(leaseID); err != nil {
		return
	}
	if record.CapabilityHash != capability.Hash || !record.ExpiresAt.After(time.Now().UTC()) {
		err = ErrNotFound
		return
	}
	record.ExpiresAt = minimumTime(time.Now().UTC().Add(ttl), capability.ExpiresAt)
	if err = manager.store.updateLease(record); err == nil {
		lease = leaseFromRecord(record)
	}
	return
}

// Release closes forwarding and returns every port in an owned lease to the pool.
func (manager *Manager) Release(capability Capability, leaseID string) (err error) {
	err = manager.releaseWhere(leaseID, capability.Hash)
	return
}

// Revoke administratively removes any lease regardless of owner.
func (manager *Manager) Revoke(leaseID string) (err error) {
	err = manager.releaseWhere(leaseID, "")
	return
}

// RevokeCapability invalidates a run credential and releases all of its leases.
func (manager *Manager) RevokeCapability(capabilityID string) (err error) {
	var (
		capability *capabilityRecord
		records    []*leaseRecord
	)

	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if capability, err = manager.store.capabilityByID(capabilityID); err != nil {
		return
	}
	if records, err = manager.store.leasesForCapability(capability.Hash, time.Time{}); err != nil {
		return
	}
	for _, record := range records {
		manager.closeLeaseProxies(record)
	}
	if err = manager.store.deleteCapability(capability); err == nil {
		manager.signalChangedLocked()
	}
	return
}

// Reap releases leases and credentials whose expiration time has passed.
func (manager *Manager) Reap() (err error) {
	manager.mutex.Lock()
	err = manager.removeExpiredLocked(time.Now().UTC())
	manager.mutex.Unlock()
	return
}

func (manager *Manager) releaseWhere(leaseID, capabilityHash string) (err error) {
	var record *leaseRecord

	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if record, err = manager.store.leaseByID(leaseID); err != nil {
		return
	}
	if capabilityHash != "" && record.CapabilityHash != capabilityHash {
		err = ErrNotFound
		return
	}
	if err = manager.store.deleteLease(leaseID); err != nil {
		return
	}
	manager.closeLeaseProxies(record)
	manager.signalChangedLocked()
	return
}

func (manager *Manager) reserveListeners(targetPorts []int, active []*leaseRecord) (ports []Port, listeners []net.Listener, err error) {
	var used map[int]bool = make(map[int]bool)

	for _, record := range active {
		for _, port := range record.Ports {
			used[port.HostPort] = true
		}
	}
	for hostPort := manager.config.PortStart; hostPort <= manager.config.PortEnd && len(ports) < len(targetPorts); hostPort++ {
		var listener net.Listener
		if used[hostPort] {
			continue
		}
		if listener, err = net.Listen("tcp", net.JoinHostPort(manager.config.ListenHost, strconv.Itoa(hostPort))); err != nil {
			continue
		}
		ports = append(ports, Port{HostPort: hostPort, TargetPort: targetPorts[len(ports)]})
		listeners = append(listeners, listener)
	}
	if len(ports) != len(targetPorts) {
		for _, listener := range listeners {
			_ = listener.Close()
		}
		ports = nil
		listeners = nil
		err = ErrExhausted
	}
	return
}

func (manager *Manager) checkLimits(capability Capability, requested int, active []*leaseRecord) (err error) {
	var (
		runCount  int
		userCount int
	)

	for _, record := range active {
		if record.UserID == capability.UserID {
			userCount += len(record.Ports)
			if record.RunID == capability.RunID {
				runCount += len(record.Ports)
			}
		}
	}
	if runCount+requested > manager.config.MaximumRun {
		err = fmt.Errorf("run port limit exceeded: %d maximum", manager.config.MaximumRun)
	} else if userCount+requested > manager.config.MaximumUser {
		err = fmt.Errorf("user port limit exceeded: %d maximum", manager.config.MaximumUser)
	}
	return
}

func (manager *Manager) reconcile() (err error) {
	var records []*leaseRecord

	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if err = manager.removeExpiredLocked(time.Now().UTC()); err != nil {
		return
	}
	if records, err = manager.store.activeLeases(time.Now().UTC()); err != nil {
		return
	}
	for _, record := range records {
		for _, port := range record.Ports {
			var listener net.Listener
			if listener, err = net.Listen("tcp", net.JoinHostPort(manager.config.ListenHost, strconv.Itoa(port.HostPort))); err != nil {
				manager.closeLeaseProxies(record)
				_ = manager.store.deleteLease(record.ID)
				break
			}
			var portProxy *proxy = newProxy(listener, net.JoinHostPort(record.TargetHost, strconv.Itoa(port.TargetPort)))
			manager.proxies[port.HostPort] = portProxy
			go portProxy.serve()
		}
	}
	return
}

func (manager *Manager) removeExpiredLocked(now time.Time) (err error) {
	var records []*leaseRecord

	if records, err = manager.store.expiredLeases(now); err != nil {
		return
	}
	for _, record := range records {
		manager.closeLeaseProxies(record)
	}
	if err = manager.store.deleteExpiredLeases(now); err == nil {
		err = manager.store.deleteExpiredCapabilities(now)
	}
	if err == nil && len(records) > 0 {
		manager.signalChangedLocked()
	}
	return
}

func (manager *Manager) closeLeaseProxies(record *leaseRecord) {
	for _, port := range record.Ports {
		var (
			portProxy *proxy
			found     bool
		)
		if portProxy, found = manager.proxies[port.HostPort]; found {
			portProxy.close()
			delete(manager.proxies, port.HostPort)
		}
	}
}

func (manager *Manager) reapLoop() {
	var ticker *time.Ticker = time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-manager.closed:
			return
		case <-ticker.C:
			_ = manager.Reap()
		}
	}
}

func (manager *Manager) signalChangedLocked() {
	close(manager.changed)
	manager.changed = make(chan struct{})
}

func randomID(prefix string) (identifier string, err error) {
	var value []byte = make([]byte, 16)
	if _, err = rand.Read(value); err != nil {
		return
	}
	identifier = prefix + hex.EncodeToString(value)
	return
}

func hashToken(token string) (hash string) {
	var value [32]byte = sha256.Sum256([]byte(token))
	hash = hex.EncodeToString(value[:])
	return
}

func minimumTime(first, second time.Time) (result time.Time) {
	result = first
	if second.Before(first) {
		result = second
	}
	return
}
