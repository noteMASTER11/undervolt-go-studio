package product

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestReadmeDescribesCurrentStudioOnly guards the published Studio contract,
// so restored upstream instructions cannot misdescribe the current product.
func TestReadmeDescribesCurrentStudioOnly(t *testing.T) {
	data, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	const provenance = "Forked from [Softorage/undervolt-go](https://github.com/Softorage/undervolt-go). Original history and authorship are retained."
	for _, required := range []string{
		"# Undervolt Go Studio",
		"Forked from [Softorage/undervolt-go](https://github.com/Softorage/undervolt-go)",
		"docs/images/studio-overview.png",
		"docs/images/studio-monitor.png",
		"docs/images/studio-hardware.png",
		"docs/images/studio-tune.png",
		"Temporary tuning",
		"Intel Core Ultra 9 275HX",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("README missing %q", required)
		}
	}
	for _, obsolete := range []string{"softorage.github.io/undervolt-go", "UndervoltGo.png", "Persist allows"} {
		if strings.Contains(text, obsolete) {
			t.Errorf("README retains obsolete content %q", obsolete)
		}
	}
	if got := strings.Count(text, provenance); got != 1 {
		t.Errorf("README provenance paragraph count = %d, want 1", got)
	}
	for _, match := range regexp.MustCompile(`!?\[[^]]*\]\(([^)]+)\)`).FindAllStringSubmatch(text, -1) {
		target := match[1]
		if strings.Contains(target, "://") || strings.HasPrefix(target, "#") {
			continue
		}
		if _, err := os.Stat(filepath.Join("../..", target)); err != nil {
			t.Errorf("README relative link %q does not resolve: %v", target, err)
		}
	}
}
