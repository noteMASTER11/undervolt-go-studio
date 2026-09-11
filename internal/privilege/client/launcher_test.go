package client

import (
	"context"
	"os/exec"
	"reflect"
	"testing"
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
