package pages

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/noteMASTER11/undervolt-go-studio/internal/hardware"
	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

func readHardwareOverview(ctx context.Context, catalog telemetry.Catalog) HardwareSummary {
	physicalCores, logicalCPUs := cpuTopology("/sys/devices/system/cpu")
	if logicalCPUs == 0 {
		logicalCPUs = runtime.NumCPU()
	}
	coreText := fmt.Sprintf("%d logical processors", logicalCPUs)
	if physicalCores > 0 {
		coreText = fmt.Sprintf("%d cores · %d logical processors", physicalCores, logicalCPUs)
	}

	totalMemory, availableMemory := readMemory("/proc/meminfo")
	usedMemory := totalMemory - availableMemory
	memory := humanBytes(totalMemory)
	memoryDetails := []string(nil)
	if totalMemory > 0 {
		memoryDetails = []string{fmt.Sprintf("%s used · %s available", humanBytes(usedMemory), humanBytes(availableMemory))}
	}

	overview := HardwareSummary{
		Machine:       machineName("/sys/class/dmi/id"),
		OS:            operatingSystemName("/etc/os-release"),
		Kernel:        kernelName(),
		CPU:           cpuModel("/proc/cpuinfo"),
		CPUDetails:    []string{coreText},
		Graphics:      graphicsAdapters(ctx, catalog),
		Memory:        memory,
		MemoryDetails: memoryDetails,
		Storage:       storageDevices("/sys/block"),
	}
	if overview.Machine == "" {
		overview.Machine = "Linux computer"
	}
	if overview.OS == "" {
		overview.OS = "Linux"
	}
	if overview.CPU == "" {
		overview.CPU = "Processor details unavailable"
	}
	if overview.Memory == "0 B" {
		overview.Memory = "Memory details unavailable"
	}
	return overview
}

func machineName(root string) string {
	vendor := readTrimmed(filepath.Join(root, "sys_vendor"))
	product := readTrimmed(filepath.Join(root, "product_name"))
	parts := uniqueUseful([]string{vendor, product})
	return strings.Join(parts, " ")
}

func operatingSystemName(path string) string {
	values := parseKeyValues(readTrimmed(path))
	if pretty := values["PRETTY_NAME"]; pretty != "" {
		return pretty
	}
	return values["NAME"]
}

func kernelName() string {
	var value syscall.Utsname
	if syscall.Uname(&value) != nil {
		return runtime.GOARCH
	}
	return fmt.Sprintf("Linux %s · %s", utsString(value.Release[:]), runtime.GOARCH)
}

func cpuModel(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if ok && (strings.TrimSpace(key) == "model name" || strings.TrimSpace(key) == "Hardware") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cpuTopology(root string) (int, int) {
	paths, _ := filepath.Glob(filepath.Join(root, "cpu[0-9]*"))
	cores := make(map[string]struct{})
	logical := 0
	for _, path := range paths {
		name := filepath.Base(path)
		if _, err := strconv.Atoi(strings.TrimPrefix(name, "cpu")); err != nil {
			continue
		}
		logical++
		core := readTrimmed(filepath.Join(path, "topology", "core_id"))
		packageID := readTrimmed(filepath.Join(path, "topology", "physical_package_id"))
		if core != "" {
			cores[packageID+":"+core] = struct{}{}
		}
	}
	return len(cores), logical
}

func readMemory(path string) (uint64, uint64) {
	values := parseKeyValues(readTrimmed(path))
	return parseKilobytes(values["MemTotal"]), parseKilobytes(values["MemAvailable"])
}

func parseKilobytes(value string) uint64 {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return 0
	}
	number, _ := strconv.ParseUint(fields[0], 10, 64)
	return number * 1024
}

func graphicsAdapters(ctx context.Context, catalog telemetry.Catalog) []string {
	output, err := commandOutput(ctx, 1<<20, "lspci", "-mm")
	if err == nil {
		if adapters := parseLSPCIWithDriver(string(output)); len(adapters) > 0 {
			return adapters
		}
	}
	var adapters []string
	for _, device := range catalog.Devices {
		if device.Kind != hardware.KindGPU {
			continue
		}
		adapters = append(adapters, strings.TrimSpace(strings.Join(uniqueUseful([]string{device.Vendor, device.Name}), " ")))
	}
	return uniqueUseful(adapters)
}

