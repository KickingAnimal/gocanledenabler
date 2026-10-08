# Go CAN LED Enabler

A Fyne GUI for monitoring CAN traffic and using the UEC/REC diagnostic services through a J2534 adapter.

The application uses `github.com/roffe/gocan/v2` and its `gmlan` package. ISO-TP framing and GMLAN services are handled by that library.

## Features

- Monitor CAN IDs, counts, lengths, payloads, and last-seen times.
- Filter the monitor and wire trace by hexadecimal CAN IDs; the default filter is `24F,249,64F,649`.
- View filtered RX and diagnostic TX frames, including ISO-TP request and flow-control frames.
- Connect through any adapter registered by `gocan/v2`, including J2534 adapters.
- Read and update the UEC parameter at DID `0x4D` using CAN IDs `0x24F -> 0x64F`.
- Read and update the REC parameter at DID `0x45` using CAN IDs `0x249 -> 0x649`.
- Preserve the complete returned configuration and change only its leading `0x8B`/`0x00` parameter byte.
- Handle the REC multi-frame ISO-TP response through `ReadDataByIdentifier`.

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
