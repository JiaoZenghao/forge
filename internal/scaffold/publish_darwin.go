//go:build darwin

package scaffold

import "golang.org/x/sys/unix"

func publish(from, to string) error { return unix.RenamexNp(from, to, unix.RENAME_EXCL) }
