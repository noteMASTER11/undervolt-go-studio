package product

// Info contains the identity displayed by Studio frontends and reports.
type Info struct {
	Name    string
	AppID   string
	Version string
}

// Current returns the canonical product identity for a build version.
func Current(version string) Info {
	return Info{
		Name:    "Undervolt Go Studio",
		AppID:   "io.github.notemaster11.UndervoltGoStudio",
		Version: version,
	}
}
