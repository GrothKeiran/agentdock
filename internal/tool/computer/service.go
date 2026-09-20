package computer

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	toolcore "github.com/uvwt/agentdock/internal/tool/core"
)

type ScreenshotPublisher func(context.Context, []byte, int) (map[string]any, error)

const defaultScreenshotRetentionSeconds = 300

type Service struct {
	driver          Driver
	publish         ScreenshotPublisher
	allowSystemKeys bool

	mu       sync.Mutex
	snapshot string
	geometry Geometry
}

func New(home string, allowSystemKeys bool, publish ScreenshotPublisher) *Service {
	return &Service{driver: newPlatformDriver(home), publish: publish, allowSystemKeys: allowSystemKeys}
}

// NewFromDriver is the explicit dependency-injection seam used by platform
// integration tests and embedders. Production runtimes should call New.
func NewFromDriver(driver Driver, allowSystemKeys bool, publish ScreenshotPublisher) *Service {
	return &Service{driver: driver, publish: publish, allowSystemKeys: allowSystemKeys}
}

func (s *Service) Apps(ctx context.Context, _ AppsRequest) (toolcore.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	apps, err := s.driver.Apps(ctx)
	if err != nil {
		return failure(s.driver.Platform(), "APP_DISCOVERY_FAILED", err.Error(), nil), nil
	}
	return toolcore.Result{
		"computer_ok":  true,
		"platform":     s.driver.Platform(),
		"capabilities": append([]string(nil), s.driver.Capabilities()...),
		"apps":         apps,
	}, nil
}

func (s *Service) Snapshot(ctx context.Context, request SnapshotRequest) (toolcore.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.captureLocked(ctx, request.RetentionSeconds)
}

func (s *Service) Act(ctx context.Context, request ActRequest) (toolcore.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.snapshot == "" || request.SnapshotID != s.snapshot {
		return failure(s.driver.Platform(), "STALE_SNAPSHOT", "snapshot_id is stale or unknown; call computer_snapshot and use its latest snapshot_id", nil), nil
	}
	if len(request.Actions) == 0 {
		return failure(s.driver.Platform(), "ACTION_REQUIRED", "at least one action is required", nil), nil
	}
	if len(request.Actions) > 100 {
		return failure(s.driver.Platform(), "INVALID_ACTION", "at most 100 actions are allowed in one batch", map[string]any{"executed_count": 0}), nil
	}
	geometry := s.geometry
	actions := make([]Action, len(request.Actions))
	heldButtons := make(map[string]int)
	for index, action := range request.Actions {
		action = normalizedAction(action)
		if err := validateAction(action, geometry); err != nil {
			return failure(s.driver.Platform(), "INVALID_ACTION", err.Error(), map[string]any{"action_index": index, "executed_count": 0}), nil
		}
		if action.Kind == "key" && !s.allowSystemKeys && isBlockedSystemKey(s.driver.Platform(), action.Key) {
			return failure(s.driver.Platform(), "SYSTEM_KEYS_DISABLED", "system-level key combination is disabled by AGENTDOCK_COMPUTER_USE_ALLOW_SYSTEM_KEYS", map[string]any{"action_index": index, "executed_count": 0}), nil
		}
		if len(heldButtons) > 0 && action.Kind != "move" && action.Kind != "wait" && action.Kind != "mouse_up" {
			return failure(s.driver.Platform(), "INVALID_ACTION", "while a mouse button is held, only move, wait, and the matching mouse_up are allowed", map[string]any{"action_index": index, "executed_count": 0}), nil
		}
		switch action.Kind {
		case "mouse_down":
			if downIndex, exists := heldButtons[action.Button]; exists {
				return failure(s.driver.Platform(), "INVALID_ACTION", fmt.Sprintf("mouse button %s is already held by action %d", action.Button, downIndex), map[string]any{"action_index": index, "executed_count": 0}), nil
			}
			heldButtons[action.Button] = index
		case "mouse_up":
			if _, exists := heldButtons[action.Button]; !exists {
				return failure(s.driver.Platform(), "INVALID_ACTION", fmt.Sprintf("mouse_up for %s has no matching mouse_down in this batch", action.Button), map[string]any{"action_index": index, "executed_count": 0}), nil
			}
			delete(heldButtons, action.Button)
		}
		actions[index] = action
	}
	for _, button := range []string{"left", "middle", "right"} {
		if downIndex, held := heldButtons[button]; held {
			return failure(s.driver.Platform(), "INVALID_ACTION", fmt.Sprintf("mouse_down for %s must have a matching mouse_up in the same batch", button), map[string]any{"action_index": downIndex, "executed_count": 0}), nil
		}
	}

	s.snapshot = ""
	executed := 0
	pressedButtons := make(map[string]Action)
	for index, action := range actions {
		if action.Kind == "mouse_down" {
			pressedButtons[action.Button] = action
		}
		if action.Kind == "mouse_up" {
			pressedButtons[action.Button] = action
		}
		if err := s.driver.Act(ctx, action, geometry, s.allowSystemKeys); err != nil {
			s.releasePressedButtons(pressedButtons, geometry)
			return failure(s.driver.Platform(), "ACTION_FAILED", err.Error(), map[string]any{"action_index": index, "executed_count": executed, "result_unknown": true}), nil
		}
		if action.X != nil && action.Y != nil {
			for button, pressed := range pressedButtons {
				pressed.X = action.X
				pressed.Y = action.Y
				pressedButtons[button] = pressed
			}
		}
		if action.Kind == "mouse_up" {
			delete(pressedButtons, action.Button)
		}
		executed++
	}

	captureAfter := request.CaptureAfter == nil || *request.CaptureAfter
	if !captureAfter {
		return toolcore.Result{
			"computer_ok": true, "platform": s.driver.Platform(), "executed_count": executed,
			"needs_snapshot": true,
		}, nil
	}
	result, err := s.captureLocked(ctx, request.RetentionSeconds)
	if err != nil {
		return nil, err
	}
	result["executed_count"] = executed
	result["needs_snapshot"] = false
	return result, nil
}

