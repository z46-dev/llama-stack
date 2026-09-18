package resource

import (
	"io"
	"net"
	"time"
)

func newProxy(listener net.Listener, target string) (portProxy *proxy) {
	portProxy = &proxy{listener: listener, target: target, connections: make(map[net.Conn]struct{})}
	return
}

func (portProxy *proxy) serve() {
	for {
		var (
			client net.Conn
			err    error
		)
		if client, err = portProxy.listener.Accept(); err != nil {
			return
		}
		portProxy.mutex.Lock()
		if portProxy.closed {
			portProxy.mutex.Unlock()
			_ = client.Close()
			return
		}
		portProxy.connections[client] = struct{}{}
		portProxy.mutex.Unlock()
		go portProxy.forward(client)
	}
}

func (portProxy *proxy) forward(client net.Conn) {
	var (
		upstream net.Conn
		err      error
	)
	defer func() {
		_ = client.Close()
		portProxy.mutex.Lock()
		delete(portProxy.connections, client)
		portProxy.mutex.Unlock()
	}()
	if upstream, err = net.DialTimeout("tcp", portProxy.target, 10*time.Second); err != nil {
		return
	}
	defer upstream.Close()

	var complete chan struct{} = make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, client)
		complete <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, upstream)
		complete <- struct{}{}
	}()
	<-complete
}

func (portProxy *proxy) close() {
	portProxy.mutex.Lock()
	defer portProxy.mutex.Unlock()
	portProxy.closed = true
	_ = portProxy.listener.Close()
	for connection := range portProxy.connections {
		_ = connection.Close()
	}
}
