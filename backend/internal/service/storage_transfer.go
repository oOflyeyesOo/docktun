package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"
)

func transferPath(value string) (string, error) {
	value = strings.ReplaceAll(value, "\\", "/")
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return "", fmt.Errorf("parent directory traversal is not allowed")
		}
	}
	value = strings.TrimLeft(value, "/")
	if strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("invalid path")
	}
	if value == "" {
		return ".", nil
	}
	return path.Clean(value), nil
}
func transferTarget(kind, id string) error {
	if kind == "volume" || kind == "container" {
		if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`).MatchString(id) {
			return fmt.Errorf("invalid storage target")
		}
	}
	if kind == "docktun" && id != "app_data" && id != "cloudflared" && id != "projects" {
		return fmt.Errorf("unknown DockTun storage target")
	}
	return nil
}

type temporaryDownload struct{ *os.File }

func (f temporaryDownload) Close() error {
	name := f.Name()
	err := f.File.Close()
	_ = os.Remove(name)
	return err
}

func (s *StorageFilesService) GetDownloadStream(kind, id, value string) (io.ReadCloser, string, int64, error) {
	rel, err := transferPath(value)
	if err != nil {
		return nil, "", 0, err
	}
	if err = transferTarget(kind, id); err != nil {
		return nil, "", 0, err
	}
	name := path.Base(rel)
	if kind == "docktun" || kind == "project" {
		root, err := os.OpenRoot(s.resolveHostBasePath(kind, id))
		if err != nil {
			return nil, "", 0, err
		}
		defer root.Close()
		f, err := root.Open(rel)
		if err != nil {
			return nil, "", 0, err
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			f.Close()
			return nil, "", 0, fmt.Errorf("only regular files can be downloaded")
		}
		return f, name, info.Size(), nil
	}
	if kind != "volume" && kind != "container" {
		return nil, "", 0, fmt.Errorf("unknown storage target type")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	f, err := os.CreateTemp("", "docktun-download-*")
	if err != nil {
		return nil, "", 0, err
	}
	success := false
	defer func() {
		if !success {
			if f != nil {
				f.Close()
				os.Remove(f.Name())
			}
		}
	}()
	var args []string
	if kind == "volume" {
		args = []string{"run", "--rm", "--network", "none", "--mount", "type=volume,source=" + id + ",target=/mnt_vol,readonly", s.getRunnerImage(), "cat", "/mnt_vol/" + rel}
	} else {
		args = []string{"exec", id, "cat", "/" + rel}
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = f
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err = cmd.Run(); err != nil {
		if kind != "container" {
			return nil, "", 0, fmt.Errorf("download failed: %s", stderr.String())
		}
		// docker cp also supports stopped/minimal containers without cat.
		if err = f.Truncate(0); err != nil {
			return nil, "", 0, err
		}
		f.Close()
		if out, e := exec.CommandContext(ctx, "docker", "cp", id+":/"+rel, f.Name()).CombinedOutput(); e != nil {
			return nil, "", 0, fmt.Errorf("download failed: %s", out)
		}
		f, err = os.Open(f.Name())
		if err != nil {
			return nil, "", 0, err
		}
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, "", 0, fmt.Errorf("only regular files can be downloaded")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return nil, "", 0, err
	}
	success = true
	return temporaryDownload{f}, name, info.Size(), nil
}

func (s *StorageFilesService) UploadFile(kind, id, directory, filename string, reader io.Reader) error {
	dir, err := transferPath(directory)
	if err != nil {
		return err
	}
	if err = transferTarget(kind, id); err != nil {
		return err
	}
	if filename == "" || filename == "." || filename == ".." || strings.ContainsAny(filename, "/\\\x00\r\n") {
		return fmt.Errorf("invalid upload filename")
	}
	target := path.Join(dir, filename)
	if kind == "docktun" || kind == "project" {
		root, err := os.OpenRoot(s.resolveHostBasePath(kind, id))
		if err != nil {
			return err
		}
		defer root.Close()
		if err = root.MkdirAll(dir, 0755); err != nil {
			return err
		}
		f, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, reader)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	if kind != "volume" && kind != "container" {
		return fmt.Errorf("unknown storage target type")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	// Spool once, allowing a retry through docker cp without buffering entire uploads.
	f, err := os.CreateTemp("", "docktun-upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = io.Copy(f, reader); err != nil {
		return err
	}
	if _, err = f.Seek(0, 0); err != nil {
		return err
	}
	dest := "/" + target
	args := []string{"exec", "-i", id}
	if kind == "volume" {
		dest = "/mnt_vol/" + target
		args = []string{"run", "--rm", "-i", "--network", "none", "--mount", "type=volume,source=" + id + ",target=/mnt_vol", s.getRunnerImage()}
	}
	args = append(args, "sh", "-c", `mkdir -p "$1" && cat > "$2"`, "storage", path.Dir(dest), dest)
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = f
	if out, e := cmd.CombinedOutput(); e != nil {
		if kind == "container" {
			if cpOut, cpErr := exec.CommandContext(ctx, "docker", "cp", f.Name(), id+":"+dest).CombinedOutput(); cpErr == nil {
				return nil
			} else {
				return fmt.Errorf("upload failed (destination parent must exist in stopped/minimal containers): %s", cpOut)
			}
		}
		return fmt.Errorf("upload failed: %s", out)
	}
	return nil
}
