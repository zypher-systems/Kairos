//go:build !linux

package storage

import "errors"

// renameNoReplace has no portable equivalent outside Linux.
func renameNoReplace(dir tree, oldname, newname string) error {
	return errors.ErrUnsupported
}
