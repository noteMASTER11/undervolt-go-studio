package main

import (
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIntel275HXScenarioRendersTuneSnapshot(t *testing.T) {
	output := filepath.Join(t.TempDir(), "tune-275hx.png")
	command := exec.Command("go", "run", ".",
		"--page", "tune",
		"--scenario", "intel-275hx",
		"--output", output,
		"--width", "800",
		"--height", "600",
	)
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("render Intel 275HX Tune snapshot: %v\n%s", err, result)
	}

	file, err := os.Open(output)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer file.Close()
	image, err := png.Decode(file)
	if err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if got, want := image.Bounds().Dx(), 800; got != want {
		t.Fatalf("snapshot width = %d, want %d", got, want)
	}
	if got, want := image.Bounds().Dy(), 600; got != want {
		t.Fatalf("snapshot height = %d, want %d", got, want)
	}
}
