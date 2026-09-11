package hardware

import "strings"

// DeviceID identifies the same physical device across provider refreshes.
type DeviceID string

// Kind classifies a hardware device.
type Kind string

const (
	KindCPU Kind = "cpu"
	KindGPU Kind = "gpu"
)

// Device describes hardware without exposing provider-specific handles.
type Device struct {
	ID         DeviceID
	Kind       Kind
	Vendor     string
	Name       string
	Attributes map[string]string
}

// NewDeviceID normalizes the stable identity reported by a provider.
func NewDeviceID(kind Kind, vendor, nativeID string) DeviceID {
	normalize := func(value string) string {
		return strings.ToLower(strings.TrimSpace(value))
	}
	return DeviceID(string(kind) + ":" + normalize(vendor) + ":" + normalize(nativeID))
}
