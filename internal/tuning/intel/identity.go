package intel

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"

	"github.com/noteMASTER11/undervolt-go-studio/internal/tuning/sysfs"
)

type Identity struct {
	Vendor       string `json:"vendor"`
	Family       uint32 `json:"family"`
	Model        uint32 `json:"model"`
	Stepping     uint32 `json:"stepping"`
	MaxBasicLeaf uint32 `json:"max_basic_leaf"`
}

type CoreType string

const (
	CoreUnknown     CoreType = "unknown"
	CoreEfficiency  CoreType = "efficiency"
	CorePerformance CoreType = "performance"
)

type Core struct {
	CPU             int      `json:"cpu"`
	Type            CoreType `json:"type"`
	MaxFrequencyKHz int64    `json:"max_frequency_khz,omitempty"`
	Diagnostic      string   `json:"diagnostic,omitempty"`
}

type CPUIDQuery func(cpu int, eax, ecx uint32) (a, b, c, d uint32, err error)

func DecodeSignature(signature uint32) Identity {
	stepping := signature & 0xf
	baseModel := (signature >> 4) & 0xf
	baseFamily := (signature >> 8) & 0xf
	extendedModel := (signature >> 16) & 0xf
	extendedFamily := (signature >> 20) & 0xff

	family := baseFamily
	if baseFamily == 0xf {
		family += extendedFamily
	}
	model := baseModel
	if baseFamily == 0x6 || baseFamily == 0xf {
		model += extendedModel << 4
	}
	return Identity{Family: family, Model: model, Stepping: stepping}
}

func CoreTypeFromLeaf1A(eax uint32) CoreType {
	switch eax >> 24 {
	case 0x40:
		return CorePerformance
	case 0x20:
		return CoreEfficiency
	default:
		return CoreUnknown
	}
}

func DetectIdentity() (Identity, error) {
	maxLeaf, ebx, ecx, edx := cpuid(0, 0)
	vendorBytes := make([]byte, 12)
	binary.LittleEndian.PutUint32(vendorBytes[0:4], ebx)
	binary.LittleEndian.PutUint32(vendorBytes[4:8], edx)
	binary.LittleEndian.PutUint32(vendorBytes[8:12], ecx)
	if maxLeaf < 1 {
		return Identity{Vendor: string(vendorBytes), MaxBasicLeaf: maxLeaf}, fmt.Errorf("intel: CPUID leaf 1 is unavailable")
	}
	signature, _, _, _ := cpuid(1, 0)
	identity := DecodeSignature(signature)
	identity.Vendor = string(vendorBytes)
	identity.MaxBasicLeaf = maxLeaf
	return identity, nil
}

func DetectTopology(store sysfs.Store, query CPUIDQuery) ([]Core, error) {
	online, err := store.Read("sys/devices/system/cpu/online")
	if err != nil {
		return nil, fmt.Errorf("intel: read online CPUs: %w", err)
	}
	ids, err := parseCPUList(strings.TrimSpace(string(online)))
	if err != nil {
		return nil, err
	}
	cores := make([]Core, 0, len(ids))
	for _, cpu := range ids {
		core := Core{CPU: cpu}
		maxLeaf, _, _, _, queryErr := query(cpu, 0, 0)
		if queryErr != nil {
			core.Diagnostic = queryErr.Error()
		} else if maxLeaf >= 0x1a {
			eax, _, _, _, leafErr := query(cpu, 0x1a, 0)
			if leafErr != nil {
				core.Diagnostic = leafErr.Error()
			} else {
				core.Type = CoreTypeFromLeaf1A(eax)
			}
		}
		frequencyPath := fmt.Sprintf("sys/devices/system/cpu/cpu%d/cpufreq/cpuinfo_max_freq", cpu)
		if rawFrequency, readErr := store.Read(frequencyPath); readErr == nil {
			if frequency, parseErr := strconv.ParseInt(strings.TrimSpace(string(rawFrequency)), 10, 64); parseErr == nil {
				core.MaxFrequencyKHz = frequency
			}
		}
		cores = append(cores, core)
	}
	return cores, nil
}

func parseCPUList(value string) ([]int, error) {
	if value == "" {
		return nil, fmt.Errorf("intel: empty online CPU list")
	}
	var cpus []int
	for _, part := range strings.Split(value, ",") {
		bounds := strings.SplitN(strings.TrimSpace(part), "-", 2)
		first, err := strconv.Atoi(bounds[0])
		if err != nil || first < 0 {
			return nil, fmt.Errorf("intel: invalid CPU list %q", value)
		}
		last := first
		if len(bounds) == 2 {
			last, err = strconv.Atoi(bounds[1])
			if err != nil || last < first {
				return nil, fmt.Errorf("intel: invalid CPU range %q", part)
			}
		}
		for cpu := first; cpu <= last; cpu++ {
			cpus = append(cpus, cpu)
		}
	}
	return cpus, nil
}
