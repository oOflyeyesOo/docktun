package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (s *Cloudflared) HasConnectorToken() bool {
	data, _ := os.ReadFile(s.providers.tokenFile())
	return strings.TrimSpace(string(data)) != ""
}

// Connector tokens are stored in the shared config directory, never returned
// through the API or passed in process arguments/container environment.
func (s *Cloudflared) SaveConnectorToken(token string) error {
	token = strings.TrimSpace(token)
	if len(token) > 64*1024 || strings.ContainsAny(token, "\r\n") {
		return fmt.Errorf("invalid connector token")
	}
	file := s.providers.tokenFile()
	if token == "" {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".connector-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(token); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}
