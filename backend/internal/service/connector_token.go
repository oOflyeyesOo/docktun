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
	fields := strings.Fields(token)
	for i, field := range fields {
		option := strings.Trim(field, `"'`)
		if option == "--token" && i+1 < len(fields) {
			return strings.Trim(fields[i+1], `"'`)
		}
		if strings.HasPrefix(option, "--token=") {
			return strings.Trim(strings.TrimPrefix(option, "--token="), `"'`)
		}
		if option == "service" && i+2 < len(fields) && fields[i+1] == "install" {
			return strings.Trim(fields[i+2], `"'`)
		}
	}
	return strings.Trim(token, `"'`)
}

func (s *Cloudflared) SaveConnectorToken(token string) error {
	if len(token) > 64*1024 || strings.ContainsAny(token, "\r\n") {
		return fmt.Errorf("invalid connector token: paste a single token or a single-line cloudflared install command")
	}
	raw := token
	token = sanitizeConnectorToken(token)
	if token == "" && strings.TrimSpace(raw) != "" {
		return fmt.Errorf("connector token is missing from the command")
	}
	if strings.ContainsAny(token, " \t\r\n") || strings.HasPrefix(token, "-") {
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
	if err := os.Rename(tmp.Name(), file); err != nil {
		return err
	}
	if err := os.Chmod(file, 0600); err != nil {
		return err
	}
	return nil
}
