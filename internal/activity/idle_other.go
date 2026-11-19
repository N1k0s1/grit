//go:build !darwin && !windows && !linux

package activity

import "time"

func idle() (time.Duration, string, error) { return 0, "", ErrUnknown }
