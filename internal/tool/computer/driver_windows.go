//go:build windows

package computer

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	computerUser32 = windows.NewLazySystemDLL("user32.dll")
	computerGDI32  = windows.NewLazySystemDLL("gdi32.dll")

	procGetSystemMetrics           = computerUser32.NewProc("GetSystemMetrics")
	procGetDC                      = computerUser32.NewProc("GetDC")
	procReleaseDC                  = computerUser32.NewProc("ReleaseDC")
	procSendInput                  = computerUser32.NewProc("SendInput")
	procEnumWindows                = computerUser32.NewProc("EnumWindows")
	procIsWindowVisible            = computerUser32.NewProc("IsWindowVisible")
	procGetWindowTextLengthW       = computerUser32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW             = computerUser32.NewProc("GetWindowTextW")
	procGetWindowThreadProcessID   = computerUser32.NewProc("GetWindowThreadProcessId")
	procGetWindowRect              = computerUser32.NewProc("GetWindowRect")
	procGetForegroundWindow        = computerUser32.NewProc("GetForegroundWindow")
	procSetProcessDPIAware         = computerUser32.NewProc("SetProcessDPIAware")
	procSetProcessDPIAwarenessCtx  = computerUser32.NewProc("SetProcessDpiAwarenessContext")
	procQueryFullProcessImageNameW = windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW")
	procCreateCompatibleDC         = computerGDI32.NewProc("CreateCompatibleDC")
	procCreateDIBSection           = computerGDI32.NewProc("CreateDIBSection")
	procSelectObject               = computerGDI32.NewProc("SelectObject")
	procBitBlt                     = computerGDI32.NewProc("BitBlt")
	procDeleteObject               = computerGDI32.NewProc("DeleteObject")
	procDeleteDC                   = computerGDI32.NewProc("DeleteDC")
)

const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79

	srcCopy    = 0x00CC0020
	captureBLT = 0x40000000

	inputMouse    = 0
	inputKeyboard = 1

	mouseEventMove        = 0x0001
	mouseEventLeftDown    = 0x0002
	mouseEventLeftUp      = 0x0004
	mouseEventRightDown   = 0x0008
	mouseEventRightUp     = 0x0010
	mouseEventMiddleDown  = 0x0020
	mouseEventMiddleUp    = 0x0040
	mouseEventWheel       = 0x0800
	mouseEventHWheel      = 0x1000
	mouseEventVirtualDesk = 0x4000
	mouseEventAbsolute    = 0x8000

	keyEventKeyUp   = 0x0002
	keyEventUnicode = 0x0004

	biRGB = 0
)

type windowsDriver struct{}

var windowsDPIAwarenessOnce sync.Once

func newPlatformDriver(string) Driver {
	windowsDPIAwarenessOnce.Do(func() {
		// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 is the pseudo-handle -4.
		// It keeps GDI pixels, window rectangles, and SendInput coordinates in
		// one physical-pixel coordinate system on mixed-DPI desktops.
		if procSetProcessDPIAwarenessCtx.Find() == nil {
			if result, _, _ := procSetProcessDPIAwarenessCtx.Call(^uintptr(3)); result != 0 {
				return
			}
		}
		if procSetProcessDPIAware.Find() == nil {
			procSetProcessDPIAware.Call()
		}
	})
	return &windowsDriver{}
}
func (d *windowsDriver) Platform() string { return "windows" }
func (d *windowsDriver) Capabilities() []string {
	return []string{"screenshot", "mouse", "keyboard", "unicode_text", "app_inventory", "multi_display", "per_monitor_dpi", "native_no_dependencies"}
}

type winPoint struct{ X, Y int32 }
type winRect struct{ Left, Top, Right, Bottom int32 }
type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}
type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

func systemMetric(index int32) int {
	value, _, _ := procGetSystemMetrics.Call(uintptr(index))
	return int(int32(value))
}

