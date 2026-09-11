package telemetry

func deliverLatest(ch chan Frame, frame Frame) (dropped bool) {
	select {
	case ch <- frame:
		return false
	default:
	}
	select {
	case <-ch:
		dropped = true
	default:
	}
	select {
	case ch <- frame:
	default:
		dropped = true
	}
	return dropped
}
