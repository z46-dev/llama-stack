package resource

import (
	"errors"
	"net"
	"sync"
	"time"
)

var (
	ErrExhausted = errors.New("port pool is exhausted")
	ErrForbidden = errors.New("resource does not belong to this capability")
	ErrNotFound  = errors.New("resource was not found")
)

type (
	Config struct {
		DatabasePath string
		ListenHost   string
		PortStart    int
		PortEnd      int
		DefaultTTL   time.Duration
		MaximumTTL   time.Duration
		MaximumRun   int
		MaximumUser  int
	}

	Capability struct {
		ID          string    `json:"id"`
		Hash        string    `json:"-"`
		UserID      string    `json:"user_id"`
		RunID       string    `json:"run_id"`
		WorkspaceID string    `json:"workspace_id"`
		TargetHost  string    `json:"target_host"`
		ExpiresAt   time.Time `json:"expires_at"`
	}

	Lease struct {
		ID          string    `json:"id"`
		UserID      string    `json:"user_id"`
		RunID       string    `json:"run_id"`
		WorkspaceID string    `json:"workspace_id"`
		TargetHost  string    `json:"target_host"`
		Ports       []Port    `json:"ports"`
		CreatedAt   time.Time `json:"created_at"`
		ExpiresAt   time.Time `json:"expires_at"`
	}

	Port struct {
		HostPort   int `json:"host_port"`
		TargetPort int `json:"target_port"`
	}

	Manager struct {
		config    Config
		store     *store
		mutex     sync.Mutex
		proxies   map[int]*proxy
		changed   chan struct{}
		closed    chan struct{}
		closeOnce sync.Once
	}

	proxy struct {
		listener    net.Listener
		target      string
		mutex       sync.Mutex
		closed      bool
		connections map[net.Conn]struct{}
	}
)
