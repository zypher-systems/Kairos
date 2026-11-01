package storage

import (
	"path/filepath"

	"golang.org/x/sys/unix"
)

// renameNoReplace moves oldname to newname, both in one directory of dir,
// failing with EEXIST if newname exists. The directory is opened through
// dir, so a rooted tree still confines it, and the names passed to the
// kernel are bare, so no symlink is followed from there.
func renameNoReplace(dir tree, oldname, newname string) error {
	parent, err := dir.Open(filepath.Dir(newname))
	if err != nil {
		return err
	}
	defer parent.Close()
	fd := int(parent.Fd())
	return unix.Renameat2(fd, filepath.Base(oldname), fd, filepath.Base(newname), unix.RENAME_NOREPLACE)
}
