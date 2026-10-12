package main

import (
	"fmt"
	"math"
)

const checkIntervalTickMilliseconds = 14.5

func checkIntervalMilliseconds(value byte) float64 {
	return float64(value) * checkIntervalTickMilliseconds
}

func checkIntervalTicks(milliseconds float64) byte {
	ticks := math.Round(milliseconds / checkIntervalTickMilliseconds)
	if ticks < 0 {
		return 0
	}
	if ticks > 255 {
		return 255
	}
	return byte(ticks)
}

type LightFlag struct {
	Name string
	Byte int
	Mask byte
}

var uecLightFlags = []LightFlag{
	{Name: "Parking lights", Byte: 1, Mask: 0x80},
	{Name: "Fog lights", Byte: 1, Mask: 0x40},
	{Name: "Low beams", Byte: 1, Mask: 0x20},
	{Name: "High beams", Byte: 1, Mask: 0x10},
	{Name: "Turn signals", Byte: 1, Mask: 0x01},
}

var recLightFlags = []LightFlag{
	{Name: "Tail lights", Byte: 1, Mask: 0x80},
	{Name: "Rear fog lights", Byte: 1, Mask: 0x40},
	{Name: "Stop lights", Byte: 1, Mask: 0x20},
	{Name: "Reverse light", Byte: 1, Mask: 0x10},
	{Name: "License plate", Byte: 1, Mask: 0x08},
	{Name: "Rear turn signals", Byte: 2, Mask: 0x08},
	{Name: "Trailer lights", Byte: 2, Mask: 0x80},
}

func flagEnabled(data []byte, flag LightFlag) bool {
	return flag.Byte >= 0 && flag.Byte < len(data) && data[flag.Byte]&flag.Mask != 0
}

func flagStates(data []byte, flags []LightFlag) map[string]bool {
	states := make(map[string]bool, len(flags))
	for _, flag := range flags {
		states[flag.Name] = flagEnabled(data, flag)
	}
	return states
}

func applyLightSettings(data []byte, flags []LightFlag, states map[string]bool) ([]byte, error) {
	updated := append([]byte(nil), data...)
	for _, flag := range flags {
		if flag.Byte < 0 || flag.Byte >= len(updated) {
			return nil, fmt.Errorf("%s refers to missing configuration byte %d", flag.Name, flag.Byte)
		}
		if states[flag.Name] {
			updated[flag.Byte] |= flag.Mask
		} else {
			updated[flag.Byte] &^= flag.Mask
		}
	}
	return updated, nil
}
