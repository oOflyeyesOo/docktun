package config

import "github.com/kelseyhightower/envconfig"

type Config struct {
	ListenAddr          string `envconfig:"LISTEN_ADDR" default:"0.0.0.0:3001"`
	JWTSecret           string `envconfig:"JWT_SECRET" required:"true"`
	AdminUsername       string `envconfig:"ADMIN_USERNAME" default:"admin"`
	AdminPassword       string `envconfig:"ADMIN_PASSWORD" required:"true"`
	DBPath              string `envconfig:"DB_PATH" default:"./data/app.db"`
	CloudflaredConfig   string `envconfig:"CLOUDFLARED_CONFIG_PATH" default:"/etc/cloudflared/config.yml"`
	CloudflaredContainer string `envconfig:"CLOUDFLARED_CONTAINER" default:"docktun-cloudflared"`
	TunnelName          string `envconfig:"TUNNEL_NAME" default:""`
	MetricsAddr         string `envconfig:"CLOUDFLARED_METRICS" default:"http://docktun-cloudflared:2000/metrics"`
	SelfPath            string `envconfig:"SELF_PATH" default:""`
	MeshMode            string `envconfig:"MESH_MODE" default:"none"`
	MeshConfigPath      string `envconfig:"MESH_CONFIG_PATH" default:"/etc/docktun/mesh.json"`
}

func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
