package history

import "github.com/noteMASTER11/undervolt-go-studio/internal/telemetry"

// DownsampleMinMax keeps chronological extrema for each display-width bucket.
func DownsampleMinMax(samples []telemetry.Sample, pixelColumns int) []telemetry.Sample {
	if len(samples) == 0 || pixelColumns <= 0 {
		return []telemetry.Sample{}
	}
	if len(samples) <= pixelColumns*2 {
		return append([]telemetry.Sample(nil), samples...)
	}

	bucketSize := (len(samples) + pixelColumns - 1) / pixelColumns
	result := make([]telemetry.Sample, 0, pixelColumns*2)
	for start := 0; start < len(samples); start += bucketSize {
		end := start + bucketSize
		if end > len(samples) {
			end = len(samples)
		}
		minimum := start
		maximum := start
		for index := start + 1; index < end; index++ {
			if samples[index].Value < samples[minimum].Value {
				minimum = index
			}
			if samples[index].Value > samples[maximum].Value {
				maximum = index
			}
		}
		if minimum == maximum {
			result = append(result, samples[minimum])
		} else if minimum < maximum {
			result = append(result, samples[minimum], samples[maximum])
		} else {
			result = append(result, samples[maximum], samples[minimum])
		}
	}
	return result
}
