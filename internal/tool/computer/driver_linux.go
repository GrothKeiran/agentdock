//go:build linux

package computer

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type linuxDriver struct{ tempRoot string }

func newPlatformDriver(home string) Driver {
	return &linuxDriver{tempRoot: filepath.Join(home, "computer-use", "tmp")}
}

func (d *linuxDriver) Platform() string { return "linux" }

func (d *linuxDriver) Capabilities() []string {
	capabilities := []string{}
	for _, command := range []string{"grim", "gnome-screenshot", "scrot", "import", "xwd"} {
		if _, err := exec.LookPath(command); err == nil {
			capabilities = append(capabilities, "screenshot")
			break
		}
	}
	if _, err := exec.LookPath("xdotool"); err == nil {
		capabilities = append(capabilities, "mouse", "keyboard", "unicode_text")
	}
	if _, err := exec.LookPath("wmctrl"); err == nil {
		capabilities = append(capabilities, "app_inventory")
	} else if _, err := exec.LookPath("xdotool"); err == nil {
		capabilities = append(capabilities, "app_inventory")
	}
	return capabilities
}

func (d *linuxDriver) Capture(ctx context.Context) (Screenshot, error) {
	if err := os.MkdirAll(d.tempRoot, 0o700); err != nil {
		return Screenshot{}, fmt.Errorf("create computer-use temporary directory: %w", err)
	}
	file, err := os.CreateTemp(d.tempRoot, "desktop-*.png")
	if err != nil {
		return Screenshot{}, fmt.Errorf("create screenshot target: %w", err)
	}
	path := file.Name()
	_ = file.Close()
	defer os.Remove(path)

	commands := [][]string{
		{"grim", path},
		{"gnome-screenshot", "-f", path},
		{"scrot", path},
		{"import", "-window", "root", path},
	}
	var failures []string
	for _, command := range commands {
		if _, lookErr := exec.LookPath(command[0]); lookErr != nil {
			continue
		}
		if output, runErr := exec.CommandContext(ctx, command[0], command[1:]...).CombinedOutput(); runErr != nil {
			failures = append(failures, fmt.Sprintf("%s: %s", command[0], strings.TrimSpace(string(output))))
			continue
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", command[0], readErr))
			continue
		}
		cfg, _, decodeErr := image.DecodeConfig(bytes.NewReader(data))
		if decodeErr != nil {
			failures = append(failures, fmt.Sprintf("%s: invalid PNG", command[0]))
			continue
		}
		return d.finishScreenshot(ctx, data, cfg.Width, cfg.Height), nil
	}
	if xwd, lookErr := exec.LookPath("xwd"); lookErr == nil {
		data, runErr := exec.CommandContext(ctx, xwd, "-root", "-silent").Output()
		if runErr != nil {
			failures = append(failures, fmt.Sprintf("xwd: %v", runErr))
		} else if decoded, decodeErr := decodeXWD(data); decodeErr != nil {
			failures = append(failures, fmt.Sprintf("xwd: %v", decodeErr))
		} else {
			var encoded bytes.Buffer
			if encodeErr := png.Encode(&encoded, decoded); encodeErr != nil {
				failures = append(failures, fmt.Sprintf("xwd: encode PNG: %v", encodeErr))
			} else {
				bounds := decoded.Bounds()
				return d.finishScreenshot(ctx, encoded.Bytes(), bounds.Dx(), bounds.Dy()), nil
			}
		}
	}
	if len(failures) == 0 {
		return Screenshot{}, fmt.Errorf("no supported screenshot command found; install grim, gnome-screenshot, scrot, ImageMagick import, or xwd")
	}
	return Screenshot{}, fmt.Errorf("desktop screenshot failed: %s", strings.Join(failures, "; "))
}

func (d *linuxDriver) finishScreenshot(ctx context.Context, data []byte, width, height int) Screenshot {
	shot := Screenshot{PNG: data, Geometry: Geometry{Width: width, Height: height}}
	if apps, appsErr := d.Apps(ctx); appsErr == nil {
		for i := range apps {
			if apps[i].Foreground {
				shot.Foreground = &apps[i]
				break
			}
		}
	}
	return shot
}

