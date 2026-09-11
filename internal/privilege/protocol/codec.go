package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

var (
	ErrFrameTooLarge    = errors.New("protocol frame is too large")
	ErrInvalidFrame     = errors.New("invalid protocol frame")
	ErrInvalidMessage   = errors.New("invalid protocol message")
	ErrInvalidPayload   = errors.New("invalid protocol payload")
	ErrIllegalDirection = errors.New("message is illegal for this direction")
)

type Reader struct {
	reader    io.Reader
	direction Direction
}

func NewReader(reader io.Reader) *Reader {
	return NewReaderForDirection(reader, AnyDirection)
}

func NewReaderForDirection(reader io.Reader, direction Direction) *Reader {
	return &Reader{reader: reader, direction: direction}
}

func (reader *Reader) Read() (Message, error) {
	var sizeBuffer [4]byte
	if _, err := io.ReadFull(reader.reader, sizeBuffer[:]); err != nil {
		return Message{}, err
	}
	size := binary.BigEndian.Uint32(sizeBuffer[:])
	if size == 0 {
		return Message{}, ErrInvalidFrame
	}
	if size > MaxFrameSize {
		return Message{}, ErrFrameTooLarge
	}
	frame := make([]byte, int(size))
	if _, err := io.ReadFull(reader.reader, frame); err != nil {
		return Message{}, err
	}
	var message Message
	if err := decodeStrict(frame, &message); err != nil {
		return Message{}, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	if err := validateMessage(message, reader.direction); err != nil {
		return Message{}, err
	}
	return message, nil
}

type Writer struct {
	writer    io.Writer
	direction Direction
}

func NewWriter(writer io.Writer) *Writer {
	return NewWriterForDirection(writer, AnyDirection)
}

func NewWriterForDirection(writer io.Writer, direction Direction) *Writer {
	return &Writer{writer: writer, direction: direction}
}

func (writer *Writer) Write(message Message) error {
	if err := validateMessage(message, writer.direction); err != nil {
		return err
	}
	frame, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	if len(frame) == 0 {
		return ErrInvalidFrame
	}
	if len(frame) > MaxFrameSize {
		return ErrFrameTooLarge
	}
	var sizeBuffer [4]byte
	binary.BigEndian.PutUint32(sizeBuffer[:], uint32(len(frame)))
	if _, err := writer.writer.Write(sizeBuffer[:]); err != nil {
		return err
	}
	_, err = writer.writer.Write(frame)
	return err
}

func validateMessage(message Message, direction Direction) error {
	if message.Version != Version {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidMessage, message.Version)
	}
	if message.RequestID == "" {
		return fmt.Errorf("%w: empty request ID", ErrInvalidMessage)
	}
	if !knownType(message.Type) {
		return fmt.Errorf("%w: unknown type %q", ErrInvalidMessage, message.Type)
	}
	if !validForDirection(message.Type, direction) {
		return fmt.Errorf("%w: %s", ErrIllegalDirection, message.Type)
	}
	if err := validatePayload(message.Type, message.Payload); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	return nil
}

func validatePayload(messageType Type, payload json.RawMessage) error {
	if len(payload) == 0 {
		switch messageType {
		case TypeProbe, TypeClose:
			return nil
		default:
			return errors.New("payload is required")
		}
	}
	var destination any
	switch messageType {
	case TypeHello:
		destination = &HelloPayload{}
	case TypeProbe, TypeClose:
		destination = &ProbePayload{}
	case TypeBegin:
		destination = &BeginPayload{}
	case TypeRenew, TypeRevert:
		destination = &TransactionPayload{}
	case TypeCapabilities, TypeReviewChanged:
		destination = &CapabilitiesPayload{}
	case TypeProgress, TypeRollbackProgress:
		destination = &ProgressPayload{}
	case TypeApplied:
		destination = &AppliedPayload{}
	case TypeFailed, TypeRollbackComplete, TypeRollbackIncomplete:
		destination = &FailurePayload{}
	default:
		return fmt.Errorf("unknown type %q", messageType)
	}
	return decodeStrict(payload, destination)
}

func decodeStrict(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}
