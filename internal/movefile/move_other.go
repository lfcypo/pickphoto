//go:build !windows

package movefile

import "os"

func Move(source, target string) error {
	return moveWithLink(source, target, os.Link)
}

func IsBusy(error) bool {
	return false
}
