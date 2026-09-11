package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/noteMASTER11/undervolt-go-studio/internal/privilege/protocol"
	"github.com/noteMASTER11/undervolt-go-studio/internal/privilege/session"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel/msr"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel/policy"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel/powercap"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel/thermal"
	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/sysfs"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 3 || os.Args[1] != "--session" || os.Args[2] != "--protocol=1" {
		return errors.New("helper: expected exactly --session --protocol=1")
	}
	if os.Geteuid() != 0 {
		return errors.New("helper: root privileges are required")
	}
	if info, err := os.Stdin.Stat(); err != nil {
		return err
	} else if info.Mode()&os.ModeCharDevice != 0 {
		return errors.New("helper: interactive TTY sessions are not allowed")
	}

	identity, err := intel.DetectIdentity()
	if err != nil {
		return err
	}
	store := sysfs.RootStore{Root: "/"}
	topology, topologyErr := intel.DetectTopology(store, intel.CPUIDOnCPU)
	pCoreCount := 0
	if topologyErr == nil {
		for _, core := range topology {
			if core.Type == intel.CorePerformance {
				pCoreCount++
			}
		}
	}
	machineID := fmt.Sprintf("%s-%d-%x-%d", identity.Vendor, identity.Family, identity.Model, identity.Stepping)
	drivers := []tuning.Driver{
		powercap.New(store),
		policy.New(store),
		thermal.New(store),
		msr.NewDriver(msr.LinuxDevice{}, identity, pCoreCount),
	}
	discoverer := tuning.NewDiscoverer(machineID, drivers...)
	bootIDRaw, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return fmt.Errorf("helper: read boot ID: %w", err)
	}
	engine := tuning.NewEngine(
		tuning.CapabilitySet{MachineID: machineID},
		tuning.FileRecoveryStore{},
		drivers...,
	)
	engine.BootID = string(bytesTrimSpace(bootIDRaw))
	backend := &helperBackend{discoverer: discoverer, engine: engine}
	server := session.NewServer(backend)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	return server.Run(
		ctx,
		protocol.NewReaderForDirection(os.Stdin, protocol.ClientToHelper),
		protocol.NewWriterForDirection(os.Stdout, protocol.HelperToClient),
	)
}

type helperBackend struct {
	discoverer *tuning.Discoverer
	engine     *tuning.Engine
}

func (backend *helperBackend) Recover(ctx context.Context) (tuning.RecoveryResult, error) {
	return backend.engine.Recover(ctx)
}

func (backend *helperBackend) Probe(ctx context.Context) (tuning.CapabilitySet, error) {
	var final tuning.DiscoveryResult
	for result := range backend.discoverer.Discover(ctx) {
		final = result
	}
	if !final.Complete {
		return tuning.CapabilitySet{}, errors.Join(ctx.Err(), final.Err)
	}
	backend.engine.Capabilities = final.Set
	return final.Set, final.Err
}

func (backend *helperBackend) Apply(ctx context.Context, changes tuning.ChangeSet) (session.Transaction, tuning.ValidationResult, error) {
	return backend.engine.Apply(ctx, changes)
}

func bytesTrimSpace(value []byte) []byte {
	start, end := 0, len(value)
	for start < end && (value[start] == ' ' || value[start] == '\n' || value[start] == '\r' || value[start] == '\t') {
		start++
	}
	for end > start && (value[end-1] == ' ' || value[end-1] == '\n' || value[end-1] == '\r' || value[end-1] == '\t') {
		end--
	}
	return value[start:end]
}
