package linuxfs

import (
	"context"
	"errors"
	"testing"

	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

func TestProvidersRespectCanceledDiscoveryContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	providers := []telemetry.Provider{NewCPUFreq(t.TempDir()), NewHWMon(t.TempDir()), NewProcStat(t.TempDir())}
	for _, provider := range providers {
		if _, err := provider.Discover(ctx); !errors.Is(err, context.Canceled) {
			t.Errorf("%s discovery error = %v, want context canceled", provider.ID(), err)
		}
	}
}
