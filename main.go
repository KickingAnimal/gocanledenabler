package main

import (
	"context"
	"fmt"
	"image/color"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"go.bug.st/serial"

	gocan "github.com/roffe/gocan/v2"
	_ "github.com/roffe/gocan/v2/adapters/all"
)

type frameStat struct {
	id    uint32
	count uint64
	last  gocan.Frame
	at    time.Time
}

type wireEntry struct {
	at        time.Time
	direction string
	frame     gocan.Frame
}

var (
	busPtr     atomic.Pointer[gocan.Bus]
	statsMu    sync.Mutex
	stats      = map[uint32]*frameStat{}
	wireMu     sync.Mutex
	wireTrace  []wireEntry
	monitorMu  sync.RWMutex
	monitorIDs map[uint32]bool
)

func parseMonitorIDs(value string) (map[uint32]bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	ids := make(map[uint32]bool)
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		part = strings.TrimPrefix(strings.TrimPrefix(part, "0x"), "0X")
		id, err := strconv.ParseUint(part, 16, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid CAN ID %q", part)
		}
		ids[uint32(id)] = true
	}
	return ids, nil
}

func monitorPasses(id uint32) bool {
	monitorMu.RLock()
	defer monitorMu.RUnlock()
	return len(monitorIDs) == 0 || monitorIDs[id]
}

func serialPorts() []string {
	ports, err := serial.GetPortsList()
	if err != nil {
		return nil
	}
	return ports
}

func recordFrame(frame gocan.Frame) {
	if !monitorPasses(frame.ID) {
		return
	}
	statsMu.Lock()
	entry := stats[frame.ID]
	if entry == nil {
		entry = &frameStat{id: frame.ID}
		stats[frame.ID] = entry
	}
	entry.count++
	entry.last = frame
	entry.at = time.Now()
	statsMu.Unlock()
	recordWire("RX", frame)
}

func recordWire(direction string, frame gocan.Frame) {
	if !monitorPasses(frame.ID) {
		return
	}
	wireMu.Lock()
	defer wireMu.Unlock()
	if len(wireTrace) >= 5000 {
		wireTrace = append(wireTrace[:0], wireTrace[2500:]...)
	}
	wireTrace = append(wireTrace, wireEntry{at: time.Now(), direction: direction, frame: frame})
}

func snapshotWire() []wireEntry {
	wireMu.Lock()
	defer wireMu.Unlock()
	return append([]wireEntry(nil), wireTrace...)
}

func snapshotStats() []frameStat {
	statsMu.Lock()
	defer statsMu.Unlock()
	rows := make([]frameStat, 0, len(stats))
	for _, entry := range stats {
		rows = append(rows, *entry)
	}
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].id < rows[j-1].id; j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
	return rows
}

