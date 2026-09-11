package protocol

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRejectsUnboundedEchoMetadata(t *testing.T) {
	for _, message := range []Message{
		{Version: Version, RequestID: strings.Repeat("x", 4096), Type: TypeHello, Payload: json.RawMessage(`{"client":"test"}`)},
		{Version: Version, RequestID: "r", Type: TypeHello, Payload: json.RawMessage(`{"client":"` + strings.Repeat("x", 4096) + `"}`)},
	} {
		var out bytes.Buffer
		if err := NewWriter(&out).Write(message); err == nil {
			t.Fatal("unbounded echoed metadata accepted")
		}
	}
}
