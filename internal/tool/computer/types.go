package computer

import "context"

const (
	ToolApps     = "computer_apps"
	ToolSnapshot = "computer_snapshot"
	ToolAct      = "computer_act"
)

type Geometry struct {
	OriginX int `json:"origin_x"`
	OriginY int `json:"origin_y"`
	Width   int `json:"width"`
	Height  int `json:"height"`
}

type App struct {
	PID        int      `json:"pid,omitempty"`
	Name       string   `json:"name"`
	Executable string   `json:"executable,omitempty"`
	Title      string   `json:"title,omitempty"`
	Bounds     Geometry `json:"bounds,omitempty"`
	Foreground bool     `json:"foreground,omitempty"`
}

type Screenshot struct {
	PNG        []byte
	Geometry   Geometry
	Foreground *App
}

type Action struct {
	Kind       string `json:"action"`
	X          *int   `json:"x,omitempty"`
	Y          *int   `json:"y,omitempty"`
	ToX        *int   `json:"to_x,omitempty"`
	ToY        *int   `json:"to_y,omitempty"`
	Button     string `json:"button,omitempty"`
	ClickCount int    `json:"click_count,omitempty"`
	DeltaX     int    `json:"delta_x,omitempty"`
	DeltaY     int    `json:"delta_y,omitempty"`
	Key        string `json:"key,omitempty"`
	Text       string `json:"text,omitempty"`
	DurationMS int    `json:"duration_ms,omitempty"`
}

type AppsRequest struct{}

type SnapshotRequest struct {
	RetentionSeconds int `json:"retention_seconds,omitempty"`
}

type ActRequest struct {
	SnapshotID       string   `json:"snapshot_id"`
	Actions          []Action `json:"actions"`
	CaptureAfter     *bool    `json:"capture_after,omitempty"`
	RetentionSeconds int      `json:"retention_seconds,omitempty"`
}

type Driver interface {
	Platform() string
	Capabilities() []string
	Capture(context.Context) (Screenshot, error)
	Apps(context.Context) ([]App, error)
	Act(context.Context, Action, Geometry, bool) error
}
