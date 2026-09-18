package resourceapi

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/z46-dev/llama-stack/internal/resource"
)

// TestAuthenticatedLeaseAPI covers administrator issuance and capability-scoped allocation.
func TestAuthenticatedLeaseAPI(t *testing.T) {
	var (
		listener   net.Listener
		manager    *resource.Manager
		server     *httptest.Server
		admin      Client
		agent      Client
		issued     IssueCapabilityResponse
		lease      resource.Lease
		leases     []resource.Lease
		response   *http.Response
		connection net.Conn
		err        error
		publicPort int
		scheduled  time.Time
	)

	if listener, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	publicPort = listener.Addr().(*net.TCPAddr).Port
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	if manager, err = resource.Open(resource.Config{
		DatabasePath: filepath.Join(t.TempDir(), "resources.db"),
		ListenHost:   "127.0.0.1",
		PortStart:    publicPort,
		PortEnd:      publicPort,
		DefaultTTL:   time.Minute,
		MaximumTTL:   time.Hour,
		MaximumRun:   1,
		MaximumUser:  1,
	}); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	server = httptest.NewServer(NewHandler(manager, "admin-secret", time.Second, func(expiresAt time.Time) (scheduleErr error) {
		scheduled = expiresAt
		return
	}))
	defer server.Close()

	admin = Client{BaseURL: server.URL, AdminToken: "admin-secret"}
	if issued, err = admin.IssueCapability(context.Background(), IssueCapabilityRequest{
		UserID: "user-a", RunID: "run-a", WorkspaceID: "workspace-a", TargetHost: "127.0.0.1", TTLSeconds: 600,
	}); err != nil {
		t.Fatal(err)
	}
	agent = Client{BaseURL: server.URL, CapabilityToken: issued.Token}
	if lease, err = agent.Allocate(context.Background(), AllocateRequest{TargetPorts: []int{8080}, TTLSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	if len(lease.Ports) != 1 || lease.Ports[0].HostPort != publicPort {
		t.Fatalf("unexpected lease: %#v", lease)
	}
	if !scheduled.Equal(lease.ExpiresAt) {
		t.Fatalf("lease expiry was not scheduled: got %s, want %s", scheduled, lease.ExpiresAt)
	}
	if leases, err = agent.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 || leases[0].ID != lease.ID {
		t.Fatalf("unexpected lease listing: %#v", leases)
	}
	if response, err = http.Get(server.URL + "/v1/admin/leases"); err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("unauthenticated administrator request returned %s", strconv.Itoa(response.StatusCode))
	}
	if err = admin.RevokeCapability(context.Background(), issued.Capability.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = agent.List(context.Background()); err == nil {
		t.Fatal("revoked capability remained usable")
	}
	if connection, err = net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(publicPort)), 100*time.Millisecond); err == nil {
		_ = connection.Close()
		t.Fatal("revoked capability left its port listener active")
	}
}
