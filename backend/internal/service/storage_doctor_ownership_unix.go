//go:build !windows

package service

import (
	"os"
	"syscall"
)

func getSystemFileOwnership(fi os.FileInfo) (int, int, bool) {
	if fi == nil {
		return -1, -1, false
	}
	if stat, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(stat.Uid), int(stat.Gid), true
	}
	return -1, -1, false
}
