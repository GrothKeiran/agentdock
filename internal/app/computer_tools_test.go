package app

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"slices"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	toolcomputer "github.com/uvwt/agentdock/internal/tool/computer"
)

type computerContractDriver struct {
	actions []toolcomputer.Action
}

func (d *computerContractDriver) Platform() string       { return "contract" }
func (d *computerContractDriver) Capabilities() []string { return []string{"screenshot", "mouse"} }
func (d *computerContractDriver) Apps(context.Context) ([]toolcomputer.App, error) {
	return []toolcomputer.App{{PID: 42, Name: "Contract App", Foreground: true}}, nil
}
func (d *computerContractDriver) Capture(context.Context) (toolcomputer.Screenshot, error) {
	imageValue := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	imageValue.Set(0, 0, color.NRGBA{R: 40, G: 80, B: 120, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, imageValue); err != nil {
		return toolcomputer.Screenshot{}, err
	}
	return toolcomputer.Screenshot{
		PNG: encoded.Bytes(), Geometry: toolcomputer.Geometry{Width: 2, Height: 2},
		Foreground: &toolcomputer.App{PID: 42, Name: "Contract App", Foreground: true},
	}, nil
}
func (d *computerContractDriver) Act(_ context.Context, action toolcomputer.Action, _ toolcomputer.Geometry, _ bool) error {
	d.actions = append(d.actions, action)
	return nil
}

func TestComputerToolsRuntimeRegistrationAndOutputContracts(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		AgentDockHome:       filepath.Join(root, ".agentdock"),
		AgentDockDefaultDir: root,
		ComputerUseEnabled:  true,
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	driver := &computerContractDriver{}
	runtime.computer = toolcomputer.NewFromDriver(driver, false, func(_ context.Context, data []byte, retention int) (map[string]any, error) {
		return map[string]any{"artifact_id": "computer-contract", "mime_type": "image/png", "size_bytes": len(data), "retention_seconds": retention}, nil
	})

	for _, toolName := range []string{toolcomputer.ToolApps, toolcomputer.ToolSnapshot, toolcomputer.ToolAct} {
		if !slices.Contains(runtime.ToolNames(), toolName) {
			t.Fatalf("enabled runtime is missing %s", toolName)
		}
	}
	apps, err := runtime.Call(context.Background(), toolcomputer.ToolApps, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	assertToolResultMatchestestOutputSchema(t, toolcomputer.ToolApps, apps)

	snapshot, err := runtime.Call(context.Background(), toolcomputer.ToolSnapshot, map[string]any{"retention_seconds": 60})
	if err != nil {
		t.Fatal(err)
	}
	assertToolResultMatchestestOutputSchema(t, toolcomputer.ToolSnapshot, snapshot)
	id, _ := snapshot["snapshot_id"].(string)
	result, err := runtime.Call(context.Background(), toolcomputer.ToolAct, map[string]any{
		"snapshot_id": id,
		"actions": []any{
			map[string]any{"action": "click", "x": 1, "y": 1},
			map[string]any{"action": "type", "text": "你好"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertToolResultMatchestestOutputSchema(t, toolcomputer.ToolAct, result)
	if len(driver.actions) != 2 {
		t.Fatalf("driver actions = %#v", driver.actions)
	}
}
