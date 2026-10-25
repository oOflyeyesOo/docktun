package main

import (
  "context"
  "log"
  "net/http"
  "os"
  "os/signal"
  "path/filepath"
  "strings"
  "syscall"
  "time"

  "github.com/go-chi/chi/v5"
  chimw "github.com/go-chi/chi/v5/middleware"
  "github.com/go-chi/cors"
  "docktun/internal/config"
  "docktun/internal/db"
  "docktun/internal/handler"
  "docktun/internal/middleware"
  "docktun/internal/service"
  "docktun/internal/ws"
)

func main() {
  cfg, err := config.Load()
  if err != nil {
    log.Fatalf("failed to load config: %v", err)
  }

  database, err := db.New(cfg.DBPath)
  if err != nil {
    log.Fatalf("failed to init db: %v", err)
  }
  defer database.Close()

  hub := ws.NewHub()
  go hub.Run()

  cfdSvc := service.NewCloudflared(cfg, hub)
  cfdSvc.Logs()
  auditSvc := service.NewAuditLogger(database)
  authHandler := handler.NewAuth(cfg, database, auditSvc)
  configSvc := service.NewConfigManager(cfg, database)
  configSvc.EnsureInitialBackup()

  authMw := middleware.NewAuth(cfg.JWTSecret)

  r := chi.NewRouter()
  r.Use(chimw.Logger)
  r.Use(chimw.Recoverer)
  r.Use(cors.Handler(cors.Options{
    AllowedOrigins:   []string{"http://localhost:3000", "http://localhost:8080"},
    AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE"},
    AllowedHeaders:   []string{"Authorization", "Content-Type"},
    AllowCredentials: true,
  }))

  dataDir := filepath.Dir(cfg.DBPath)
  cfAPISvc := service.NewCloudflareAPI(database)
  projectsSvc := service.NewProjects(database, cfg.SelfPath)
  dockerRunSvc := service.NewDockerRun(database, projectsSvc)
  dockerContainersSvc := service.NewDockerContainers(cfg, database, configSvc, cfdSvc, cfAPISvc, auditSvc)
  meshSvc := service.NewMeshService(dataDir, configSvc, cfg)
  remoteAccessSvc := service.NewRemoteAccessService(cfg, configSvc, cfdSvc, cfAPISvc, meshSvc, auditSvc, dataDir, dockerContainersSvc)
  storageSvc := service.NewStorageService(database, cfg)
  storageFilesSvc := service.NewStorageFilesService(database, cfg)

  statusHandler := handler.NewStatus(cfdSvc, cfg)
  controlHandler := handler.NewControl(cfdSvc, auditSvc)
  logsHandler := handler.NewLogs(hub, cfg.JWTSecret)
  configHandler := handler.NewConfig(configSvc, auditSvc, cfAPISvc)
  tcpHandler := handler.NewTcpAccess(cfdSvc)
  auditHandler := handler.NewAudit(auditSvc)
  healthHandler := handler.NewHealthCheck(cfg.CloudflaredConfig)
  metricsHandler := handler.NewMetrics(cfg)
  settingsHandler := handler.NewSettings(database, auditSvc)
  notifHandler := handler.NewNotifications(dataDir)
  projectsHandler := handler.NewProjects(projectsSvc, auditSvc)
  dockerRunHandler := handler.NewDockerRun(dockerRunSvc, auditSvc)
  dockerContainersHandler := handler.NewDockerContainers(dockerContainersSvc)
  meshHandler := handler.NewMesh(meshSvc)
  remoteAccessHandler := handler.NewRemoteAccess(remoteAccessSvc, auditSvc)
  cloudflareHandler := handler.NewCloudflareHandler(cfAPISvc, configSvc)
  storageHandler := handler.NewStorageHandler(storageSvc, storageFilesSvc, auditSvc)
  setupSvc := service.NewSetupService(cfg, database, dockerContainersSvc, cfAPISvc, configSvc, meshSvc, auditSvc, dataDir)
  setupHandler := handler.NewSetup(setupSvc, authHandler)

  r.Get("/healthz", handler.Readiness(database))
  r.Get("/api/setup/status", setupHandler.Status)
  authLimiter := middleware.NewRateLimiter(10, time.Minute)
  r.With(authLimiter.Limit).Post("/api/setup/complete", setupHandler.Complete)
  r.With(authLimiter.Limit).Post("/auth/login", authHandler.Login)
  r.Get("/api/logs/ws", logsHandler.Stream)

  r.Group(func(r chi.Router) {
    r.Use(authMw.Verify)

    r.Get("/api/setup/details", setupHandler.Details)
    r.Post("/api/setup/reconfigure", setupHandler.Reconfigure)
    r.Post("/auth/logout", authHandler.Logout)
    r.Post("/auth/refresh", authHandler.Refresh)

    r.Get("/api/status", statusHandler.Get)
    r.Get("/api/diagnostics", handler.Diagnostics())
    r.Post("/api/control/{action}", controlHandler.Execute)

    r.Get("/api/logs/history", logsHandler.History)

    r.Get("/api/config", configHandler.Get)
    r.Put("/api/config", configHandler.Update)
    r.Get("/api/config/backups", configHandler.ListBackups)
    r.Post("/api/config/backups", configHandler.CreateBackup)
    r.Get("/api/config/backups/{id}", configHandler.GetBackup)
    r.Delete("/api/config/backups/{id}", configHandler.DeleteBackup)
    r.Post("/api/config/validate", configHandler.Validate)

    r.Post("/api/tcp-access/generate", tcpHandler.Generate)
    r.Post("/api/tcp-access/run", tcpHandler.Run)
    r.Delete("/api/tcp-access/run/{pid}", tcpHandler.Stop)

    r.Get("/api/audit", auditHandler.List)

    r.Get("/api/health", healthHandler.Check)
    r.Get("/api/metrics", metricsHandler.Get)
    r.Post("/api/settings/password", settingsHandler.ChangePassword)
    r.Get("/api/notifications", notifHandler.Get)
    r.Put("/api/notifications", notifHandler.Update)
    r.Post("/api/notifications/test", notifHandler.Test)

    r.Get("/api/mesh", meshHandler.Get)
    r.Put("/api/mesh", meshHandler.Save)

    // Remote Access (unified Cloudflare, Tailscale, WireGuard, ZeroConf)
    r.Get("/api/remote-access/overview", remoteAccessHandler.Overview)
    r.Get("/api/remote-access/containers", remoteAccessHandler.ListContainers)
    r.Post("/api/remote-access/containers/expose", remoteAccessHandler.SetContainerExposure)
    r.Get("/api/remote-access/tailscale/status", remoteAccessHandler.TailscaleStatus)
    r.Post("/api/remote-access/tailscale/up", remoteAccessHandler.TailscaleUp)
    r.Post("/api/remote-access/tailscale/down", remoteAccessHandler.TailscaleDown)
    r.Post("/api/remote-access/tailscale/options", remoteAccessHandler.TailscaleSetOptions)
    r.Post("/api/remote-access/tailscale/control", remoteAccessHandler.TailscaleControl)
    r.Get("/api/remote-access/wireguard/status", remoteAccessHandler.WireguardStatus)
    r.Post("/api/remote-access/wireguard/control", remoteAccessHandler.WireguardControl)
    r.Post("/api/remote-access/wireguard/peer", remoteAccessHandler.WireguardAddPeer)
    r.Delete("/api/remote-access/wireguard/peer/{name}", remoteAccessHandler.WireguardDeletePeer)
    r.Post("/api/remote-access/zeroconf/control", remoteAccessHandler.ZeroconfControl)


    r.Get("/api/projects", projectsHandler.List)
    r.Post("/api/projects", projectsHandler.Create)
    r.Post("/api/projects/prune", projectsHandler.Prune)
    r.Put("/api/projects/{id}", projectsHandler.Update)
    r.Delete("/api/projects/{id}", projectsHandler.Delete)
    r.Get("/api/projects/{id}/history", projectsHandler.History)
    r.Get("/api/projects/{id}/output", projectsHandler.Output)
    r.Post("/api/projects/{id}/{action}", projectsHandler.Action)

    r.Get("/api/projects/{id}/compose", projectsHandler.ComposeGet)
    r.Put("/api/projects/{id}/compose", projectsHandler.ComposeSave)
    r.Get("/api/projects/{id}/env", projectsHandler.EnvGet)
    r.Put("/api/projects/{id}/env", projectsHandler.EnvSave)
    r.Get("/api/projects/{id}/files", projectsHandler.FilesList)
    r.Get("/api/projects/{id}/file", projectsHandler.FileGet)
    r.Put("/api/projects/{id}/file", projectsHandler.FileSave)
    r.Post("/api/projects/{id}/file", projectsHandler.FileCreate)
    r.Delete("/api/projects/{id}/file", projectsHandler.FileDelete)

    r.Get("/api/docker/containers", dockerContainersHandler.List)
    r.Get("/api/docker/containers/{id}", dockerContainersHandler.Details)
    r.Get("/api/docker/containers/{id}/exposure", dockerContainersHandler.Exposure)
    r.Post("/api/docker/containers/{id}/exposure", dockerContainersHandler.UpdateExposure)
    r.Post("/api/docker/containers/batch", dockerContainersHandler.Batch)
    r.Post("/api/docker/containers/{id}/network/connect", dockerContainersHandler.ConnectNetwork)
    r.Post("/api/docker/containers/{id}/network/disconnect", dockerContainersHandler.DisconnectNetwork)
    r.Post("/api/docker/containers/{id}/project", dockerContainersHandler.EnsureProject)
    r.Post("/api/docker/containers/{id}/start", dockerContainersHandler.Start)
    r.Post("/api/docker/containers/{id}/stop", dockerContainersHandler.Stop)
    r.Post("/api/docker/containers/{id}/restart", dockerContainersHandler.Restart)
    r.Post("/api/docker/containers/{id}/remove", dockerContainersHandler.Remove)
    r.Get("/api/docker/containers/{id}/logs", dockerContainersHandler.Logs)
    r.Post("/api/docker/containers/{id}/exec", dockerContainersHandler.Exec)
    r.Post("/api/docker/containers/check-updates", dockerContainersHandler.CheckAllUpdates)
    r.Post("/api/docker/containers/{id}/check-update", dockerContainersHandler.CheckUpdate)
    r.Post("/api/docker/containers/{id}/update", dockerContainersHandler.Update)
    r.Get("/api/docker/system/df", dockerContainersHandler.SystemDF)
    r.Post("/api/docker/system/prune", dockerContainersHandler.SystemPrune)
    r.Post("/api/docker/probe", dockerContainersHandler.ProbeTarget)
    r.Post("/api/docker/network/reconcile", dockerContainersHandler.ReconcileNetworks)

    // Storage Management & Reclaim Center
    r.Get("/api/storage/overview", storageHandler.GetOverview)
    r.Get("/api/storage/volumes", storageHandler.ListVolumes)
    r.Delete("/api/storage/volumes/{name}", storageHandler.DeleteVolume)
    r.Post("/api/storage/volumes/prune", storageHandler.PruneVolumes)
    r.Get("/api/storage/images", storageHandler.ListImages)
    r.Delete("/api/storage/images/{id}", storageHandler.DeleteImage)
    r.Post("/api/storage/images/prune", storageHandler.PruneImages)
    r.Get("/api/storage/containers", storageHandler.ListContainers)
    r.Post("/api/storage/containers/prune", storageHandler.PruneContainers)
    r.Post("/api/storage/build-cache/prune", storageHandler.PruneBuildCache)
    r.Post("/api/storage/reclaim", storageHandler.ReclaimBatch)
    r.Post("/api/storage/vacuum-db", storageHandler.VacuumDB)

    // Storage File Manager (browse/edit volumes, containers, projects, host storage)
    r.Get("/api/storage/files/targets", storageHandler.ListTargets)
    r.Get("/api/storage/files/list", storageHandler.ListFiles)
    r.Get("/api/storage/files/read", storageHandler.ReadFile)
    r.Post("/api/storage/files/write", storageHandler.WriteFile)
    r.Post("/api/storage/files/create", storageHandler.CreateItem)
    r.Delete("/api/storage/files/delete", storageHandler.DeleteItem)
    r.Get("/api/storage/files/download", storageHandler.DownloadFile)
    r.Post("/api/storage/files/upload", storageHandler.UploadFile)

    r.Post("/api/docker/run", dockerRunHandler.Run)
    r.Get("/api/docker/run/output/{id}", dockerRunHandler.Output)

    r.Get("/api/cloudflare/settings", cloudflareHandler.GetSettings)
    r.Post("/api/cloudflare/settings", cloudflareHandler.SaveSettings)
    r.Post("/api/cloudflare/sync-all", cloudflareHandler.SyncAllRoutes)
    r.Post("/api/cloudflare/create-wildcard", cloudflareHandler.CreateWildcard)
    r.Get("/api/cloudflare/tunnel", cloudflareHandler.GetTunnelStatus)
    r.Get("/api/cloudflare/access-apps", cloudflareHandler.ListAccessApps)
    r.Post("/api/cloudflare/access-apps", cloudflareHandler.CreateAccessApp)
    r.Delete("/api/cloudflare/access-apps/{id}", cloudflareHandler.DeleteAccessApp)
  })

  // ---------------------------------------------------------------------------
  // Static Frontend SPA Serving (Unified Container)
  // ---------------------------------------------------------------------------
  staticDir := os.Getenv("STATIC_DIR")
  if staticDir == "" {
    staticDir = "./dist"
  }
  if fi, err := os.Stat(staticDir); err == nil && fi.IsDir() {
    fileServer := http.FileServer(http.Dir(staticDir))
    r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
      reqPath := filepath.Clean(r.URL.Path)
      normalizedPath := filepath.ToSlash(reqPath)
      fullPath := filepath.Join(staticDir, reqPath)

      // If requested file exists on disk, serve it
      if info, err := os.Stat(fullPath); err == nil && !info.IsDir() {
        if strings.HasPrefix(normalizedPath, "/assets/") || strings.HasPrefix(normalizedPath, "assets/") {
          w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
        }
        fileServer.ServeHTTP(w, r)
        return
      }

      // If a hashed asset in /assets/ is requested and missing, return 404
      // rather than serving index.html, so Vite's preloadError / dynamic import handlers trigger cleanly
      if strings.HasPrefix(normalizedPath, "/assets/") || strings.HasPrefix(normalizedPath, "assets/") {
        w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
        http.NotFound(w, r)
        return
      }

      // SPA fallback to index.html with no-cache header
      w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
      http.ServeFile(w, r, filepath.Join(staticDir, "index.html"))
    })
    log.Printf("[docktun] serving frontend UI directly from %s", staticDir)
  }

  // Backwards-compatibility auxiliary listener on port 80 (inside container)
  go func() {
    srv80 := &http.Server{
      Addr:         ":80",
      Handler:      r,
      ReadTimeout:  15 * time.Second,
      WriteTimeout: 600 * time.Second,
      IdleTimeout:  60 * time.Second,
    }
    _ = srv80.ListenAndServe()
  }()

  srv := &http.Server{
    Addr:         cfg.ListenAddr,
    Handler:      r,
    ReadTimeout:  15 * time.Second,
    WriteTimeout: 600 * time.Second,
    IdleTimeout:  60 * time.Second,
  }

  ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
  defer stop()

  go func() {
    log.Printf("server listening on %s", cfg.ListenAddr)
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
      log.Fatalf("server error: %v", err)
    }
  }()

  // Automatically sync all ingress hostnames & wildcard route with Cloudflare DNS on startup
  go func() {
    time.Sleep(2 * time.Second)
    if _, err := dockerContainersSvc.ReconcileNetworks(); err != nil {
      log.Printf("[docktun] network reconciliation notice: %v", err)
    }
    time.Sleep(1 * time.Second)
    cfAPISvc.AutoSyncIngressDNS(configSvc)
  }()

  <-ctx.Done()
  log.Println("shutting down...")

  shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
  defer cancel()
  srv.Shutdown(shutdownCtx)
}
