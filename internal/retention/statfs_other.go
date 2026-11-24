//go:build !linux

package retention

import "errors"

// Statfs isn't implemented off Linux, where the server doesn't run (ADR
// 0001): the low-disk check logs that it can't measure, and does nothing.
func Statfs(string) (Space, error) {
	return Space{}, errors.New("free space can't be measured on this platform")
}
