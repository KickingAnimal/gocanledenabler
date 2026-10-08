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

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

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
		return nil, fmt.Errorf("read %s DID 0x%02X: parameter byte is 0x%02X, expected 0x8B or 0x00", target.name, target.did, data[0])
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

func stateName(value byte) string {
	if value == 0x8B {
		return "enabled (0x8B)"
	}
	return "disabled (0x00)"
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
		err = bus.Send(ctx, frame)
		if err != nil {
			return fmt.Errorf("write consecutive frame: %w", err)
		}
		offset = end
		sequence = 0x20 | ((sequence + 1) & 0x0F)
	}
	return nil
}

func toggleConfiguration(ctx context.Context, bus *gocan.Bus, client *gmlan.Client, target diagnosticTarget, desired byte, dryRun bool, tx func(gocan.Frame)) (string, error) {
	current, err := readConfiguration(ctx, client, target, tx)
	if err != nil {
		return "", err
	}
	lines := []string{fmt.Sprintf("%s read response: %s", target.name, formatDiagnosticFrames(0x5A, target, current))}
	if current[0] == desired {
		lines = append(lines, fmt.Sprintf("%s already %s", target.name, stateName(desired)))
		return strings.Join(lines, "\n"), nil
	}

	updated := append([]byte(nil), current...)
	updated[0] = desired
	lines = append(lines, fmt.Sprintf("%s write request: %s", target.name, formatDiagnosticFrames(0x3B, target, updated)))
	if dryRun {
		lines = append(lines, fmt.Sprintf("%s DRY RUN: write not sent", target.name))
		return strings.Join(lines, "\n"), nil
	}
	if err := writeConfiguration(ctx, bus, target, updated, tx); err != nil {
		return "", fmt.Errorf("write %s DID 0x%02X: %w", target.name, target.did, err)
	}

	verified, err := verifyConfiguration(ctx, client, target, tx)
	if err != nil {
		return "", fmt.Errorf("verify %s DID 0x%02X: %w", target.name, target.did, err)
	}
	if verified[0] != desired {
		return "", fmt.Errorf("verify %s DID 0x%02X: parameter is 0x%02X, expected 0x%02X", target.name, target.did, verified[0], desired)
	}
	if !bytes.Equal(verified[1:], current[1:]) {
		return "", fmt.Errorf("verify %s DID 0x%02X: bytes after parameter changed: before %X, after %X", target.name, target.did, current[1:], verified[1:])
	}
	lines = append(lines, fmt.Sprintf("%s verified response: %s", target.name, formatDiagnosticFrames(0x5A, target, verified)))
	return strings.Join(lines, "\n"), nil
}

func newDiagnosticsTab(bus func() *gocan.Bus, tx func(gocan.Frame)) fyne.CanvasObject {
	uecRequest := widget.NewEntry()
	uecRequest.SetText("24F")
	uecResponse := widget.NewEntry()
	uecResponse.SetText("64F")
	recRequest := widget.NewEntry()
	recRequest.SetText("249")
	recResponse := widget.NewEntry()
	recResponse.SetText("649")
	dryRun := widget.NewCheck("Dry run (read only)", nil)
	dryRun.SetChecked(true)
	output := widget.NewMultiLineEntry()
	output.Wrapping = fyne.TextWrapOff
	output.TextStyle.Monospace = true
	output.SetMinRowsVisible(14)

	var responseLog []string
	appendLog := func(text string) {
		responseLog = append(responseLog, time.Now().Format("15:04:05.000")+"  "+text)
		output.SetText(strings.Join(responseLog, "\n"))
	}
	start := func(requestEntry, responseEntry *widget.Entry, target diagnosticTarget, action func(context.Context, *gocan.Bus, *gmlan.Client, diagnosticTarget) (string, error)) {
		request, err := parseCANID(requestEntry.Text)
		if err != nil {
			appendLog("ERROR: " + err.Error())
			return
		}
		response, err := parseCANID(responseEntry.Text)
		if err != nil {
			appendLog("ERROR: " + err.Error())
			return
		}
		target.requestID = request
		target.responseID = response
		appendLog("Running " + target.name + "...")
		go func() {
			connected := bus()
			if connected == nil {
				fyne.Do(func() { appendLog("ERROR: not connected") })
				return
			}
			diagnosticMu.Lock()
			defer diagnosticMu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			client := gmlan.New(connected, target.requestID, target.responseID)
			text, actionErr := action(ctx, connected, client, target)
			fyne.Do(func() {
				if actionErr != nil {
					appendLog("ERROR: " + actionErr.Error())
					return
				}
				appendLog(text)
			})
		}()
	}

	readAction := func(requestEntry, responseEntry *widget.Entry, target diagnosticTarget) func() {
		return func() {
			start(requestEntry, responseEntry, target, func(ctx context.Context, bus *gocan.Bus, client *gmlan.Client, target diagnosticTarget) (string, error) {
				data, err := readConfiguration(ctx, client, target, tx)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("%s response: %s (%s)", target.name, formatDiagnosticFrames(0x5A, target, data), stateName(data[0])), nil
			})
		}
	}
	toggleAction := func(requestEntry, responseEntry *widget.Entry, target diagnosticTarget, desired byte) func() {
		return func() {
			plannedOnly := dryRun.Checked
			start(requestEntry, responseEntry, target, func(ctx context.Context, bus *gocan.Bus, client *gmlan.Client, target diagnosticTarget) (string, error) {
				return toggleConfiguration(ctx, bus, client, target, desired, plannedOnly, tx)
			})
		}
	}

	readUEC := widget.NewButton("Read UEC", readAction(uecRequest, uecResponse, uecTarget))
	disableUEC := widget.NewButton("Disable UEC", toggleAction(uecRequest, uecResponse, uecTarget, 0x00))
	enableUEC := widget.NewButton("Enable UEC", toggleAction(uecRequest, uecResponse, uecTarget, 0x8B))
	readREC := widget.NewButton("Read REC", readAction(recRequest, recResponse, recTarget))
	disableREC := widget.NewButton("Disable REC", toggleAction(recRequest, recResponse, recTarget, 0x00))
	enableREC := widget.NewButton("Enable REC", toggleAction(recRequest, recResponse, recTarget, 0x8B))

	controls := container.NewVBox(
		dryRun,
		widget.NewLabel("UEC: DID 0x4D, parameter byte must be 0x8B or 0x00"),
		container.NewGridWithColumns(4,
			widget.NewLabel("Request ID"), uecRequest,
			widget.NewLabel("Response ID"), uecResponse,
		),
		container.NewHBox(readUEC, disableUEC, enableUEC),
		widget.NewLabel("REC: DID 0x45, multi-frame ISO-TP response"),
		container.NewGridWithColumns(4,
			widget.NewLabel("Request ID"), recRequest,
			widget.NewLabel("Response ID"), recResponse,
		),
		container.NewHBox(readREC, disableREC, enableREC),
	)
	return container.NewBorder(controls, nil, nil, nil, output)
}
