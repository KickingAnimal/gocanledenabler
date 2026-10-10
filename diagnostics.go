package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	gocan "github.com/roffe/gocan/v2"
	"github.com/roffe/gocan/v2/gmlan"
)

var diagnosticMu sync.Mutex

type diagnosticTarget struct {
	name       string
	requestID  uint32
	responseID uint32
	did        byte
}

var (
	uecTarget = diagnosticTarget{name: "UEC", requestID: 0x24F, responseID: 0x64F, did: 0x4D}
	recTarget = diagnosticTarget{name: "REC", requestID: 0x249, responseID: 0x649, did: 0x45}
)

func parseCANID(value string) (uint32, error) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(value, "0x"), "0X"))
	parsed, err := strconv.ParseUint(value, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid CAN ID %q", value)
	}
	return uint32(parsed), nil
}

func readConfiguration(ctx context.Context, client *gmlan.Client, target diagnosticTarget, tx func(gocan.Frame)) ([]byte, error) {
	if tx != nil {
		tx(gocan.NewFrame(target.requestID, []byte{0x02, 0x1A, target.did}))
	}
	data, err := client.ReadDataByIdentifier(ctx, target.did)
	if err != nil {
		return nil, fmt.Errorf("read %s DID 0x%02X: %w", target.name, target.did, err)
	}
	if len(data) < 1 {
		return nil, fmt.Errorf("read %s DID 0x%02X: empty data", target.name, target.did)
	}
	if data[0] != 0x8B && data[0] != 0x00 {
		return nil, fmt.Errorf("read %s DID 0x%02X: unexpected lamp-check parameter 0x%02X; known values are 0x8B and 0x00", target.name, target.did, data[0])
	}
	if tx != nil && len(data)+2 > 7 {
		tx(gocan.NewFrame(target.requestID, []byte{0x30, 0x00, 0x00}))
	}
	return data, nil
}

func verifyConfiguration(ctx context.Context, client *gmlan.Client, target diagnosticTarget, tx func(gocan.Frame)) ([]byte, error) {
	const attempts = 8
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		var data []byte
		data, err = readConfiguration(ctx, client, target, tx)
		if err == nil {
			return data, nil
		}
		if !errors.Is(err, context.DeadlineExceeded) || attempt == attempts-1 {
			return nil, err
		}
		timer := time.NewTimer(350 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		}
	}
	return nil, err
}

func formatDiagnosticFrames(service byte, target diagnosticTarget, data []byte) string {
	payload := append([]byte{service, target.did}, data...)
	if len(payload) <= 7 {
		return fmt.Sprintf("%02X %X", len(payload), payload)
	}

	frames := []string{fmt.Sprintf("10 %02X %X", len(payload), payload[:6])}
	sequence := byte(0x21)
	for offset := 6; offset < len(payload); {
		end := offset + 7
		if end > len(payload) {
			end = len(payload)
		}
		frames = append(frames, fmt.Sprintf("%02X %X", sequence, payload[offset:end]))
		offset = end
		sequence = 0x20 | ((sequence + 1) & 0x0F)
	}
	return strings.Join(frames, " / ")
}

func diagnosticRequestFrames(target diagnosticTarget, service byte, data []byte) []gocan.Frame {
	payload := append([]byte{service, target.did}, data...)
	if len(payload) <= 7 {
		return []gocan.Frame{gocan.NewFrame(target.requestID, append([]byte{byte(len(payload))}, payload...))}
	}
	frames := []gocan.Frame{gocan.NewFrame(target.requestID, append([]byte{0x10, byte(len(payload))}, payload[:6]...))}
	sequence := byte(0x21)
	for offset := 6; offset < len(payload); {
		end := offset + 7
		if end > len(payload) {
			end = len(payload)
		}
		frames = append(frames, gocan.NewFrame(target.requestID, append([]byte{sequence}, payload[offset:end]...)))
		offset = end
		sequence = 0x20 | ((sequence + 1) & 0x0F)
	}
	return frames
}

func logDiagnosticRequest(tx func(gocan.Frame), target diagnosticTarget, service byte, data []byte) {
	if tx == nil {
		return
	}
	for _, frame := range diagnosticRequestFrames(target, service, data) {
		tx(frame)
	}
}

func writeConfiguration(ctx context.Context, bus *gocan.Bus, target diagnosticTarget, data []byte, tx func(gocan.Frame)) error {
	payload := append([]byte{0x3B, target.did}, data...)
	if len(payload) <= 7 {
		frame := gocan.NewFrame(target.requestID, append([]byte{byte(len(payload))}, payload...))
		if tx != nil {
			tx(frame)
		}
		response, err := bus.Request(ctx, frame, target.responseID)
		if err != nil {
			return fmt.Errorf("write response: %w", err)
		}
		if err := gmlan.CheckErr(response); err != nil {
			return err
		}
		if response.Data[1] != 0x7B {
			return fmt.Errorf("unexpected write response: %X", response.Data[:response.Length])
		}
		return nil
	}

	first := gocan.NewFrame(target.requestID, append([]byte{0x10, byte(len(payload))}, payload[:6]...))
	if tx != nil {
		tx(first)
	}
	flowControl, err := bus.Request(ctx, first, target.responseID)
	if err != nil {
		return fmt.Errorf("write flow control: %w", err)
	}
	if flowControl.Data[0] != 0x30 {
		if err := gmlan.CheckErr(flowControl); err != nil {
			return err
		}
		return fmt.Errorf("unexpected write flow control: %X", flowControl.Data[:flowControl.Length])
	}

	sequence := byte(0x21)
	for offset := 6; offset < len(payload); {
		end := offset + 7
		if end > len(payload) {
			end = len(payload)
		}
		frame := gocan.NewFrame(target.requestID, append([]byte{sequence}, payload[offset:end]...))
		if tx != nil {
			tx(frame)
		}
		if err := bus.Send(ctx, frame); err != nil {
			return fmt.Errorf("write consecutive frame: %w", err)
		}
		offset = end
		sequence = 0x20 | ((sequence + 1) & 0x0F)
	}
	return nil
}

func verifyWrittenConfiguration(current, verified, expected []byte) error {
	if verified[0] != expected[0] {
		return fmt.Errorf("parameter is 0x%02X, expected 0x%02X", verified[0], expected[0])
	}
	if !bytes.Equal(verified[1:], current[1:]) {
		return fmt.Errorf("bytes after parameter changed: before %X, after %X", current[1:], verified[1:])
	}
	if !bytes.Equal(verified, expected) {
		return fmt.Errorf("verified configuration differs from requested configuration: requested %X, received %X", expected, verified)
	}
	return nil
}