func frameData(frame gocan.Frame) string {
	return fmt.Sprintf("% X", frame.Data[:frame.Length])
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	a := app.NewWithID("com.kickinganimal.gocanledenabler")
	window := a.NewWindow("Go CAN LED Enabler")

	adapter := widget.NewSelect(gocan.AdapterNames(), nil)
	port := widget.NewSelectEntry(serialPorts())
	rate := widget.NewSelectEntry([]string{"33.3", "47.619", "500", "615.384", "1000"})
	rate.SetText("33.3")
	status := widget.NewLabel("Disconnected")
	connect := widget.NewButton("Connect", nil)

	var rows []frameStat
	var wireRows []wireEntry
	table := widget.NewTableWithHeaders(
		func() (int, int) { return len(rows), 5 },
		func() fyne.CanvasObject {
			label := widget.NewLabel("")
			label.TextStyle.Monospace = true
			return label
		},
		func(id widget.TableCellID, object fyne.CanvasObject) {
			if id.Row >= len(rows) {
				return
			}
			row := rows[id.Row]
			label := object.(*widget.Label)
			switch id.Col {
			case 0:
				label.SetText(fmt.Sprintf("0x%03X", row.id))
			case 1:
				label.SetText(strconv.FormatUint(row.count, 10))
			case 2:
				label.SetText(fmt.Sprintf("%d", row.last.Length))
			case 3:
				label.SetText(frameData(row.last))
			case 4:
				label.SetText(row.at.Format("15:04:05.000"))
			}
		},
	)
	table.ShowHeaderColumn = false
	headers := []string{"ID", "Count", "Length", "Data", "Last seen"}
	table.CreateHeader = func() fyne.CanvasObject { return widget.NewLabel("") }
	table.UpdateHeader = func(id widget.TableCellID, object fyne.CanvasObject) {
		object.(*widget.Label).SetText(headers[id.Col])
	}
	for i, width := range []float32{80, 80, 70, 260, 120} {
		table.SetColumnWidth(i, width)
	}
	wireList := widget.NewList(
		func() int { return len(wireRows) },
		func() fyne.CanvasObject {
			label := widget.NewLabel("")
			label.TextStyle.Monospace = true
			return label
		},
		func(i widget.ListItemID, object fyne.CanvasObject) {
			entry := wireRows[i]
			object.(*widget.Label).SetText(fmt.Sprintf("%s %s 0x%03X  %s", entry.at.Format("15:04:05.000"), entry.direction, entry.frame.ID, frameData(entry.frame)))
		},
	)
	wireList.HideSeparators = true

	monitorFilter := widget.NewEntry()
	monitorFilter.SetText("24F,249,64F,649")
	filterStatus := widget.NewLabel("")
	applyMonitorFilter := func(value string) {
		ids, err := parseMonitorIDs(value)
		if err != nil {
			filterStatus.SetText("Filter error: " + err.Error())
			return
		}
		monitorMu.Lock()
		monitorIDs = ids
		monitorMu.Unlock()
		statsMu.Lock()
		clear(stats)
		statsMu.Unlock()
		wireMu.Lock()
		wireTrace = nil
		wireMu.Unlock()
		rows = nil
		wireRows = nil
		table.Refresh()
		wireList.Refresh()
		filterStatus.SetText(fmt.Sprintf("%d IDs", len(ids)))
	}
	monitorFilter.OnChanged = applyMonitorFilter
	applyMonitorFilter(monitorFilter.Text)

	var cancel context.CancelFunc
	connect.OnTapped = func() {
		if cancel != nil {
			cancel()
			return
		}
		rateValue, err := strconv.ParseFloat(strings.TrimSpace(rate.Text), 64)
		if err != nil {
			status.SetText("Invalid CAN rate: " + rate.Text)
			return
		}
		adapterName := adapter.Selected
		portName := port.Text
		connect.Disable()
		status.SetText("Connecting...")
		go func() {
			ctx, stop := context.WithCancel(context.Background())
			bus, openErr := gocan.Open(ctx, adapterName, gocan.Config{Port: portName, CANRate: rateValue})
			if openErr != nil {
				stop()
				fyne.Do(func() {
					status.SetText("Error: " + openErr.Error())
					connect.Enable()
				})
				return
			}
			busPtr.Store(bus)
			fyne.Do(func() {
				cancel = stop
				connect.SetText("Disconnect")
				connect.Enable()
				status.SetText(fmt.Sprintf("Connected: %s @ %g kbit/s", adapterName, rateValue))
			})
			for frame := range bus.Frames(ctx) {
				recordFrame(frame)
			}
			busPtr.Store(nil)
			busErr := bus.Err()
			bus.Close()
			stop()
			fyne.Do(func() {
				cancel = nil
				connect.SetText("Connect")
				connect.Enable()
				if busErr != nil {
					status.SetText("Error: " + busErr.Error())
				} else {
					status.SetText("Disconnected")
				}
			})
		}()
	}

	refresh := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() {
		port.SetOptions(serialPorts())
		adapter.SetOptions(gocan.AdapterNames())
		gocan.Rescan()
	})
	clear := widget.NewButton("Clear", func() {
		statsMu.Lock()
		clear(stats)
		statsMu.Unlock()
		rows = nil
		wireMu.Lock()
		wireTrace = nil
		wireMu.Unlock()
		wireRows = nil
		table.Refresh()
		wireList.Refresh()
	})

	go func() {
		for range time.Tick(200 * time.Millisecond) {
			snapshot := snapshotStats()
			wireSnapshot := snapshotWire()
			fyne.Do(func() {
				rows = snapshot
				wireRows = wireSnapshot
				table.Refresh()
				wireList.Refresh()
			})
		}
	}()

	top := container.NewHBox(
		widget.NewLabel("Adapter:"), adapter,
		widget.NewLabel("Port:"), port, refresh,
		widget.NewLabel("Rate:"), rate,
		connect, clear,
	)
	tabs := container.NewAppTabs(
		container.NewTabItem("Monitor", container.NewBorder(
			container.NewHBox(widget.NewLabel("CAN ID filter:"), monitorFilter, filterStatus),
			nil, nil, nil,
			container.NewVSplit(table, container.NewBorder(widget.NewLabel("Wire trace (RX/TX)"), nil, nil, nil, wireList)))),
		container.NewTabItem("Diagnostics", newDiagnosticsTab(func() *gocan.Bus { return busPtr.Load() }, func(frame gocan.Frame) { recordWire("TX", frame) })),
	)
	window.SetContent(container.NewBorder(top, canvas.NewRectangle(color.Transparent), nil, nil,
		container.NewBorder(nil, status, nil, nil, tabs)))
	window.Resize(fyne.NewSize(850, 560))
	window.ShowAndRun()
}
