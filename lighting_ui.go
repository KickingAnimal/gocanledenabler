package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	gocan "github.com/roffe/gocan/v2"
	"github.com/roffe/gocan/v2/gmlan"
)

func newDiagnosticsTab(bus func() *gocan.Bus, tx func(gocan.Frame)) fyne.CanvasObject {
	uecRequest := widget.NewEntry()
	uecRequest.SetText("24F")
	uecResponse := widget.NewEntry()
	uecResponse.SetText("64F")
	recRequest := widget.NewEntry()
	recRequest.SetText("249")
	recResponse := widget.NewEntry()
	recResponse.SetText("649")
	newLampCheckSlider := func() (*widget.Slider, *widget.Label) {
		slider := widget.NewSlider(0, 255)
		slider.Step = 1
		value := widget.NewLabel("0x8B")
		slider.OnChanged = func(number float64) {
			value.SetText(fmt.Sprintf("0x%02X", byte(number+0.5)))
		}
		slider.SetValue(0x8B)
		return slider, value
	}
	uecLampCheck, uecLampCheckValue := newLampCheckSlider()
	recLampCheck, recLampCheckValue := newLampCheckSlider()
	dryRun := widget.NewCheck("Dry run (read only)", nil)
	dryRun.SetChecked(true)
	output := widget.NewMultiLineEntry()
	output.Wrapping = fyne.TextWrapOff
	output.TextStyle.Monospace = true
	output.SetMinRowsVisible(9)
	logScroll := container.NewVScroll(output)
	var logText string
	output.OnChanged = func(value string) {
		if value != logText {
			output.SetText(logText)
		}
	}

	uecChecks := make([]*widget.Check, 0, len(uecLightFlags))
	for _, flag := range uecLightFlags {
		uecChecks = append(uecChecks, widget.NewCheck(flag.Name, nil))
	}
	recChecks := make([]*widget.Check, 0, len(recLightFlags))
	for _, flag := range recLightFlags {
		recChecks = append(recChecks, widget.NewCheck(flag.Name, nil))
	}
	var uecLoaded, recLoaded bool
	var responseLog []string
	var logMu sync.Mutex
	appendLog := func(text string) {
		logMu.Lock()
		responseLog = append(responseLog, time.Now().Format("15:04:05.000")+"  "+text)
		value := strings.Join(responseLog, "\n")
		logMu.Unlock()
		logText = value
		output.SetText(value)
		logScroll.ScrollToBottom()
	}
	setChecks := func(checks []*widget.Check, flags []LightFlag, data []byte) {
		states := flagStates(data, flags)
		for _, check := range checks {
			check.OnChanged = nil
			check.SetChecked(states[check.Text])
		}
	}
	checkStates := func(checks []*widget.Check, flags []LightFlag) map[string]bool {
		states := make(map[string]bool, len(flags))
		for index, flag := range flags {
			states[flag.Name] = checks[index].Checked
		}
		return states
	}
	setLampCheck := func(slider *widget.Slider, value byte) {
		slider.SetValue(float64(value))
	}
	lampCheckValue := func(slider *widget.Slider) byte {
		return byte(slider.Value + 0.5)
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

	readAction := func(requestEntry, responseEntry *widget.Entry, target diagnosticTarget, checks []*widget.Check, flags []LightFlag, slider *widget.Slider, loaded *bool) func() {
		return func() {
			start(requestEntry, responseEntry, target, func(ctx context.Context, bus *gocan.Bus, client *gmlan.Client, target diagnosticTarget) (string, error) {
				data, err := readConfiguration(ctx, client, target, tx)
				if err != nil {
					return "", err
				}
				fyne.Do(func() {
					setChecks(checks, flags, data)
					setLampCheck(slider, data[0])
					*loaded = true
				})
				return fmt.Sprintf("%s response: %s", target.name, formatDiagnosticFrames(0x5A, target, data)), nil
			})
		}
	}
	applyAction := func(requestEntry, responseEntry *widget.Entry, target diagnosticTarget, checks []*widget.Check, flags []LightFlag, slider *widget.Slider, loaded *bool) func() {
		return func() {
			if !*loaded {
				appendLog("ERROR: read " + target.name + " configuration before applying changes")
				return
			}
			desired := checkStates(checks, flags)
			plannedOnly := dryRun.Checked
			start(requestEntry, responseEntry, target, func(ctx context.Context, bus *gocan.Bus, client *gmlan.Client, target diagnosticTarget) (string, error) {
				current, err := readConfiguration(ctx, client, target, tx)
				if err != nil {
					return "", err
				}
				updated, err := applyLightSettings(current, flags, desired)
				if err != nil {
					return "", err
				}
				updated[0] = lampCheckValue(slider)
				lines := []string{fmt.Sprintf("%s read response: %s", target.name, formatDiagnosticFrames(0x5A, target, current))}
				lines = append(lines, fmt.Sprintf("%s write request: %s", target.name, formatDiagnosticFrames(0x3B, target, updated)))
				if plannedOnly {
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
				if err := verifyWrittenConfiguration(current, verified, updated); err != nil {
					return "", fmt.Errorf("verify %s DID 0x%02X: %w", target.name, target.did, err)
				}
				lines = append(lines, fmt.Sprintf("%s verified response: %s", target.name, formatDiagnosticFrames(0x5A, target, verified)))
				return strings.Join(lines, "\n"), nil
			})
		}
	}

	readUEC := widget.NewButton("Read UEC", readAction(uecRequest, uecResponse, uecTarget, uecChecks, uecLightFlags, uecLampCheck, &uecLoaded))
	applyUEC := widget.NewButton("Apply UEC", applyAction(uecRequest, uecResponse, uecTarget, uecChecks, uecLightFlags, uecLampCheck, &uecLoaded))
	readREC := widget.NewButton("Read REC", readAction(recRequest, recResponse, recTarget, recChecks, recLightFlags, recLampCheck, &recLoaded))
	applyREC := widget.NewButton("Apply REC", applyAction(recRequest, recResponse, recTarget, recChecks, recLightFlags, recLampCheck, &recLoaded))

	uecPanel := container.NewVBox(
		widget.NewLabel("UEC  DID 0x4D"),
		container.NewGridWithColumns(4,
			widget.NewLabel("TX"), uecRequest,
			widget.NewLabel("RX"), uecResponse,
		),
		container.NewBorder(widget.NewLabel("Lamp-check byte"), nil, nil, uecLampCheckValue, uecLampCheck),
		container.NewGridWithColumns(3, uecChecks[0], uecChecks[1], uecChecks[2], uecChecks[3], uecChecks[4]),
		container.NewHBox(readUEC, applyUEC),
	)
	recPanel := container.NewVBox(
		widget.NewLabel("REC  DID 0x45"),
		container.NewGridWithColumns(4,
			widget.NewLabel("TX"), recRequest,
			widget.NewLabel("RX"), recResponse,
		),
		container.NewBorder(widget.NewLabel("Lamp-check byte"), nil, nil, recLampCheckValue, recLampCheck),
		container.NewGridWithColumns(3, recChecks[0], recChecks[1], recChecks[2], recChecks[3], recChecks[4], recChecks[5], recChecks[6]),
		container.NewHBox(readREC, applyREC),
	)

	controls := container.NewVBox(
		dryRun,
		container.NewGridWithColumns(2, uecPanel, recPanel),
	)
	return container.NewBorder(controls, nil, nil, nil, logScroll)
}
