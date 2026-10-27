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
func sanitizeConnectorToken(raw string) string {
	token := strings.TrimSpace(raw)
	token = strings.Trim(token, `"' `)
	if idx := strings.Index(token, "--token"); idx != -1 {
		sub := strings.TrimSpace(token[idx+len("--token"):])
		sub = strings.TrimPrefix(sub, `"`)
		sub = strings.TrimPrefix(sub, `=`)
		sub = strings.TrimSpace(sub)
		parts := strings.Fields(sub)
		if len(parts) > 0 {
			token = parts[0]
		}
	} else if idx := strings.Index(token, "service install"); idx != -1 {
		sub := strings.TrimSpace(token[idx+len("service install"):])
		parts := strings.Fields(sub)
		if len(parts) > 0 {
			token = parts[0]
		}
	}
	return strings.Trim(token, `"' `)
}

func (s *Cloudflared) SaveConnectorToken(token string) error {
	token = sanitizeConnectorToken(token)
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
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0755)
	tmp, err := os.CreateTemp(dir, ".connector-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0644); err != nil {
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
	if err := os.Rename(tmp.Name(), file); err != nil {
		return err
	}
	_ = os.Chmod(file, 0644)
	return nil
}
