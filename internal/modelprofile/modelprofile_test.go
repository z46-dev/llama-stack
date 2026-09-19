package modelprofile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderSelectsOnlyCompatibleModels covers catalog matching and native preset generation.
func TestRenderSelectsOnlyCompatibleModels(t *testing.T) {
	var (
		root     string = t.TempDir()
		models   string = filepath.Join(root, "models")
		template string = filepath.Join(root, "smollm3.jinja")
		catalog  string = filepath.Join(root, "profiles.toml")
		preset   string = filepath.Join(root, "generated", "models.ini")
		contents []byte
		matched  int
		err      error
	)

	if err = os.MkdirAll(models, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SmolLM3-3B-128K-Q4_K_M.gguf", "Nemotron-Q4_0.gguf"} {
		if err = os.WriteFile(filepath.Join(models, name), nil, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(template, []byte("template"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(catalog, []byte(`[[profile]]
name = "smollm3-tools"
model_glob = "SmolLM3-3B-128K-*.gguf"
chat_template_file = "`+template+`"
chat_template_kwargs = { enable_thinking = false }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if matched, err = Render(catalog, models, preset); err != nil {
		t.Fatal(err)
	}
	if matched != 1 {
		t.Fatalf("expected one matched model, got %d", matched)
	}
	if contents, err = os.ReadFile(preset); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "[SmolLM3-3B-128K-Q4_K_M]") ||
		!strings.Contains(string(contents), `chat-template-kwargs = {"enable_thinking":false}`) ||
		strings.Contains(string(contents), "Nemotron") {
		t.Fatalf("unexpected generated preset:\n%s", contents)
	}
}
