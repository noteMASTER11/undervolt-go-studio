package client

import (
	"context"
	"io"
	"os/exec"
	"reflect"
	"testing"
	"time"
)

func TestPKExecLauncherUsesFixedCommandWithoutShell(t *testing.T) {
	var recorded []string
	launcher := PKExecLauncher{CommandFactory: func(ctx context.Context, name string, args ...string) *exec.Cmd {
		recorded = append([]string{name}, args...)
		return exec.CommandContext(ctx, "true")
	}}
	process, err := launcher.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = process.CloseInput()
	_ = process.Wait()
	want := []string{"pkexec", "/usr/libexec/undervolt-go-studio-helper", "--session", "--protocol=1"}
	if !reflect.DeepEqual(recorded, want) {
		t.Fatalf("args = %q", recorded)
	}
}

func TestAuthorizedSessionOutlivesApplyRequestContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	launcher := PKExecLauncher{CommandFactory: func(ctx context.Context, _ string, _ ...string) *exec.Cmd { return exec.CommandContext(ctx, "cat") }}
	process, err := launcher.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer process.CloseInput()
	if err := process.Authorized(); err != nil {
		t.Fatal(err)
	}
	cancel()
	done := make(chan error, 1)
	go func() {
		_, err := process.Input.Write([]byte("alive\n"))
		if err == nil {
			buffer := make([]byte, 6)
			_, err = io.ReadFull(process.Output, buffer)
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("completed Apply context killed active helper: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("session did not respond")
	}
	process.CloseInput()
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
}
