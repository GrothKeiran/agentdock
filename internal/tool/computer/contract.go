package computer

import toolcontract "github.com/uvwt/agentdock/internal/tool/contract"

func InputSchema(name string) (map[string]any, bool) {
	stringProp := toolcontract.String
	intProp := toolcontract.BoundedInteger
	props := map[string]any{}
	var required []string
	switch name {
	case ToolApps:
	case ToolSnapshot:
		props["retention_seconds"] = intProp("Screenshot Artifact retention seconds. Zero uses the privacy-oriented default of 300; capped at 604800.", 0, 604800)
	case ToolAct:
		props["snapshot_id"] = stringProp("Opaque id returned by the most recent computer_snapshot or computer_act call. It prevents actions against a stale or different desktop image.")
		props["actions"] = actionsSchema()
		props["capture_after"] = toolcontract.Boolean("Capture and return the resulting desktop state. Defaults to true. When false, call computer_snapshot before any further action.")
		props["retention_seconds"] = intProp("Final screenshot Artifact retention seconds. Zero uses the privacy-oriented default of 300; capped at 604800.", 0, 604800)
		required = []string{"snapshot_id", "actions"}
	default:
		return nil, false
	}
	return toolcontract.InputObject(props, required...), true
}

func OutputSchema(name string) (map[string]any, bool) {
	props := map[string]any{
		"computer_ok": toolcontract.Boolean("Whether the desktop operation succeeded."),
		"platform":    toolcontract.String("Host desktop platform."),
		"error":       toolcontract.OpenObject("Structured desktop-control error."),
	}
	switch name {
	case ToolApps:
		props["apps"] = toolcontract.ObjectArray("Visible desktop applications/windows.")
		props["capabilities"] = toolcontract.StringArray("Available desktop-control capabilities.")
	case ToolSnapshot:
		addSnapshotOutputProperties(props)
	case ToolAct:
		props["executed_count"] = toolcontract.Integer("Number of actions completed in order.")
		props["needs_snapshot"] = toolcontract.Boolean("Whether a fresh snapshot is required before another action.")
		props["result_unknown"] = toolcontract.Boolean("Whether a failed native input may have partially changed the desktop.")
		addSnapshotOutputProperties(props)
	default:
		return nil, false
	}
	return toolcontract.OutputObject(props), true
}

func addSnapshotOutputProperties(props map[string]any) {
	props["snapshot_id"] = toolcontract.String("Opaque id that binds subsequent actions to this screenshot.")
	props["display"] = toolcontract.OpenObject("Captured desktop geometry. Action coordinates are relative to this image.")
	props["foreground_app"] = toolcontract.OpenObject("Foreground application when the platform can identify it.")
	props["screenshot"] = toolcontract.OpenObject("Published screenshot Artifact reference.")
}

func actionsSchema() map[string]any {
	coord := func(description string) map[string]any {
		return map[string]any{"type": "integer", "minimum": 0, "maximum": 200000, "description": description}
	}
	return map[string]any{
		"type": "array", "minItems": 1, "maxItems": 100,
		"description": "Actions run serially against the desktop represented by snapshot_id. Coordinates are screenshot pixels with top-left (0,0). Every mouse_down must have a matching mouse_up in the same batch; while held, only move, wait, and that mouse_up are allowed.",
		"items": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"action"},
			"properties": map[string]any{
				"action":      map[string]any{"type": "string", "enum": []string{"move", "click", "mouse_down", "mouse_up", "drag", "scroll", "key", "type", "wait"}},
				"x":           coord("Start/target horizontal screenshot coordinate."),
				"y":           coord("Start/target vertical screenshot coordinate."),
				"to_x":        coord("Drag destination horizontal screenshot coordinate."),
				"to_y":        coord("Drag destination vertical screenshot coordinate."),
				"button":      map[string]any{"type": "string", "enum": []string{"left", "middle", "right"}, "description": "Mouse button. Defaults to left."},
				"click_count": map[string]any{"type": "integer", "minimum": 1, "maximum": 3, "description": "Click count. Defaults to 1."},
				"delta_x":     map[string]any{"type": "integer", "minimum": -100, "maximum": 100, "description": "Horizontal scroll ticks; positive scrolls right."},
				"delta_y":     map[string]any{"type": "integer", "minimum": -100, "maximum": 100, "description": "Vertical scroll ticks; positive scrolls down."},
				"key":         map[string]any{"type": "string", "minLength": 1, "maxLength": 4096, "description": "Key chord or space-separated chord macro, e.g. ctrl+l or Return."},
				"text":        map[string]any{"type": "string", "maxLength": 32768, "description": "Literal Unicode text to type."},
				"duration_ms": map[string]any{"type": "integer", "minimum": 0, "maximum": 10000, "description": "Wait duration, or drag duration. Defaults to 250ms for drag."},
			},
		},
	}
}