func (d *windowsDriver) Capture(ctx context.Context) (Screenshot, error) {
	if err := ctx.Err(); err != nil {
		return Screenshot{}, err
	}
	geometry := Geometry{
		OriginX: systemMetric(smXVirtualScreen), OriginY: systemMetric(smYVirtualScreen),
		Width: systemMetric(smCXVirtualScreen), Height: systemMetric(smCYVirtualScreen),
	}
	if geometry.Width < 1 || geometry.Height < 1 {
		return Screenshot{}, fmt.Errorf("Windows virtual desktop has invalid geometry %+v", geometry)
	}
	screenDC, _, callErr := procGetDC.Call(0)
	if screenDC == 0 {
		return Screenshot{}, fmt.Errorf("GetDC failed: %v", callErr)
	}
	defer procReleaseDC.Call(0, screenDC)
	memoryDC, _, callErr := procCreateCompatibleDC.Call(screenDC)
	if memoryDC == 0 {
		return Screenshot{}, fmt.Errorf("CreateCompatibleDC failed: %v", callErr)
	}
	defer procDeleteDC.Call(memoryDC)

	info := bitmapInfo{Header: bitmapInfoHeader{
		Size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), Width: int32(geometry.Width),
		Height: -int32(geometry.Height), Planes: 1, BitCount: 32, Compression: biRGB,
	}}
	var pixels unsafe.Pointer
	bitmap, _, callErr := procCreateDIBSection.Call(memoryDC, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&pixels)), 0, 0)
	if bitmap == 0 || pixels == nil {
		return Screenshot{}, fmt.Errorf("CreateDIBSection failed: %v", callErr)
	}
	defer procDeleteObject.Call(bitmap)
	previous, _, _ := procSelectObject.Call(memoryDC, bitmap)
	if previous == 0 {
		return Screenshot{}, fmt.Errorf("SelectObject failed")
	}
	defer procSelectObject.Call(memoryDC, previous)
	result, _, callErr := procBitBlt.Call(
		memoryDC, 0, 0, uintptr(geometry.Width), uintptr(geometry.Height), screenDC,
		uintptr(int64(geometry.OriginX)), uintptr(int64(geometry.OriginY)), srcCopy|captureBLT,
	)
	if result == 0 {
		return Screenshot{}, fmt.Errorf("BitBlt failed: %v", callErr)
	}
	if err := ctx.Err(); err != nil {
		return Screenshot{}, err
	}

	raw := unsafe.Slice((*byte)(pixels), geometry.Width*geometry.Height*4)
	imageValue := image.NewNRGBA(image.Rect(0, 0, geometry.Width, geometry.Height))
	for offset := 0; offset < len(raw); offset += 4 {
		imageValue.Pix[offset] = raw[offset+2]
		imageValue.Pix[offset+1] = raw[offset+1]
		imageValue.Pix[offset+2] = raw[offset]
		imageValue.Pix[offset+3] = 0xff
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, imageValue); err != nil {
		return Screenshot{}, fmt.Errorf("encode desktop PNG: %w", err)
	}
	shot := Screenshot{PNG: encoded.Bytes(), Geometry: geometry}
	foreground := uintptr(0)
	foreground, _, _ = procGetForegroundWindow.Call()
	if foreground != 0 {
		if app, ok := windowApp(foreground, foreground, geometry.OriginX, geometry.OriginY); ok {
			shot.Foreground = &app
		}
	}
	return shot, nil
}

func (d *windowsDriver) Apps(ctx context.Context) ([]App, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	foreground, _, _ := procGetForegroundWindow.Call()
	originX := systemMetric(smXVirtualScreen)
	originY := systemMetric(smYVirtualScreen)
	apps := []App{}
	callback := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
			return 1
		}
		if app, ok := windowApp(hwnd, foreground, originX, originY); ok {
			apps = append(apps, app)
		}
		return 1
	})
	result, _, callErr := procEnumWindows.Call(callback, 0)
	if result == 0 {
		return nil, fmt.Errorf("EnumWindows failed: %v", callErr)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return apps, nil
}

func windowApp(hwnd, foreground uintptr, originX, originY int) (App, bool) {
	length, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if length == 0 {
		return App{}, false
	}
	buffer := make([]uint16, int(length)+1)
	written, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if written == 0 {
		return App{}, false
	}
	var pid uint32
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	var rect winRect
	if ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect))); ok == 0 {
		return App{}, false
	}
	executable := processExecutable(pid)
	name := strings.TrimSuffix(filepath.Base(executable), filepath.Ext(executable))
	if name == "" {
		name = fmt.Sprintf("PID %d", pid)
	}
	return App{
		PID: int(pid), Name: name, Executable: executable, Title: windows.UTF16ToString(buffer[:written]),
		Bounds:     Geometry{OriginX: int(rect.Left) - originX, OriginY: int(rect.Top) - originY, Width: int(rect.Right - rect.Left), Height: int(rect.Bottom - rect.Top)},
		Foreground: hwnd == foreground,
	}, true
}

func processExecutable(pid uint32) string {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	result, _, _ := procQueryFullProcessImageNameW.Call(uintptr(handle), 0, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)))
	if result == 0 {
		return ""
	}
	return windows.UTF16ToString(buffer[:size])
}

type mouseInput struct {
	DX, DY    int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}
type keyboardInput struct {
	VK, Scan  uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}
type windowsInput struct {
	Type    uint32
	Padding uint32
	Data    [32]byte
}

