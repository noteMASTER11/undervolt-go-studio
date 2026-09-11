package ui

import (
	"context"
	"fmt"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"

	"github.com/noteMASTER11/undervolt-go-studio/internal/product"
	"github.com/noteMASTER11/undervolt-go-studio/internal/providers/linuxfs"
	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

// Desktop owns the Studio application lifecycle and telemetry scheduler.
type Desktop struct {
	info      product.Info
	scheduler *telemetry.Scheduler

	mu        sync.Mutex
	closeMu   sync.Mutex
	context   context.Context
	cancel    context.CancelFunc
	window    fyne.Window
	shell     *Shell
	started   bool
	closed    bool
	closeDone chan struct{}
}

// NewDesktop prepares Linux telemetry providers without touching the filesystem.
func NewDesktop(info product.Info) *Desktop {
	providers := []telemetry.Provider{
		linuxfs.NewCPUFreq("/"),
		linuxfs.NewHWMon("/"),
		linuxfs.NewProcStat("/"),
	}
	return &Desktop{
		info:      info,
		scheduler: telemetry.NewScheduler(providers, telemetry.SchedulerOptions{}),
		closeDone: make(chan struct{}),
	}
}

// Build constructs a visible-ready window without starting discovery or polling.
func (d *Desktop) Build(application fyne.App) fyne.Window {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.window != nil {
		return d.window
	}

	d.context, d.cancel = context.WithCancel(context.Background())
	d.shell = NewShell(d.info, d.scheduler)
	d.window = application.NewWindow(d.info.Name)
	d.window.Resize(fyne.NewSize(1280, 760))
	d.window.SetContent(d.shell.Object())
	window := d.window
	d.window.SetCloseIntercept(func() {
		beginWindowClose(d.Close, fyne.Do, func() {
			window.SetCloseIntercept(nil)
			window.Close()
		}, func(err error) { dialog.ShowError(err, window) })
	})
	return d.window
}

// Run starts the desktop window immediately and discovers hardware in the background.
func (d *Desktop) Run() error {
	application := app.NewWithID(d.info.AppID)
	application.Settings().SetTheme(StudioTheme())
	window := d.Build(application)
	window.Show()
	d.start()
	application.Run()
	return d.Close()
}

func (d *Desktop) start() {
	d.mu.Lock()
	if d.started || d.closed {
		d.mu.Unlock()
		return
	}
	d.started = true
	ctx := d.context
	shell := d.shell
	d.mu.Unlock()

	d.scheduler.Start(ctx)
	go func() {
		catalog, err := d.scheduler.Discover(ctx)
		if ctx.Err() != nil {
			return
		}
		status := fmt.Sprintf("%d metrics available", len(catalog.Metrics))
		if err != nil {
			status = fmt.Sprintf("Discovery completed with warnings: %v", err)
		}
		fyne.Do(func() {
			shell.SetCatalog(catalog)
			shell.SetStatus(status)
		})
	}()
}

// Close stops subscriptions and cancels all provider work. It is safe to call repeatedly.
func (d *Desktop) Close() error {
	d.closeMu.Lock()
	defer d.closeMu.Unlock()
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	if d.closeDone == nil {
		d.closeDone = make(chan struct{})
	}
	done := d.closeDone
	cancel := d.cancel
	shell := d.shell
	d.mu.Unlock()

	if shell != nil {
		if err := shell.CloseTune(); err != nil {
			return err
		}
		shell.Deactivate()
	}
	if cancel != nil {
		cancel()
	}
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
	close(done)
	return nil
}

func beginWindowClose(work func() error, dispatch func(func()), finish func(), failed func(error)) {
	go func() {
		if err := work(); err != nil {
			dispatch(func() { failed(err) })
			return
		}
		dispatch(finish)
	}()
}
