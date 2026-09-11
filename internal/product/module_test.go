package product

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoToolReportsCanonicalModulePath(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Path}}")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(out)), "github.com/noteMASTER11/undervolt-go-studio"; got != want {
		t.Fatalf("module path = %q, want %q", got, want)
	}
}
