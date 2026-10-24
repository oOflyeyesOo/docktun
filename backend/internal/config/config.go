package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	SetupToken           string `envconfig:"SETUP_TOKEN" default:""`
	ListenAddr           string `envconfig:"LISTEN_ADDR" default:"0.0.0.0:3001"`
	JWTSecret            string `envconfig:"JWT_SECRET" default:""`
	AdminUsername        string `envconfig:"ADMIN_USERNAME" default:"admin"`
	AdminPassword        string `envconfig:"ADMIN_PASSWORD" default:""`
	DBPath               string `envconfig:"DB_PATH" default:"./data/app.db"`
	CloudflaredConfig    string `envconfig:"CLOUDFLARED_CONFIG_PATH" default:"/etc/cloudflared/config.yml"`
	CloudflaredContainer string `envconfig:"CLOUDFLARED_CONTAINER" default:"docktun-cloudflared"`
	TunnelName           string `envconfig:"TUNNEL_NAME" default:""`
	MetricsAddr          string `envconfig:"CLOUDFLARED_METRICS" default:"http://docktun-cloudflared:2000/metrics"`
	SelfPath             string `envconfig:"SELF_PATH" default:""`
	MeshMode             string `envconfig:"MESH_MODE" default:"none"`
	MeshConfigPath       string `envconfig:"MESH_CONFIG_PATH" default:"/etc/docktun/mesh.json"`
}

func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, err
	}

	// Refuse to start with an ephemeral or unreadable signing secret.
	if strings.TrimSpace(cfg.JWTSecret) == "" {
		secretFile := filepath.Join(filepath.Dir(cfg.DBPath), "jwt.secret")
		token, err := os.ReadFile(secretFile)
		if os.IsNotExist(err) {
			buf := make([]byte, 32)
			if _, err := rand.Read(buf); err != nil {
				return nil, err
			}
			if err := os.MkdirAll(filepath.Dir(secretFile), 0700); err != nil {
				return nil, err
			}
			f, openErr := os.OpenFile(secretFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if openErr != nil {
				return nil, openErr
			}
			_, writeErr := f.WriteString(hex.EncodeToString(buf))
			closeErr := f.Close()
			if writeErr != nil {
				return nil, writeErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
			token, err = os.ReadFile(secretFile)
		}
		if err != nil {
			return nil, fmt.Errorf("read JWT secret: %w", err)
		}
		cfg.JWTSecret = strings.TrimSpace(string(token))
	}
	if len(cfg.JWTSecret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must contain at least 32 bytes")
	}

	// Bootstrap ownership must require a secret available on the host.
	if strings.TrimSpace(cfg.SetupToken) == "" {
		secretFile := filepath.Join(filepath.Dir(cfg.DBPath), "setup.token")
		token, err := os.ReadFile(secretFile)
		if os.IsNotExist(err) {
			buf := make([]byte, 32)
			if _, err := rand.Read(buf); err != nil {
				return nil, err
			}
			if err := os.MkdirAll(filepath.Dir(secretFile), 0700); err != nil {
				return nil, err
			}
			f, openErr := os.OpenFile(secretFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if openErr != nil {
				return nil, openErr
			}
			_, writeErr := f.WriteString(hex.EncodeToString(buf))
			closeErr := f.Close()
			if writeErr != nil {
				return nil, writeErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
			token, err = os.ReadFile(secretFile)
		}
		if err != nil {
			return nil, fmt.Errorf("read setup token: %w", err)
		}
		cfg.SetupToken = strings.TrimSpace(string(token))
	}
	if len(cfg.SetupToken) < 32 {
		return nil, fmt.Errorf("SETUP_TOKEN must contain at least 32 bytes")
	}
	return &cfg, nil
}
