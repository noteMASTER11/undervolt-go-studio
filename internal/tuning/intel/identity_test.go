package intel

import (
	"reflect"
	"testing"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/testkit"
)

func TestDecodeSignatureC0662(t *testing.T) {
	got := DecodeSignature(0x000c0662)
	if got.Family != 6 || got.Model != 0xc6 || got.Stepping != 2 {
		t.Fatalf("identity = %+v", got)
	}
}

func TestDecodeSignatureUsesExtendedFamilyAndModel(t *testing.T) {
	got := DecodeSignature((1 << 20) | (3 << 16) | (0xf << 8) | (5 << 4) | 7)
	if got.Family != 0x10 || got.Model != 0x35 || got.Stepping != 7 {
		t.Fatalf("identity = %+v", got)
	}
}

func TestCoreTypeFromLeaf1A(t *testing.T) {
	if got := CoreTypeFromLeaf1A(0x40000000); got != CorePerformance {
		t.Fatalf("performance core decoded as %q", got)
	}
	if got := CoreTypeFromLeaf1A(0x20000000); got != CoreEfficiency {
		t.Fatalf("efficiency core decoded as %q", got)
	}
	if got := CoreTypeFromLeaf1A(0); got != CoreUnknown {
		t.Fatalf("unknown core decoded as %q", got)
	}
}

func TestDetectTopologyReadsOnlineCPUsAndPreservesUnknownCoreType(t *testing.T) {
	store := testkit.NewMemoryStore(map[string]string{
		"sys/devices/system/cpu/online":                        "0-2,4\n",
		"sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_max_freq": "5700000\n",
		"sys/devices/system/cpu/cpu1/cpufreq/cpuinfo_max_freq": "5700000\n",
		"sys/devices/system/cpu/cpu2/cpufreq/cpuinfo_max_freq": "4600000\n",
	})
	leaves := map[int]uint32{0: 0x40000000, 1: 0x40000000, 2: 0x20000000, 4: 0}
	query := func(cpu int, eax, ecx uint32) (a, b, c, d uint32, err error) {
		if eax == 0 {
			return 0x1a, 0, 0, 0, nil
		}
		return leaves[cpu], 0, 0, 0, nil
	}

	got, err := DetectTopology(store, query)
	if err != nil {
		t.Fatal(err)
	}
	want := []Core{
		{CPU: 0, Type: CorePerformance, MaxFrequencyKHz: 5700000},
		{CPU: 1, Type: CorePerformance, MaxFrequencyKHz: 5700000},
		{CPU: 2, Type: CoreEfficiency, MaxFrequencyKHz: 4600000},
		{CPU: 4, Type: CoreUnknown},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("topology = %#v, want %#v", got, want)
	}
}
