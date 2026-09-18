package resource

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

type allocationResult struct {
	lease Lease
	err   error
}

// TestLeaseLifecycleAndForwarding covers exclusivity, ownership, waiting, and TCP forwarding.
func TestLeaseLifecycleAndForwarding(t *testing.T) {
	var (
		targetListener net.Listener
		manager        *Manager
		capabilityA    Capability
		capabilityB    Capability
		leaseA         Lease
		token          string
		err            error
		start          int
		result         chan allocationResult = make(chan allocationResult, 1)
		waiting        allocationResult
		held           net.Conn
	)

	if targetListener, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer targetListener.Close()
	go echo(targetListener)
	if start, err = freeRange(2); err != nil {
		t.Fatal(err)
	}
	if manager, err = Open(Config{
		DatabasePath: filepath.Join(t.TempDir(), "resources.db"),
		ListenHost:   "127.0.0.1",
		PortStart:    start,
		PortEnd:      start + 1,
		DefaultTTL:   2 * time.Second,
		MaximumTTL:   10 * time.Second,
		MaximumRun:   2,
		MaximumUser:  2,
	}); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	if token, capabilityA, err = manager.IssueCapability("user-a", "run-a", "workspace-a", "127.0.0.1", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if capabilityA, err = manager.Authenticate(token); err != nil {
		t.Fatal(err)
	}
	if _, capabilityB, err = manager.IssueCapability("user-b", "run-b", "workspace-b", "127.0.0.1", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if leaseA, err = manager.Allocate(context.Background(), capabilityA, []int{targetListener.Addr().(*net.TCPAddr).Port, targetListener.Addr().(*net.TCPAddr).Port}, 5*time.Second, 0); err != nil {
		t.Fatal(err)
	}
	assertForwarding(t, leaseA.Ports[0].HostPort)
	if held, err = net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(leaseA.Ports[0].HostPort)), time.Second); err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	if err = manager.Release(capabilityB, leaseA.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another capability released the lease: %v", err)
	}
	if _, err = manager.Allocate(context.Background(), capabilityB, []int{targetListener.Addr().(*net.TCPAddr).Port}, 5*time.Second, 0); !errors.Is(err, ErrExhausted) {
		t.Fatalf("expected exhaustion, got %v", err)
	}

	go func() {
		var allocation allocationResult
		allocation.lease, allocation.err = manager.Allocate(context.Background(), capabilityB, []int{targetListener.Addr().(*net.TCPAddr).Port}, 5*time.Second, 2*time.Second)
		result <- allocation
	}()
	time.Sleep(100 * time.Millisecond)
	if err = manager.Release(capabilityA, leaseA.ID); err != nil {
		t.Fatal(err)
	}
	if err = held.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = held.Read(make([]byte, 1)); err == nil {
		t.Fatal("active connection survived lease release")
	}
	waiting = <-result
	if waiting.err != nil {
		t.Fatalf("waiting allocation failed: %v", waiting.err)
	}
	if waiting.lease.UserID != "user-b" {
		t.Fatalf("unexpected waiting lease: %#v", waiting.lease)
	}
}

// TestReconcileRestoresListeners verifies durable leases survive a daemon restart.
func TestReconcileRestoresListeners(t *testing.T) {
	var (
		targetListener net.Listener
		manager        *Manager
		capability     Capability
		lease          Lease
		token          string
		err            error
		start          int
		database       string = filepath.Join(t.TempDir(), "resources.db")
		cfg            Config
	)

	if targetListener, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer targetListener.Close()
	go echo(targetListener)
	if start, err = freeRange(1); err != nil {
		t.Fatal(err)
	}
	cfg = Config{DatabasePath: database, ListenHost: "127.0.0.1", PortStart: start, PortEnd: start, DefaultTTL: time.Second, MaximumTTL: 10 * time.Second, MaximumRun: 1, MaximumUser: 1}
	if manager, err = Open(cfg); err != nil {
		t.Fatal(err)
	}
	if token, capability, err = manager.IssueCapability("user", "run", "workspace", "127.0.0.1", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if lease, err = manager.Allocate(context.Background(), capability, []int{targetListener.Addr().(*net.TCPAddr).Port}, 5*time.Second, 0); err != nil {
		t.Fatal(err)
	}
	if err = manager.Close(); err != nil {
		t.Fatal(err)
	}
	if manager, err = Open(cfg); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err = manager.Authenticate(token); err != nil {
		t.Fatalf("capability was not restored: %v", err)
	}
	assertForwarding(t, lease.Ports[0].HostPort)
}

func echo(listener net.Listener) {
	for {
		var (
			connection net.Conn
			err        error
		)
		if connection, err = listener.Accept(); err != nil {
			return
		}
		go func(connection net.Conn) {
			defer connection.Close()
			_, _ = io.Copy(connection, connection)
		}(connection)
	}
}

func assertForwarding(t *testing.T, port int) {
	var (
		connection net.Conn
		response   []byte = make([]byte, 4)
		err        error
	)

	t.Helper()
	if connection, err = net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second); err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err = connection.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != "ping" {
		t.Fatalf("unexpected proxy response %q", response)
	}
}

func freeRange(count int) (start int, err error) {
	for candidate := 32000; candidate < 60000-count; candidate++ {
		var listeners []net.Listener
		var available bool = true

		for offset := 0; offset < count; offset++ {
			var listener net.Listener
			if listener, err = net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(candidate+offset))); err != nil {
				available = false
				break
			}
			listeners = append(listeners, listener)
		}
		for _, listener := range listeners {
			_ = listener.Close()
		}
		if available {
			start = candidate
			err = nil
			return
		}
	}
	err = errors.New("no free local port range found")
	return
}
