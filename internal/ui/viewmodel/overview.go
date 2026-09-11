package viewmodel

import (
	"strings"
	"time"

	"github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"
)

type Overview struct {
	monitor *Monitor
}

func NewOverview(source SubscriptionSource, catalog telemetry.Catalog, interval time.Duration) *Overview {
	monitor := NewMonitor(source, interval)
	monitor.SetMetricIDs(summaryMetricIDs(catalog))
	return &Overview{monitor: monitor}
}

func (o *Overview) Activate()                                 { o.monitor.Activate() }
func (o *Overview) Deactivate()                               { o.monitor.Deactivate() }
func (o *Overview) State() MonitorState                       { return o.monitor.State() }
func (o *Overview) SetListener(l StateListener[MonitorState]) { o.monitor.SetListener(l) }

func (o *Overview) SetCatalog(catalog telemetry.Catalog) {
	o.monitor.SetMetricIDs(summaryMetricIDs(catalog))
}

func summaryMetricIDs(catalog telemetry.Catalog) []telemetry.MetricID {
	var utilization, temperature telemetry.MetricID
	var frequencies []telemetry.MetricID
	for _, descriptor := range catalog.Metrics {
		id := strings.ToLower(string(descriptor.ID))
		label := strings.ToLower(descriptor.Label)
		switch {
		case utilization == "" && (id == "cpu.utilization" || (strings.Contains(label, "cpu") && descriptor.Unit == "%")):
			utilization = descriptor.ID
		case temperature == "" && descriptor.Unit == "°C" && (strings.Contains(label, "package") || strings.Contains(id, "package")):
			temperature = descriptor.ID
		case descriptor.Unit == "MHz" && strings.Contains(id, "cpu"):
			frequencies = append(frequencies, descriptor.ID)
		}
	}
	result := make([]telemetry.MetricID, 0, 2+len(frequencies))
	for _, metricID := range []telemetry.MetricID{utilization, temperature} {
		if metricID != "" {
			result = append(result, metricID)
		}
	}
	result = append(result, frequencies...)
	return result
}
