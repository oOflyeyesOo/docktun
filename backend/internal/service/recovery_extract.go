package service

import (
	"archive/tar"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

// Runs only inside a network-isolated helper mounted to a newly created volume.
func ExtractRecoveryVolume(directory string, input io.Reader) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("recovery volume must be empty")
	}
	tr := tar.NewReader(io.LimitReader(input, 32<<30))
	type dirMode struct {
		name     string
		mode     os.FileMode
		uid, gid int
	}
	directories := []dirMode{}
	var total int64
	count := 0
	for {
		header, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		count++
		if count > 500000 {
			return fmt.Errorf("too many volume entries")
		}
		name := strings.TrimSuffix(strings.TrimPrefix(header.Name, "./"), "/")
		if name == "." || name == "" {
			directories = append(directories, dirMode{".", os.FileMode(header.Mode) & 0777, header.Uid, header.Gid})
			continue
		}
		if !fs.ValidPath(name) || strings.Contains(name, "\\") || strings.HasPrefix(header.Name, "/") {
			return fmt.Errorf("unsafe recovery volume path")
		}
		if header.Size < 0 || total+header.Size > 32<<30 {
			return fmt.Errorf("recovery volume exceeds size limit")
		}
		total += header.Size
		if err = root.MkdirAll(path.Dir(name), 0755); err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err = root.MkdirAll(name, 0755); err != nil {
				return err
			}
			directories = append(directories, dirMode{name, os.FileMode(header.Mode) & 0777, header.Uid, header.Gid})
		case tar.TypeReg:
			file, e := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				return e
			}
			_, e = io.CopyN(file, tr, header.Size)
			if e == nil {
				e = file.Chown(header.Uid, header.Gid)
			}
			if e == nil {
				e = file.Chmod(os.FileMode(header.Mode) & 0777)
			}
			closeErr := file.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink:
			target := path.Clean(path.Join(path.Dir(name), header.Linkname))
			if path.IsAbs(header.Linkname) || !fs.ValidPath(target) {
				return fmt.Errorf("escaping recovery symlink")
			}
			if err = root.Symlink(header.Linkname, name); err != nil {
				return err
			}
		case tar.TypeLink:
			target := strings.TrimPrefix(header.Linkname, "./")
			if !fs.ValidPath(target) {
				return fmt.Errorf("unsafe recovery hardlink")
			}
			if err = root.Link(target, name); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported volume entry type")
		}
	}
	for i := len(directories) - 1; i >= 0; i-- {
		d := directories[i]
		if err = root.Chown(d.name, d.uid, d.gid); err != nil {
			return err
		}
		if err = root.Chmod(d.name, d.mode); err != nil {
			return err
		}
	}
	return nil
}
