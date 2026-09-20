package computer

import (
	"context"
	"errors"
)

type unsupportedDriver struct{ platform string }

func (d *unsupportedDriver) Platform() string       { return d.platform }
func (d *unsupportedDriver) Capabilities() []string { return nil }
func (d *unsupportedDriver) Capture(context.Context) (Screenshot, error) {
	return Screenshot{}, errors.New("computer use is not supported on this platform")
}
func (d *unsupportedDriver) Apps(context.Context) ([]App, error) {
	return nil, errors.New("computer use is not supported on this platform")
}
func (d *unsupportedDriver) Act(context.Context, Action, Geometry, bool) error {
	return errors.New("computer use is not supported on this platform")
}
