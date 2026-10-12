This repository has been made more or less obsolete after the following forum thread:
[TrionicTuning.com: Saab 93 Led Enabler](https://www.trionictuning.com/forum/viewtopic.php?t=18549)  

After the thread [roffe](https://github.com/roffe) made his code public: [roffe/hcd](https://github.com/roffe/hcd) 

This code had a partial implementation of the following that was revealed there.

Quote from roffe on TrionicTuning.com:
> Module identifiers
>
>```Module  Identifier  Contents
>UEC     0x90        VIN
>UEC     0x40        PWM output configuration
>UEC     0x4D        Bulb check configuration, front
>REC     0x45        Bulb check configuration, rear
>```
>Byte 0 of each configuration is the check interval in ticks of 14.5 ms (0 = checking >off). Byte 1 (and byte 2 for rear turn signals) is a bit mask with one bit per light:
>
>```UEC byte 1   Light                 REC byte 1   Light
>0x80         Front parking lights  0x80         Tail lights
>0x40         Front fog lights      0x40         Rear fog lights
>0x20         Low beams             0x20         Stop lights
>0x10         High beams            0x10         Reverse light
>0x01         Front turn signals    0x08         License plate
>byte 2 0x08  Rear turn signals
>```

So before this was confirmed the bitmask for the lights was correct and the checking interval byte. the rest was to be determined and has now been made public since 11-10-2026 ~16:30

# Go CAN LED Enabler

A Fyne GUI for setting the UEC/REC light checking settings and monitoring CAN traffic through a J2534 adapter.

The application uses `github.com/roffe/gocan/v2` and its `gmlan` package. ISO-TP framing and GMLAN services are handled by that library.

And special thanks go to:
- [TrionicTuning.com: "is there a way to turn off CANBUS lighting sensor circuits - maptun apparently can"](https://www.trionictuning.com/forum/viewtopic.php?t=12064)
- [BCR Auto: Saab LED enabler](https://github.com/wawawa15/saab-led-enabler)

## Features

- Connect through any adapter registered by `gocan/v2`, including J2534 adapters.
- Read and update the UEC parameter at DID `0x4D` using CAN IDs `0x24F -> 0x64F`.
- Read and update the REC parameter at DID `0x45` using CAN IDs `0x249 -> 0x649`.
- Populate UEC and REC lighting checkboxes from the read configuration and apply only the known light bits.
- Preserve the complete returned configuration and change only its parameter bytes.
- Handle the REC multi-frame ISO-TP response through `ReadDataByIdentifier`.
- Monitor CAN IDs, counts, lengths, payloads, and last-seen times.
- Filter the monitor and wire trace by hexadecimal CAN IDs; the default filter is `24F,249,64F,649`.
- View filtered RX and diagnostic TX frames, including ISO-TP request and flow-control frames.

## Build

```powershell
go mod tidy
go build -tags j2534 .
```

For a Windows build, the native compiler must match the Go target. The default 32-bit build uses the MSYS2 `mingw32` toolchain from `C:\msys64`:

```powershell
.\build-windows.ps1 -Architecture 386
```

This creates `dist/gocanledenabler-win386.exe`. Use `-MsysRoot` if MSYS2 is installed elsewhere. The J2534 driver DLL must also be 32-bit; a 64-bit DLL cannot be loaded by the 32-bit executable.
