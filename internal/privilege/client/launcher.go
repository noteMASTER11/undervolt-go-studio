package client

import (
	"context"
	"errors"
	"io"
	"os/exec"
)

const helperPath = "/usr/libexec/undervolt-go-studio-helper"

var ErrAuthorizationCancelled = errors.New("authorization was cancelled")

type CommandFactory func(context.Context, string, ...string) *exec.Cmd

type PKExecLauncher struct {
	CommandFactory CommandFactory
}

type Process struct {
	command           *exec.Cmd
	Input             io.WriteCloser
	Output            io.ReadCloser
	Stderr            io.ReadCloser
	authorization     context.Context
	stopAuthorization func() bool
}

func (launcher PKExecLauncher) Start(ctx context.Context) (*Process, error) {
	factory := launcher.CommandFactory
	if factory == nil {
		factory = exec.CommandContext
	}
	// Only authorization depends on the request context; after the handshake
	// the private pipe and lease own the helper's lifetime.
	command := factory(context.WithoutCancel(ctx), "pkexec", helperPath, "--session", "--protocol=1")
	input, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		return nil, classifyLaunchError(err)
	}
	process := &Process{command: command, Input: input, Output: output, Stderr: stderr, authorization: ctx}
	process.stopAuthorization = context.AfterFunc(ctx, func() { _ = input.Close(); _ = command.Process.Kill() })
	return process, nil
}

func (process *Process) Authorized() error {
	if process.stopAuthorization != nil {
		process.stopAuthorization()
	}
	return process.authorization.Err()
}

func (process *Process) CloseInput() error { return process.Input.Close() }

func (process *Process) Wait() error {
	if process.stopAuthorization != nil {
		defer process.stopAuthorization()
	}
	err := process.command.Wait()
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && (exitError.ExitCode() == 126 || exitError.ExitCode() == 127) {
		return ErrAuthorizationCancelled
	}
	return err
}

func classifyLaunchError(err error) error {
	if errors.Is(err, context.Canceled) {
		return ErrAuthorizationCancelled
	}
	return err
}
