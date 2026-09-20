package computer

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

type fakeDriver struct {
	actions []Action
	failAt  int
	failed  bool
}

func (d *fakeDriver) Platform() string       { return "fake" }
func (d *fakeDriver) Capabilities() []string { return []string{"screenshot", "mouse"} }
func (d *fakeDriver) Apps(context.Context) ([]App, error) {
	return []App{{PID: 7, Name: "Editor", Foreground: true}}, nil
}
func (d *fakeDriver) Capture(context.Context) (Screenshot, error) {
	return Screenshot{
		PNG: testPNG(), Geometry: Geometry{OriginX: -100, OriginY: 20, Width: 4, Height: 3},
		Foreground: &App{PID: 7, Name: "Editor", Foreground: true},
	}, nil
}
func (d *fakeDriver) Act(_ context.Context, action Action, _ Geometry, _ bool) error {
	if d.failAt > 0 && !d.failed && len(d.actions)+1 == d.failAt {
		d.failed = true
		return errors.New("injected action failure")
	}
	d.actions = append(d.actions, action)
	return nil
}

func testPNG() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x * 40), G: uint8(y * 60), B: 90, A: 255})
		}
	}
	var out bytes.Buffer
	_ = png.Encode(&out, img)
	return out.Bytes()
}

func testPublisher(_ context.Context, data []byte, _ int) (map[string]any, error) {
	if len(data) == 0 {
		return nil, errors.New("empty")
	}
	return map[string]any{"artifact_id": "artifact-1", "mime_type": "image/png"}, nil
}