func sendWindowsInput(inputType uint32, value any) error {
	input := windowsInput{Type: inputType}
	var source []byte
	switch typed := value.(type) {
	case mouseInput:
		source = unsafe.Slice((*byte)(unsafe.Pointer(&typed)), int(unsafe.Sizeof(typed)))
	case keyboardInput:
		source = unsafe.Slice((*byte)(unsafe.Pointer(&typed)), int(unsafe.Sizeof(typed)))
	default:
		return fmt.Errorf("unsupported SendInput payload")
	}
	copy(input.Data[:], source)
	count, _, callErr := procSendInput.Call(1, uintptr(unsafe.Pointer(&input)), unsafe.Sizeof(input))
	if count != 1 {
		return fmt.Errorf("SendInput failed: %v", callErr)
	}
	return nil
}

func moveWindowsPointer(x, y int, geometry Geometry) error {
	if geometry.Width <= 1 || geometry.Height <= 1 {
		return fmt.Errorf("invalid virtual desktop geometry")
	}
	dx := int32(math.Round(float64(x-geometry.OriginX) * 65535 / float64(geometry.Width-1)))
	dy := int32(math.Round(float64(y-geometry.OriginY) * 65535 / float64(geometry.Height-1)))
	return sendWindowsInput(inputMouse, mouseInput{DX: dx, DY: dy, Flags: mouseEventMove | mouseEventAbsolute | mouseEventVirtualDesk})
}

func mouseButtonFlags(button string) (uint32, uint32, error) {
	switch button {
	case "left":
		return mouseEventLeftDown, mouseEventLeftUp, nil
	case "right":
		return mouseEventRightDown, mouseEventRightUp, nil
	case "middle":
		return mouseEventMiddleDown, mouseEventMiddleUp, nil
	default:
		return 0, 0, fmt.Errorf("unsupported mouse button %q", button)
	}
}