func (d *linuxDriver) Apps(ctx context.Context) ([]App, error) {
	path, err := exec.LookPath("wmctrl")
	if err != nil {
		xdotool, xdotoolErr := exec.LookPath("xdotool")
		if xdotoolErr != nil {
			return nil, fmt.Errorf("wmctrl or xdotool is required for app inventory")
		}
		return appsWithXDoTool(ctx, xdotool)
	}
	output, err := exec.CommandContext(ctx, path, "-lpGx").Output()
	if err != nil {
		return nil, fmt.Errorf("list desktop windows: %w", err)
	}
	active := ""
	if xdotool, lookErr := exec.LookPath("xdotool"); lookErr == nil {
		if value, activeErr := exec.CommandContext(ctx, xdotool, "getactivewindow").Output(); activeErr == nil {
			if id, parseErr := strconv.ParseUint(strings.TrimSpace(string(value)), 10, 64); parseErr == nil {
				active = fmt.Sprintf("0x%08x", id)
			}
		}
	}
	apps := []App{}
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 9 {
			continue
		}
		pid, _ := strconv.Atoi(fields[2])
		x, _ := strconv.Atoi(fields[3])
		y, _ := strconv.Atoi(fields[4])
		width, _ := strconv.Atoi(fields[5])
		height, _ := strconv.Atoi(fields[6])
		apps = append(apps, App{
			PID: pid, Name: fields[7], Title: strings.Join(fields[8:], " "),
			Bounds:     Geometry{OriginX: x, OriginY: y, Width: width, Height: height},
			Foreground: strings.EqualFold(fields[0], active),
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return apps, nil
}

func appsWithXDoTool(ctx context.Context, path string) ([]App, error) {
	output, err := exec.CommandContext(ctx, path, "search", "--onlyvisible", "--name", ".").Output()
	if err != nil {
		return nil, fmt.Errorf("list visible X11 windows with xdotool: %w", err)
	}
	active := ""
	if value, activeErr := exec.CommandContext(ctx, path, "getactivewindow").Output(); activeErr == nil {
		active = strings.TrimSpace(string(value))
	}
	ids := strings.Fields(string(output))
	if len(ids) > 512 {
		ids = ids[:512]
	}
	apps := make([]App, 0, len(ids))
	for _, id := range ids {
		titleOutput, titleErr := exec.CommandContext(ctx, path, "getwindowname", id).Output()
		if titleErr != nil {
			continue
		}
		title := strings.TrimSpace(string(titleOutput))
		if title == "" {
			continue
		}
		pid := 0
		if pidOutput, pidErr := exec.CommandContext(ctx, path, "getwindowpid", id).Output(); pidErr == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(pidOutput)))
		}
		name := title
		if pid > 0 {
			if processName, readErr := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm")); readErr == nil && strings.TrimSpace(string(processName)) != "" {
				name = strings.TrimSpace(string(processName))
			}
		}
		geometry := Geometry{}
		if geometryOutput, geometryErr := exec.CommandContext(ctx, path, "getwindowgeometry", "--shell", id).Output(); geometryErr == nil {
			values := map[string]int{}
			for _, line := range strings.Split(string(geometryOutput), "\n") {
				key, value, found := strings.Cut(line, "=")
				if !found {
					continue
				}
				values[key], _ = strconv.Atoi(strings.TrimSpace(value))
			}
			geometry = Geometry{OriginX: values["X"], OriginY: values["Y"], Width: values["WIDTH"], Height: values["HEIGHT"]}
		}
		apps = append(apps, App{PID: pid, Name: name, Title: title, Bounds: geometry, Foreground: id == active})
	}
	return apps, nil
}

func (d *linuxDriver) Act(ctx context.Context, action Action, geometry Geometry, allowSystemKeys bool) error {
	if action.Kind == "wait" {
		return waitContext(ctx, time.Duration(action.DurationMS)*time.Millisecond)
	}
	path, err := exec.LookPath("xdotool")
	if err != nil {
		return fmt.Errorf("xdotool is required for desktop input: %w", err)
	}
	abs := func(value *int, origin int) string { return strconv.Itoa(*value + origin) }
	run := func(args ...string) error {
		output, runErr := exec.CommandContext(ctx, path, args...).CombinedOutput()
		if runErr != nil {
			return fmt.Errorf("xdotool %s failed: %s", args[0], strings.TrimSpace(string(output)))
		}
		return nil
	}
	button := map[string]string{"left": "1", "middle": "2", "right": "3"}[action.Button]
	switch action.Kind {
	case "move":
		return run("mousemove", "--sync", abs(action.X, geometry.OriginX), abs(action.Y, geometry.OriginY))
	case "click":
		return run("mousemove", "--sync", abs(action.X, geometry.OriginX), abs(action.Y, geometry.OriginY), "click", "--repeat", strconv.Itoa(action.ClickCount), "--delay", "80", button)
	case "mouse_down":
		return run("mousemove", "--sync", abs(action.X, geometry.OriginX), abs(action.Y, geometry.OriginY), "mousedown", button)
	case "mouse_up":
		return run("mousemove", "--sync", abs(action.X, geometry.OriginX), abs(action.Y, geometry.OriginY), "mouseup", button)
	case "drag":
		if err := run("mousemove", "--sync", abs(action.X, geometry.OriginX), abs(action.Y, geometry.OriginY), "mousedown", button); err != nil {
			return err
		}
		buttonHeld := true
		defer func() {
			if buttonHeld {
				releaseCtx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = exec.CommandContext(releaseCtx, path, "mouseup", button).Run()
			}
		}()
		if err := waitContext(ctx, time.Duration(action.DurationMS/2)*time.Millisecond); err != nil {
			return err
		}
		if err := run("mousemove", "--sync", abs(action.ToX, geometry.OriginX), abs(action.ToY, geometry.OriginY)); err != nil {
			return err
		}
		if err := run("mouseup", button); err != nil {
			return err
		}
		buttonHeld = false
		return nil
	case "scroll":
		if err := run("mousemove", "--sync", abs(action.X, geometry.OriginX), abs(action.Y, geometry.OriginY)); err != nil {
			return err
		}
		for _, scroll := range []struct {
			amount             int
			negative, positive string
		}{{action.DeltaY, "4", "5"}, {action.DeltaX, "6", "7"}} {
			if scroll.amount == 0 {
				continue
			}
			key := scroll.positive
			count := scroll.amount
			if count < 0 {
				key, count = scroll.negative, -count
			}
			if err := run("click", "--repeat", strconv.Itoa(count), "--delay", "10", key); err != nil {
				return err
			}
		}
		return nil
	case "key":
		if !allowSystemKeys && isBlockedSystemKey(d.Platform(), action.Key) {
			return fmt.Errorf("system-level key combination is disabled by AGENTDOCK_COMPUTER_USE_ALLOW_SYSTEM_KEYS")
		}
		return run(append([]string{"key", "--clearmodifiers"}, strings.Fields(action.Key)...)...)
	case "type":
		return run("type", "--clearmodifiers", "--delay", "1", "--", action.Text)
	default:
		return fmt.Errorf("unsupported Linux desktop action %q", action.Kind)
	}
}