func TestSnapshotBindsActionsAndReturnsInlineImage(t *testing.T) {
	driver := &fakeDriver{}
	service := NewFromDriver(driver, false, testPublisher)
	snapshot, err := service.Snapshot(context.Background(), SnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot["computer_ok"] != true || snapshot["snapshot_id"] == "" || snapshot["_mcp_image_base64"] == "" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	id := snapshot["snapshot_id"].(string)
	result, err := service.Act(context.Background(), ActRequest{
		SnapshotID: id,
		Actions:    []Action{{Kind: "click", X: intPointer(3), Y: intPointer(2)}, {Kind: "type", Text: "你好"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["computer_ok"] != true || result["executed_count"] != 2 || result["snapshot_id"] == id {
		t.Fatalf("act result = %#v", result)
	}
	if len(driver.actions) != 2 || driver.actions[0].Button != "left" || driver.actions[0].ClickCount != 1 {
		t.Fatalf("actions = %#v", driver.actions)
	}
}

func TestSnapshotUsesShortPrivacyDefaultRetention(t *testing.T) {
	retention := 0
	service := NewFromDriver(&fakeDriver{}, false, func(_ context.Context, _ []byte, seconds int) (map[string]any, error) {
		retention = seconds
		return map[string]any{"artifact_id": "artifact-1", "mime_type": "image/png"}, nil
	})
	if _, err := service.Snapshot(context.Background(), SnapshotRequest{}); err != nil {
		t.Fatal(err)
	}
	if retention != defaultScreenshotRetentionSeconds {
		t.Fatalf("retention = %d, want %d", retention, defaultScreenshotRetentionSeconds)
	}
}

func TestStaleAndOutOfBoundsSnapshotsFailClosed(t *testing.T) {
	driver := &fakeDriver{}
	service := NewFromDriver(driver, false, testPublisher)
	stale, err := service.Act(context.Background(), ActRequest{SnapshotID: "unknown", Actions: []Action{{Kind: "wait"}}})
	if err != nil {
		t.Fatal(err)
	}
	if stale["computer_ok"] != false || stale["error"].(map[string]any)["code"] != "STALE_SNAPSHOT" {
		t.Fatalf("stale result = %#v", stale)
	}
	snapshot, _ := service.Snapshot(context.Background(), SnapshotRequest{})
	invalid, err := service.Act(context.Background(), ActRequest{
		SnapshotID: snapshot["snapshot_id"].(string),
		Actions:    []Action{{Kind: "click", X: intPointer(4), Y: intPointer(0)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if invalid["computer_ok"] != false || invalid["error"].(map[string]any)["code"] != "INVALID_ACTION" || len(driver.actions) != 0 {
		t.Fatalf("invalid result = %#v actions=%#v", invalid, driver.actions)
	}
}

func TestBatchIsFullyValidatedBeforeAnyActionRuns(t *testing.T) {
	driver := &fakeDriver{}
	service := NewFromDriver(driver, false, testPublisher)
	snapshot, _ := service.Snapshot(context.Background(), SnapshotRequest{})
	id := snapshot["snapshot_id"].(string)
	result, err := service.Act(context.Background(), ActRequest{
		SnapshotID: id,
		Actions: []Action{
			{Kind: "type", Text: "must not run"},
			{Kind: "click", X: intPointer(9), Y: intPointer(0)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["computer_ok"] != false || result["executed_count"] != 0 || len(driver.actions) != 0 {
		t.Fatalf("invalid batch result = %#v actions=%#v", result, driver.actions)
	}
	corrected, err := service.Act(context.Background(), ActRequest{
		SnapshotID: id,
		Actions:    []Action{{Kind: "type", Text: "corrected"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if corrected["computer_ok"] != true || len(driver.actions) != 1 {
		t.Fatalf("corrected batch result = %#v actions=%#v", corrected, driver.actions)
	}
}

func TestSystemKeysAreRejectedBeforeDriverExecution(t *testing.T) {
	driver := &fakeDriver{}
	service := NewFromDriver(driver, false, testPublisher)
	snapshot, _ := service.Snapshot(context.Background(), SnapshotRequest{})
	result, err := service.Act(context.Background(), ActRequest{
		SnapshotID: snapshot["snapshot_id"].(string),
		Actions:    []Action{{Kind: "key", Key: "ctrl+c"}, {Kind: "key", Key: "alt+f4"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["computer_ok"] != false || result["error"].(map[string]any)["code"] != "SYSTEM_KEYS_DISABLED" || len(driver.actions) != 0 {
		t.Fatalf("system-key result = %#v actions=%#v", result, driver.actions)
	}
}

func TestPartialFailureInvalidatesSnapshotAndReportsProgress(t *testing.T) {
	driver := &fakeDriver{failAt: 2}
	service := NewFromDriver(driver, false, testPublisher)
	snapshot, _ := service.Snapshot(context.Background(), SnapshotRequest{})
	id := snapshot["snapshot_id"].(string)
	result, err := service.Act(context.Background(), ActRequest{
		SnapshotID: id,
		Actions:    []Action{{Kind: "wait", DurationMS: 0}, {Kind: "type", Text: "x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["computer_ok"] != false || result["executed_count"] != 1 || result["result_unknown"] != true {
		t.Fatalf("partial result = %#v", result)
	}
	retry, _ := service.Act(context.Background(), ActRequest{SnapshotID: id, Actions: []Action{{Kind: "wait"}}})
	if retry["error"].(map[string]any)["code"] != "STALE_SNAPSHOT" {
		t.Fatalf("retry result = %#v", retry)
	}
}

func TestMouseButtonsMustBalanceAndAreReleasedAfterFailure(t *testing.T) {
	driver := &fakeDriver{}
	service := NewFromDriver(driver, false, testPublisher)
	snapshot, _ := service.Snapshot(context.Background(), SnapshotRequest{})
	id := snapshot["snapshot_id"].(string)
	unbalanced, err := service.Act(context.Background(), ActRequest{
		SnapshotID: id,
		Actions:    []Action{{Kind: "mouse_down", X: intPointer(1), Y: intPointer(1)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if unbalanced["computer_ok"] != false || unbalanced["error"].(map[string]any)["code"] != "INVALID_ACTION" || len(driver.actions) != 0 {
		t.Fatalf("unbalanced result = %#v actions=%#v", unbalanced, driver.actions)
	}

	driver.failAt = 2
	failed, err := service.Act(context.Background(), ActRequest{
		SnapshotID: id,
		Actions: []Action{
			{Kind: "mouse_down", X: intPointer(1), Y: intPointer(1)},
			{Kind: "move", X: intPointer(2), Y: intPointer(1)},
			{Kind: "mouse_up", X: intPointer(2), Y: intPointer(1)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if failed["computer_ok"] != false || failed["executed_count"] != 1 || len(driver.actions) != 2 || driver.actions[1].Kind != "mouse_up" {
		t.Fatalf("failed mouse batch = %#v actions=%#v", failed, driver.actions)
	}
}

func TestSystemKeyBlocklist(t *testing.T) {
	for _, testCase := range []struct {
		platform string
		key      string
	}{
		{platform: "darwin", key: "cmd+q"},
		{platform: "darwin", key: "super+tab"},
		{platform: "windows", key: "super+c"},
		{platform: "windows", key: "super+l"},
		{platform: "windows", key: "win+l"},
		{platform: "linux", key: "super+r"},
		{platform: "windows", key: "alt+tab"},
		{platform: "windows", key: "ctrl+alt+delete"},
		{platform: "windows", key: "alt+f4"},
		{platform: "windows", key: "ctrl+shift+escape"},
	} {
		if !isBlockedSystemKey(testCase.platform, testCase.key) {
			t.Errorf("%s %q was not blocked", testCase.platform, testCase.key)
		}
	}
	for _, testCase := range []struct {
		platform string
		key      string
	}{
		{platform: "darwin", key: "cmd+l"},
		{platform: "darwin", key: "cmd+c"},
		{platform: "windows", key: "ctrl+c"},
		{platform: "windows", key: "ctrl+a"},
		{platform: "linux", key: "shift+tab"},
		{platform: "darwin", key: "Return"},
	} {
		if isBlockedSystemKey(testCase.platform, testCase.key) {
			t.Errorf("%s %q was unexpectedly blocked", testCase.platform, testCase.key)
		}
	}
}

func TestKeyMacroValidationRejectsUnknownModifiers(t *testing.T) {
	for _, key := range []string{"ctrl+l", "cmd+shift+p", "a d d space", "Return"} {
		if err := validateKeyMacro(key); err != nil {
			t.Errorf("validateKeyMacro(%q) error = %v", key, err)
		}
	}
	for _, key := range []string{"hyper+x", "ctrl+", "ctrl++"} {
		if err := validateKeyMacro(key); err == nil {
			t.Errorf("validateKeyMacro(%q) unexpectedly succeeded", key)
		}
	}
}

func intPointer(value int) *int { return &value }
