export const defaultComposeExample = `services:
  my-app:
    image: nginx:alpine
    container_name: my-app
    restart: unless-stopped
    ports:
      - "8080:80"
`

export const composeTemplates = [
  {
    id: 'nginx-server',
    name: 'Nginx Web Server',
    category: 'web',
    icon: 'Globe',
    description: 'High-performance HTTP server & reverse proxy with volume persistence on its own Compose network.',
    ports: ['8080:80'],
    compose: `services:
  nginx:
    image: nginx:alpine
    container_name: nginx-server
    restart: unless-stopped
    ports:
      - "8080:80"
    volumes:
      - ./html:/usr/share/nginx/html:ro

`,
  },
  {
    id: 'postgres-db',
    name: 'PostgreSQL 16 & pgAdmin 4',
    category: 'database',
    icon: 'Database',
    description: 'Robust SQL database engine bundled with pgAdmin 4 web GUI administration dashboard.',
    ports: ['5050:80'],
    compose: `services:
  postgres:
    image: postgres:16-alpine
    container_name: postgres-db
    restart: unless-stopped
    environment:
      POSTGRES_USER: \${POSTGRES_USER:-docktun}
      POSTGRES_PASSWORD: \${POSTGRES_PASSWORD:-ChangeMeSecurePassword123!}
      POSTGRES_DB: \${POSTGRES_DB:-appdb}
    volumes:
      - postgres_data:/var/lib/postgresql/data

  pgadmin:
    image: dpage/pgadmin4:latest
    container_name: pgadmin
    restart: unless-stopped
    environment:
      PGADMIN_DEFAULT_EMAIL: \${PGADMIN_EMAIL:-admin@docktun.local}
      PGADMIN_DEFAULT_PASSWORD: \${PGADMIN_PASSWORD:-AdminPassword123!}
    ports:
      - "5050:80"
    depends_on:
      - postgres

volumes:
  postgres_data:

`,
    env: `POSTGRES_USER=docktun
POSTGRES_PASSWORD=ChangeMeSecurePassword123!
POSTGRES_DB=appdb
PGADMIN_EMAIL=admin@docktun.local
PGADMIN_PASSWORD=AdminPassword123!
`,
  },
  {
    id: 'redis-cache',
    name: 'Redis 7 & Commander GUI',
    category: 'database',
    icon: 'Cpu',
    description: 'In-memory key-value data structure store & cache with Redis Commander browser explorer.',
    ports: ['8081:8081'],
    compose: `services:
  redis:
    image: redis:7-alpine
    container_name: redis-cache
    restart: unless-stopped
    command: redis-server --appendonly yes
    volumes:
      - redis_data:/data

  redis-commander:
    image: rediscommander/redis-commander:latest
    container_name: redis-commander
    restart: unless-stopped
    environment:
      - REDIS_HOSTS=local:redis:6379
    ports:
      - "8081:8081"
    depends_on:
      - redis

volumes:
  redis_data:

`,
  },
  {
    id: 'uptime-kuma',
    name: 'Uptime Kuma',
    category: 'monitoring',
    icon: 'Activity',
    description: 'Self-hosted monitoring tool with real-time incident reports, latency graphs, and alerts.',
    ports: ['3001:3001'],
    compose: `services:
  uptime-kuma:
    image: louislam/uptime-kuma:1
    container_name: uptime-kuma
    restart: unless-stopped
    volumes:
      - kuma_data:/app/data
    ports:
      - "3001:3001"

volumes:
  kuma_data:

`,
  },
  {
    id: 'portainer',
    name: 'Portainer CE',
    category: 'utility',
    icon: 'Box',
    description: 'Centralized container management GUI for Docker containers, images, volumes, and networks.',
    ports: ['9443:9443', '9000:9000'],
    compose: `services:
  portainer:
    image: portainer/portainer-ce:latest
    container_name: portainer
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    volumes:
      - /etc/localtime:/etc/localtime:ro
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - portainer_data:/data
    ports:
      - "9443:9443"
      - "9000:9000"

volumes:
  portainer_data:

`,
  },
  {
    id: 'jellyfin',
    name: 'Jellyfin Media Server',
    category: 'media',
    icon: 'Film',
    description: 'Free Software Media System that lets you collect, manage, and stream your media anywhere.',
    ports: ['8096:8096'],
    compose: `services:
  jellyfin:
    image: jellyfin/jellyfin:latest
    container_name: jellyfin
    restart: unless-stopped
    environment:
      - TZ=America/Chicago
    volumes:
      - jellyfin_config:/config
      - jellyfin_cache:/cache
      - ./media:/media:ro
    ports:
      - "8096:8096"

volumes:
  jellyfin_config:
  jellyfin_cache:

`,
  },
  {
    id: 'nextcloud',
    name: 'Nextcloud Hub',
    category: 'utility',
    icon: 'HardDrive',
    description: 'Self-hosted productivity suite with file storage, sharing, office documents, and calendar syncing.',
    ports: ['8088:80'],
    compose: `services:
  nextcloud:
    image: nextcloud:latest
    container_name: nextcloud
    restart: unless-stopped
    ports:
      - "8088:80"
    volumes:
      - nextcloud_data:/var/www/html
    environment:
      - NEXTCLOUD_ADMIN_USER=admin
      - NEXTCLOUD_ADMIN_PASSWORD=AdminPasswordChangeMe!

volumes:
  nextcloud_data:

`,
  },
  {
    id: 'vaultwarden',
    name: 'Vaultwarden (Bitwarden)',
    category: 'utility',
    icon: 'Key',
    description: 'Lightweight Bitwarden-compatible password manager backend written in Rust.',
    ports: ['8080:80'],
    compose: `services:
  vaultwarden:
    image: vaultwarden/server:latest
    container_name: vaultwarden
    restart: unless-stopped
    environment:
      - WEBSOCKET_ENABLED=true
      - SIGNUPS_ALLOWED=true
    volumes:
      - vaultwarden_data:/data
    ports:
      - "8080:80"

volumes:
  vaultwarden_data:

`,
  },
  {
    id: 'mediastack',
    name: 'Media Automation Stack',
    category: 'media',
    icon: 'Tv',
    description: 'Automated TV, Movie, and Indexer stack combining Sonarr, Radarr, and Prowlarr.',
    ports: ['8989:8989', '7878:7878', '9696:9696'],
    compose: `services:
  sonarr:
    image: lscr.io/linuxserver/sonarr:latest
    container_name: sonarr
    restart: unless-stopped
    environment:
      - PUID=1000
      - PGID=1000
      - TZ=America/Chicago
    volumes:
      - sonarr_config:/config
      - ./tv:/tv
      - ./downloads:/downloads
    ports:
      - "8989:8989"

  radarr:
    image: lscr.io/linuxserver/radarr:latest
    container_name: radarr
    restart: unless-stopped
    environment:
      - PUID=1000
      - PGID=1000
      - TZ=America/Chicago
    volumes:
      - radarr_config:/config
      - ./movies:/movies
      - ./downloads:/downloads
    ports:
      - "7878:7878"

  prowlarr:
    image: lscr.io/linuxserver/prowlarr:latest
    container_name: prowlarr
    restart: unless-stopped
    environment:
      - PUID=1000
      - PGID=1000
      - TZ=America/Chicago
    volumes:
      - prowlarr_config:/config
    ports:
      - "9696:9696"

volumes:
  sonarr_config:
  radarr_config:
  prowlarr_config:

`,
  },
  {
    id: 'movie-night',
    name: 'Movie Night VR Cinema',
    category: 'media',
    icon: 'Film',
    description: 'Multiplayer VR cinema for Meta Quest with hardware transcoding, Stremio, Jellyfin, and Cloudflare tunnel support.',
    ports: ['3000:3000'],
    compose: `services:
  cinema:
    image: ghcr.io/ooflyeyesoo/media-vr:latest
    container_name: movie-night
    restart: unless-stopped
    ports:
      - "3000:3000"
    extra_hosts:
      - "host.docker.internal:host-gateway"
    environment:
      - PORT=3000
      - BROWSER_SERVICE_URL=http://browser:9000
      - STREAMING_SERVICE_URL=http://media:11470
      - MEDIA_ALLOWED_HOSTS=media:11470,stremio:11470
      - ALLOW_PRIVATE_NETWORKS=true
      - JELLYFIN_URL=\${JELLYFIN_URL:-}
      - MEDIASTORM_URL=\${MEDIASTORM_URL:-}
      - MEDIASTORM_KEY=\${MEDIASTORM_KEY:-}
      - SILO_URL=\${SILO_URL:-}
      - SILO_TOKEN=\${SILO_TOKEN:-}
      - MEDIA_MAX_HEIGHT=1080
      - MEDIA_MAX_FPS=60
      - MEDIA_DISABLE_REMUX=false
    volumes:
      - movie_night_data:/share
    depends_on:
      - media
      - browser

  browser:
    image: ghcr.io/ooflyeyesoo/media-vr-browser:latest
    container_name: movie-night-browser
    restart: unless-stopped
    shm_size: 1gb
    tmpfs:
      - /tmp:size=2147483648,mode=1777
    volumes:
      - movie_night_browser_data:/data

  media:
    image: stremio/server:latest
    container_name: movie-night-media
    restart: unless-stopped
    environment:
      - APP_PATH=/data
    volumes:
      - movie_night_media_data:/data

volumes:
  movie_night_data:
  movie_night_media_data:
  movie_night_browser_data:

`,
    env: `# Optional Media Backends:
JELLYFIN_URL=
MEDIASTORM_URL=
MEDIASTORM_KEY=
SILO_URL=
SILO_TOKEN=

# Notifications & Subtitles (Optional):
DISCORD_WEBHOOK_URL=
OPENSUBTITLES_API_KEY=
`,
  },
];
