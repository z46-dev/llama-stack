package main

import (
	"testing"
)

// TestCommandGrammar exercises help and required nested arguments without host access.
func TestCommandGrammar(t *testing.T) {
	var err error

	if err = run([]string{"--help"}); err != nil {
		t.Fatalf("root help failed: %v", err)
	}
	if err = run([]string{"resource", "capability", "issue", "--user", "user-a"}); err == nil {
		t.Fatalf("incomplete capability command returned %v", err)
	}
}

// TestResolveConfigPath honors the environment override used by tests and recovery.
func TestResolveConfigPath(t *testing.T) {
	var path string

	t.Setenv("LLAMA_STACK_CONFIG", "/tmp/llama-stack-test.toml")
	resolveConfigPath(&path)
	if path != "/tmp/llama-stack-test.toml" {
		t.Fatalf("unexpected config path: %s", path)
	}
}
