//go:build !unix

package kitty

import (
	"errors"
	"os"
)

// Preview is not supported on this platform.
func Preview(_ *os.File, _ []byte, _ int) error { return errors.ErrUnsupported }

// Thumbnail is not supported on this platform.
func Thumbnail(_ *os.File, _ []byte, _ int) error { return errors.ErrUnsupported }
