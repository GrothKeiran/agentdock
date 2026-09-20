package computer

import (
	"fmt"
	"strings"
)

func validateKeyMacro(key string) error {
	for _, chord := range strings.Fields(strings.TrimSpace(key)) {
		parts := strings.Split(strings.ToLower(chord), "+")
		if len(parts) == 0 || parts[len(parts)-1] == "" {
			return fmt.Errorf("invalid key chord %q", chord)
		}
		for _, modifier := range parts[:len(parts)-1] {
			switch modifier {
			case "shift", "ctrl", "control", "alt", "option", "super", "win", "cmd", "command", "meta":
			default:
				return fmt.Errorf("unsupported key modifier %q", modifier)
			}
		}
	}
	return nil
}

func isBlockedSystemKey(platform, key string) bool {
	for _, chord := range strings.Fields(strings.ToLower(strings.TrimSpace(key))) {
		parts := strings.Split(chord, "+")
		seen := make(map[string]bool, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			switch part {
			case "command", "cmd", "meta", "win":
				part = "super"
			case "control":
				part = "ctrl"
			case "option":
				part = "alt"
			case "esc":
				part = "escape"
			}
			seen[part] = true
		}
		if (seen["alt"] && seen["tab"]) ||
			(seen["alt"] && seen["f4"]) ||
			(seen["ctrl"] && seen["alt"] && seen["delete"]) ||
			(seen["ctrl"] && seen["shift"] && seen["escape"]) {
			return true
		}
		switch platform {
		case "windows", "linux":
			if seen["super"] ||
				(seen["alt"] && (seen["escape"] || seen["space"])) ||
				(seen["ctrl"] && seen["escape"]) {
				return true
			}
		case "darwin":
			if seen["super"] && (seen["q"] || seen["tab"] || seen["space"] || (seen["ctrl"] && seen["q"]) || (seen["alt"] && seen["escape"])) {
				return true
			}
		default:
			if seen["super"] && (seen["q"] || seen["tab"] || seen["space"] || seen["l"]) {
				return true
			}
		}
	}
	return false
}
