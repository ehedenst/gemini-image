//go:build !unix

package kitty

import (
	"errors"
	"os"
)

// Preview is not supported on this platform.
func Preview(_ *os.File, _ []byte) error { return errors.ErrUnsupported }