func (s *Service) releasePressedButtons(pressed map[string]Action, geometry Geometry) {
	if len(pressed) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, button := range []string{"left", "middle", "right"} {
		action, held := pressed[button]
		if !held {
			continue
		}
		action.Kind = "mouse_up"
		action.Button = button
		_ = s.driver.Act(ctx, normalizedAction(action), geometry, s.allowSystemKeys)
	}
}

func (s *Service) captureLocked(ctx context.Context, retentionSeconds int) (toolcore.Result, error) {
	shot, err := s.driver.Capture(ctx)
	if err != nil {
		s.snapshot = ""
		return failure(s.driver.Platform(), "CAPTURE_FAILED", err.Error(), nil), nil
	}
	if len(shot.PNG) == 0 || shot.Geometry.Width < 1 || shot.Geometry.Height < 1 {
		s.snapshot = ""
		return failure(s.driver.Platform(), "CAPTURE_FAILED", "desktop capture returned an empty image or invalid geometry", nil), nil
	}
	if s.publish == nil {
		return nil, errors.New("computer screenshot publisher is not configured")
	}
	if retentionSeconds == 0 {
		retentionSeconds = defaultScreenshotRetentionSeconds
	}
	published, err := s.publish(ctx, shot.PNG, retentionSeconds)
	if err != nil {
		s.snapshot = ""
		return nil, fmt.Errorf("publish computer screenshot: %w", err)
	}
	id, err := snapshotID()
	if err != nil {
		return nil, fmt.Errorf("create computer snapshot id: %w", err)
	}
	s.snapshot = id
	s.geometry = shot.Geometry
	result := toolcore.Result{
		"computer_ok":          true,
		"platform":             s.driver.Platform(),
		"snapshot_id":          id,
		"display":              shot.Geometry,
		"screenshot":           published,
		"_mcp_image_base64":    base64.StdEncoding.EncodeToString(shot.PNG),
		"_mcp_image_mime_type": "image/png",
	}
	if shot.Foreground != nil {
		result["foreground_app"] = shot.Foreground
	}
	return result, nil
}

func snapshotID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "cs_" + hex.EncodeToString(raw), nil
}

func failure(platform, code, message string, details map[string]any) toolcore.Result {
	errorValue := map[string]any{"code": code, "message": message}
	if len(details) > 0 {
		errorValue["details"] = details
	}
	result := toolcore.Result{"computer_ok": false, "platform": platform, "error": errorValue}
	for key, value := range details {
		result[key] = value
	}
	return result
}

func normalizedAction(action Action) Action {
	action.Kind = strings.ToLower(strings.TrimSpace(action.Kind))
	action.Button = strings.ToLower(strings.TrimSpace(action.Button))
	if action.Button == "" {
		action.Button = "left"
	}
	if action.ClickCount == 0 {
		action.ClickCount = 1
	}
	if action.Kind == "drag" && action.DurationMS == 0 {
		action.DurationMS = 250
	}
	return action
}

func validateAction(action Action, geometry Geometry) error {
	action = normalizedAction(action)
	if action.DurationMS < 0 || action.DurationMS > int((10*time.Second)/time.Millisecond) {
		return errors.New("duration_ms must be between 0 and 10000")
	}
	if action.ClickCount < 1 || action.ClickCount > 3 {
		return errors.New("click_count must be between 1 and 3")
	}
	if action.Button != "left" && action.Button != "middle" && action.Button != "right" {
		return fmt.Errorf("unsupported mouse button %q", action.Button)
	}
	point := func(x, y *int, label string) error {
		if x == nil || y == nil {
			return fmt.Errorf("%s requires x and y", label)
		}
		if *x < 0 || *y < 0 || *x >= geometry.Width || *y >= geometry.Height {
			return fmt.Errorf("%s coordinate (%d,%d) is outside screenshot bounds %dx%d", label, *x, *y, geometry.Width, geometry.Height)
		}
		return nil
	}
	switch action.Kind {
	case "move", "click", "mouse_down", "mouse_up":
		return point(action.X, action.Y, action.Kind)
	case "drag":
		if err := point(action.X, action.Y, "drag start"); err != nil {
			return err
		}
		return point(action.ToX, action.ToY, "drag destination")
	case "scroll":
		if err := point(action.X, action.Y, "scroll"); err != nil {
			return err
		}
		if action.DeltaX == 0 && action.DeltaY == 0 {
			return errors.New("scroll requires a non-zero delta_x or delta_y")
		}
		if action.DeltaX < -100 || action.DeltaX > 100 || action.DeltaY < -100 || action.DeltaY > 100 {
			return errors.New("scroll delta_x and delta_y must be between -100 and 100")
		}
		return nil
	case "key":
		key := strings.TrimSpace(action.Key)
		if key == "" {
			return errors.New("key action requires key")
		}
		if len(key) > 4096 {
			return errors.New("key must not exceed 4096 bytes")
		}
		return validateKeyMacro(key)
	case "type":
		if len(action.Text) > 32768 {
			return errors.New("text must not exceed 32768 bytes")
		}
		return nil
	case "wait":
		return nil
	default:
		return fmt.Errorf("unsupported computer action %q", action.Kind)
	}
}