func (d *windowsDriver) Act(ctx context.Context, action Action, geometry Geometry, allowSystemKeys bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if action.Kind == "wait" {
		return waitContext(ctx, time.Duration(action.DurationMS)*time.Millisecond)
	}
	abs := func(x, y *int) (int, int) { return *x + geometry.OriginX, *y + geometry.OriginY }
	move := func(x, y *int) error { px, py := abs(x, y); return moveWindowsPointer(px, py, geometry) }
	down, up, buttonErr := mouseButtonFlags(action.Button)
	switch action.Kind {
	case "move":
		return move(action.X, action.Y)
	case "click":
		if buttonErr != nil {
			return buttonErr
		}
		if err := move(action.X, action.Y); err != nil {
			return err
		}
		for i := 0; i < action.ClickCount; i++ {
			if err := sendWindowsInput(inputMouse, mouseInput{Flags: down}); err != nil {
				return err
			}
			if err := sendWindowsInput(inputMouse, mouseInput{Flags: up}); err != nil {
				return err
			}
			if i+1 < action.ClickCount {
				if err := waitContext(ctx, 60*time.Millisecond); err != nil {
					return err
				}
			}
		}
		return nil
	case "mouse_down", "mouse_up":
		if buttonErr != nil {
			return buttonErr
		}
		if err := move(action.X, action.Y); err != nil {
			return err
		}
		flag := down
		if action.Kind == "mouse_up" {
			flag = up
		}
		return sendWindowsInput(inputMouse, mouseInput{Flags: flag})
	case "drag":
		if buttonErr != nil {
			return buttonErr
		}
		startX, startY := abs(action.X, action.Y)
		endX, endY := abs(action.ToX, action.ToY)
		if err := moveWindowsPointer(startX, startY, geometry); err != nil {
			return err
		}
		if err := sendWindowsInput(inputMouse, mouseInput{Flags: down}); err != nil {
			return err
		}
		buttonHeld := true
		defer func() {
			if buttonHeld {
				_ = sendWindowsInput(inputMouse, mouseInput{Flags: up})
			}
		}()
		steps := action.DurationMS / 8
		if steps < 2 {
			steps = 2
		}
		if steps > 120 {
			steps = 120
		}
		for i := 1; i <= steps; i++ {
			t := float64(i) / float64(steps)
			if err := moveWindowsPointer(int(math.Round(float64(startX)+float64(endX-startX)*t)), int(math.Round(float64(startY)+float64(endY-startY)*t)), geometry); err != nil {
				return err
			}
			if err := waitContext(ctx, time.Duration(action.DurationMS/steps)*time.Millisecond); err != nil {
				return err
			}
		}
		if err := sendWindowsInput(inputMouse, mouseInput{Flags: up}); err != nil {
			return err
		}
		buttonHeld = false
		return nil
	case "scroll":
		if err := move(action.X, action.Y); err != nil {
			return err
		}
		if action.DeltaY != 0 {
			value := int32(-action.DeltaY * 120)
			if err := sendWindowsInput(inputMouse, mouseInput{MouseData: uint32(value), Flags: mouseEventWheel}); err != nil {
				return err
			}
		}
		if action.DeltaX != 0 {
			value := int32(action.DeltaX * 120)
			if err := sendWindowsInput(inputMouse, mouseInput{MouseData: uint32(value), Flags: mouseEventHWheel}); err != nil {
				return err
			}
		}
		return nil
	case "key":
		if !allowSystemKeys && isBlockedSystemKey(d.Platform(), action.Key) {
			return fmt.Errorf("system-level key combination is disabled by AGENTDOCK_COMPUTER_USE_ALLOW_SYSTEM_KEYS")
		}
		for _, chord := range strings.Fields(action.Key) {
			if err := pressWindowsChord(chord); err != nil {
				return err
			}
		}
		return nil
	case "type":
		for _, code := range utf16.Encode([]rune(action.Text)) {
			if code == '\n' {
				if err := tapWindowsKey(0x0D); err != nil {
					return err
				}
				continue
			}
			if code == '\t' {
				if err := tapWindowsKey(0x09); err != nil {
					return err
				}
				continue
			}
			if code == '\r' {
				continue
			}
			if err := sendWindowsInput(inputKeyboard, keyboardInput{Scan: code, Flags: keyEventUnicode}); err != nil {
				return err
			}
			if err := sendWindowsInput(inputKeyboard, keyboardInput{Scan: code, Flags: keyEventUnicode | keyEventKeyUp}); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported Windows desktop action %q", action.Kind)
	}
}

func tapWindowsKey(vk uint16) error {
	if err := sendWindowsInput(inputKeyboard, keyboardInput{VK: vk}); err != nil {
		return err
	}
	return sendWindowsInput(inputKeyboard, keyboardInput{VK: vk, Flags: keyEventKeyUp})
}

func pressWindowsChord(chord string) error {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(chord)), "+")
	if len(parts) == 0 {
		return fmt.Errorf("empty key chord")
	}
	modifiers := []uint16{}
	pressed := []uint16{}
	defer func() {
		for i := len(pressed) - 1; i >= 0; i-- {
			_ = sendWindowsInput(inputKeyboard, keyboardInput{VK: pressed[i], Flags: keyEventKeyUp})
		}
	}()
	for _, part := range parts[:len(parts)-1] {
		vk, ok := windowsKeyCode(part)
		if !ok || (vk != 0x10 && vk != 0x11 && vk != 0x12 && vk != 0x5B) {
			return fmt.Errorf("unsupported key modifier %q", part)
		}
		modifiers = append(modifiers, vk)
		if err := sendWindowsInput(inputKeyboard, keyboardInput{VK: vk}); err != nil {
			return err
		}
		pressed = append(pressed, vk)
	}
	key, ok := windowsKeyCode(parts[len(parts)-1])
	if !ok {
		return fmt.Errorf("unsupported key %q", parts[len(parts)-1])
	}
	if err := tapWindowsKey(key); err != nil {
		return err
	}
	for i := len(modifiers) - 1; i >= 0; i-- {
		if err := sendWindowsInput(inputKeyboard, keyboardInput{VK: modifiers[i], Flags: keyEventKeyUp}); err != nil {
			return err
		}
	}
	pressed = nil
	return nil
}

func windowsKeyCode(name string) (uint16, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if len(name) == 1 {
		char := name[0]
		if char >= 'a' && char <= 'z' {
			return uint16(char - 'a' + 'A'), true
		}
		if char >= '0' && char <= '9' {
			return uint16(char), true
		}
	}
	aliases := map[string]uint16{
		"shift": 0x10, "ctrl": 0x11, "control": 0x11, "alt": 0x12, "option": 0x12, "super": 0x5B, "win": 0x5B, "cmd": 0x5B, "command": 0x5B, "meta": 0x5B,
		"backspace": 0x08, "tab": 0x09, "return": 0x0D, "enter": 0x0D, "escape": 0x1B, "esc": 0x1B, "space": 0x20, "pageup": 0x21, "pagedown": 0x22,
		"end": 0x23, "home": 0x24, "left": 0x25, "up": 0x26, "right": 0x27, "down": 0x28, "delete": 0x2E,
		"f1": 0x70, "f2": 0x71, "f3": 0x72, "f4": 0x73, "f5": 0x74, "f6": 0x75, "f7": 0x76, "f8": 0x77, "f9": 0x78, "f10": 0x79, "f11": 0x7A, "f12": 0x7B,
	}
	vk, ok := aliases[name]
	return vk, ok
}
