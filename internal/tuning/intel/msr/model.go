package msr

import "github.com/noteMASTER11/undervolt-go-studio/internal/tuning/intel"

const (
	registerOCMailbox         uint32 = 0x150
	registerTemperatureTarget uint32 = 0x1a2
	registerTurboRatioLimit   uint32 = 0x1ad
)

type VoltagePlane uint32

const (
	PlaneCore  VoltagePlane = 0
	PlaneCache VoltagePlane = 2
)

type RatioLayout struct {
	Register uint32
	Entries  int
}

type VoltageLayout struct {
	Register uint32
	Planes   map[VoltagePlane]uint32
}

type Model struct {
	Identity   intel.Identity
	PCoreRatio *RatioLayout
	ECoreRatio *RatioLayout
	Voltage    *VoltageLayout
}

func LookupModel(identity intel.Identity) (Model, bool) {
	if identity.Vendor != "GenuineIntel" || identity.Family != 6 || identity.Model != 0xc6 || identity.Stepping != 2 {
		return Model{}, false
	}
	return Model{
		Identity: identity,
		PCoreRatio: &RatioLayout{
			Register: registerTurboRatioLimit,
			Entries:  8,
		},
		ECoreRatio: nil,
		Voltage: &VoltageLayout{
			Register: registerOCMailbox,
			Planes: map[VoltagePlane]uint32{
				PlaneCore:  uint32(PlaneCore),
				PlaneCache: uint32(PlaneCache),
			},
		},
	}, true
}
