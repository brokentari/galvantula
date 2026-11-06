package standards

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/brokentari/galvantula/internal/guardrails"
)

func TestLockMatchesVendoredFiles(t *testing.T) {
	raw, err := vendor.ReadFile("vendor/lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lock []struct{ Name, Path, Sha256 string }
	if err := json.Unmarshal(raw, &lock); err != nil {
		t.Fatal(err)
	}
	for _, f := range lock {
		data, err := vendor.ReadFile(path.Join("vendor", f.Name, f.Path))
		if err != nil {
			t.Fatal(err)
		}
		if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != f.Sha256 {
			t.Errorf("%s/%s does not match lock.json; run standards/vendor.sh", f.Name, f.Path)
		}
	}
}

func TestRender(t *testing.T) {
	block, err := Render("1.27")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"It checks 25 of the guidelines. No tool checks these 29",
		"- `json_v2` (Go 1.27):",
		"### Ponytail, lazy senior dev mode",
		"### Orwell Writing",
		"#### Core Rules",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("block does not contain %q", want)
		}
	}
	if strings.Contains(block, "name: orwell-writing") {
		t.Error("block still holds the skill's front matter")
	}

	older, err := Render("1.25")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(older, "json_v2") {
		t.Error("Go 1.25 block lists a Go 1.27 guideline")
	}
}

// AGENTS.md must hold the block for the repo's Go version. If this fails,
// run: go run ./cmd/factory standards -o AGENTS.md
func TestAgentsFileIsCurrent(t *testing.T) {
	g, err := guardrails.Load("../guardrails.yaml")
	if err != nil {
		t.Fatal(err)
	}
	block, err := Render(g.Go.Version)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile("../AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := Replace(doc, block)
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != string(doc) {
		t.Fatal("AGENTS.md is out of date; run: go run ./cmd/factory standards -o AGENTS.md")
	}
}