func commandOutput(ctx context.Context, limit int64, name string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		return nil, err
	}
	output, readErr := io.ReadAll(io.LimitReader(stdout, limit))
	waitErr := command.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if waitErr != nil {
		return nil, waitErr
	}
	return output, nil
}

func parseLSPCI(output string) []string {
	return parseLSPCIOutput(output, func(string) string { return "" })
}

func parseLSPCIWithDriver(output string) []string {
	return parseLSPCIOutput(output, pciDriver)
}

func parseLSPCIOutput(output string, driverForAddress func(string) string) []string {
	var adapters []string
	for _, line := range strings.Split(output, "\n") {
		fields := quotedFields(line)
		if len(fields) < 4 {
			continue
		}
		class := fields[1]
		if class != "VGA compatible controller" && class != "3D controller" && class != "Display controller" {
			continue
		}
		vendor := cleanVendor(fields[2])
		name := strings.TrimSpace(fields[3])
		driver := driverForAddress(fields[0])
		adapter := strings.TrimSpace(strings.Join(uniqueUseful([]string{vendor, name}), " "))
		if driver != "" {
			adapter += " · " + driver + " driver"
		}
		adapters = append(adapters, adapter)
	}
	return uniqueUseful(adapters)
}

func quotedFields(line string) []string {
	var fields []string
	for len(line) > 0 {
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if line[0] != '"' {
			index := strings.IndexByte(line, ' ')
			if index < 0 {
				fields = append(fields, line)
				break
			}
			fields = append(fields, line[:index])
			line = line[index+1:]
			continue
		}
		end := strings.IndexByte(line[1:], '"')
		if end < 0 {
			break
		}
		fields = append(fields, line[1:end+1])
		line = line[end+2:]
	}
	return fields
}

func pciDriver(address string) string {
	target, err := filepath.EvalSymlinks(filepath.Join("/sys/bus/pci/devices", "0000:"+address, "driver"))
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

func cleanVendor(value string) string {
	replacer := strings.NewReplacer(" Corporation", "", " Inc.", "", " Co., Ltd.", "", " Co., Ltd", "")
	return strings.TrimSpace(replacer.Replace(value))
}

func storageDevices(root string) []string {
	entries, _ := os.ReadDir(root)
	var devices []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "zram") || strings.HasPrefix(name, "dm-") {
			continue
		}
		base := filepath.Join(root, name)
		sectors, _ := strconv.ParseUint(readTrimmed(filepath.Join(base, "size")), 10, 64)
		if sectors == 0 {
			continue
		}
		vendor := readTrimmed(filepath.Join(base, "device", "vendor"))
		model := readTrimmed(filepath.Join(base, "device", "model"))
		label := strings.Join(uniqueUseful([]string{vendor, model}), " ")
		if label == "" {
			label = name
		}
		devices = append(devices, fmt.Sprintf("%s · %s", label, humanBytes(sectors*512)))
	}
	sort.Strings(devices)
	return devices
}

func humanBytes(value uint64) string {
	const unit = uint64(1024)
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	suffixes := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	size := float64(value)
	index := -1
	for size >= float64(unit) && index < len(suffixes)-1 {
		size /= float64(unit)
		index++
	}
	if size >= 10 {
		return fmt.Sprintf("%.0f %s", size, suffixes[index])
	}
	return fmt.Sprintf("%.1f %s", size, suffixes[index])
}

func parseKeyValues(content string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			key, value, ok = strings.Cut(line, ":")
		}
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}
		values[strings.TrimSpace(key)] = value
	}
	return values
}

func readTrimmed(path string) string {
	value, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(value))
}

func uniqueUseful(values []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || value == "To Be Filled By O.E.M." || value == "Default string" {
			continue
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func utsString(value []int8) string {
	bytes := make([]byte, 0, len(value))
	for _, character := range value {
		if character == 0 {
			break
		}
		bytes = append(bytes, byte(character))
	}
	return string(bytes)
}
