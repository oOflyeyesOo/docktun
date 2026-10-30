package main

import (
	"docktun/internal/templateprobe"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	encoder := json.NewEncoder(os.Stdout)
	switch os.Args[1] {
	case "ports":
		var request templateprobe.Request
		if json.NewDecoder(os.Stdin).Decode(&request) != nil {
			os.Exit(2)
		}
		encoder.Encode(templateprobe.FindPorts(request))
	case "filesystem":
		var stat unix.Statfs_t
		if err := unix.Statfs("/checked", &stat); err != nil {
			fmt.Fprintln(os.Stderr, "cannot inspect filesystem")
			os.Exit(1)
		}
		read, write := uint32(unix.R_OK), uint32(unix.W_OK)
		if info, err := os.Stat("/checked"); err == nil && info.IsDir() {
			read |= unix.X_OK
			write |= unix.X_OK
		}
		encoder.Encode(map[string]any{"free_bytes": uint64(stat.Bavail) * uint64(stat.Bsize), "readable": unix.Access("/checked", read) == nil, "writable": unix.Access("/checked", write) == nil, "uid": os.Getuid(), "gid": os.Getgid()})
	default:
		os.Exit(2)
	}
}
