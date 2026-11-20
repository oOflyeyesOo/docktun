//go:build windows

package service

import "os"

func getSystemFileOwnership(fi os.FileInfo) (int, int, bool) {
	return 0, 0, false
}
