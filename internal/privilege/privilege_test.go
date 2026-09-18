package privilege

import (
	"os"
	"testing"
)

// TestRequireAdminAllowsDirectRoot covers the non-interactive system-service path.
func TestRequireAdminAllowsDirectRoot(t *testing.T) {
	var err error

	if os.Geteuid() != 0 {
		t.Skip("test runner is not root")
	}

	t.Setenv("SUDO_USER", "")
	if err = RequireAdmin("group-does-not-need-to-exist"); err != nil {
		t.Fatalf("direct root should be allowed: %v", err)
	}
}
