// Command ui-snapshot renders Studio pages without opening a desktop window.
package main

import (
	"flag"
	"fmt"
	"image/png"
	"os"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/noteMASTER11/undervolt-go-studio/internal/product"
	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
	"github.com/noteMASTER11/undervolt-go-studio/internal/ui"
)

func main() {
	page := flag.String("page", "hardware", "page ID to render")
	output := flag.String("output", "ui-snapshot.png", "PNG output path")
	width := flag.Int("width", 1600, "canvas width")
	height := flag.Int("height", 1000, "canvas height")
	flag.Parse()
	done := make(chan error, 1)
	go func() { done <- render(*page, *output, *width, *height) }()
	if err := <-done; err != nil {
		fatal(err)
	}
}

func render(page, output string, width, height int) error {
	application := test.NewApp()
	defer application.Quit()
	application.Settings().SetTheme(ui.StudioTheme())

	scheduler := telemetry.NewScheduler(nil, telemetry.SchedulerOptions{})
	shell := ui.NewShell(product.Current("dev"), scheduler)
	defer shell.Deactivate()
	if err := shell.Select(page); err != nil {
		return err
	}
	shell.SetStatus("Headless UI preview")

	window := test.NewWindow(shell.Object())
	defer window.Close()
	window.Resize(fyne.NewSize(float32(width), float32(height)))
	time.Sleep(2 * time.Second)

	file, err := os.Create(output)
	if err != nil {
		return err
	}
	if err := png.Encode(file, window.Canvas().Capture()); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return nil
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
