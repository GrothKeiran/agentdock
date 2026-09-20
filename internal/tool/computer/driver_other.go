//go:build !windows && !darwin && !linux

package computer

func newPlatformDriver(string) Driver {
	return &unsupportedDriver{platform: "unsupported"}
}
