package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestCodecRoundTrip(t *testing.T) {
	var wire bytes.Buffer
	writer := NewWriter(&wire)
	reader := NewReader(&wire)
	want := Message{Version: 1, RequestID: "r1", Type: TypeHello, Payload: json.RawMessage(`{"client":"dev"}`)}
	if err := writer.Write(want); err != nil {
		t.Fatal(err)
	}
	got, err := reader.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestCodecRejectsOversizedFrameBeforeAllocation(t *testing.T) {
	wire := bytes.NewBuffer([]byte{0, 16, 0, 1})
	if _, err := NewReader(wire).Read(); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestCodecRejectsZeroLengthFrame(t *testing.T) {
	if _, err := NewReader(bytes.NewReader([]byte{0, 0, 0, 0})).Read(); !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("err = %v", err)
	}
}

func TestCodecRejectsUnknownFieldsInTypedPayload(t *testing.T) {
	message := []byte(`{"version":1,"request_id":"r1","type":"hello","payload":{"client":"dev","command":"rm"}}`)
	var wire bytes.Buffer
	if err := binary.Write(&wire, binary.BigEndian, uint32(len(message))); err != nil {
		t.Fatal(err)
	}
	wire.Write(message)
	if _, err := NewReader(&wire).Read(); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("err = %v", err)
	}
}

func TestCodecRejectsIllegalDirection(t *testing.T) {
	var wire bytes.Buffer
	message := Message{Version: Version, RequestID: "r1", Type: TypeApplied, Payload: json.RawMessage(`{"transaction_id":"tx"}`)}
	if err := NewWriter(&wire).Write(message); err != nil {
		t.Fatal(err)
	}
	if _, err := NewReaderForDirection(&wire, ClientToHelper).Read(); !errors.Is(err, ErrIllegalDirection) {
		t.Fatalf("err = %v", err)
	}
}

func TestCodecRejectsProtocolAndVocabularyErrors(t *testing.T) {
	for _, message := range []Message{
		{Version: 2, RequestID: "r1", Type: TypeHello, Payload: json.RawMessage(`{"client":"dev"}`)},
		{Version: Version, Type: TypeHello, Payload: json.RawMessage(`{"client":"dev"}`)},
		{Version: Version, RequestID: "r1", Type: Type("execute_shell")},
	} {
		var wire bytes.Buffer
		if err := NewWriter(&wire).Write(message); err == nil {
			t.Fatalf("message accepted: %+v", message)
		}
	}
}

func FuzzReader(f *testing.F) {
	f.Add([]byte{0, 0, 0, 2, '{', '}'})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = NewReader(bytes.NewReader(data)).Read()
	})
}
