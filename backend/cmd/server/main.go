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

	"docktun/internal/config"
	"docktun/internal/db"
	"docktun/internal/handler"
	"docktun/internal/middleware"
	"docktun/internal/service"
	"docktun/internal/ws"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "recovery-volume-extract" {
		if err := service.ExtractRecoveryVolume(os.Args[2], os.Stdin); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "recovery-instance-check" {
		if _, err := os.Stat(os.Args[2]); err != nil {
			log.Fatal(err)
		}
		snapshot, err := db.New(os.Args[2])
		if err != nil {
			log.Fatal(err)
		}
		defer snapshot.Close()
		var result string
		if err = snapshot.QueryRow("PRAGMA quick_check").Scan(&result); err != nil || result != "ok" {
			log.Fatal("restored database integrity check failed")
		}
		var users int
		if snapshot.QueryRow("SELECT count(*) FROM users").Scan(&users) != nil || users < 1 {
			log.Fatal("restored database has no administrator")
		}
		return
	}

	if len(os.Args) == 6 && os.Args[1] == "update-worker" {
		if err := service.RunJournaledWorker(os.Args[2], os.Args[5], func() error { return service.RunSelfUpdateWorker(os.Args[2], os.Args[3], os.Args[4]) }); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) == 5 && os.Args[1] == "services-worker" {
		if err := service.RunJournaledWorker(os.Args[2], os.Args[4], func() error { return service.RunManagedServiceWorker(os.Args[2], os.Args[3]) }); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) == 5 && os.Args[1] == "update-worker" {
		if err := service.RunSelfUpdateWorker(os.Args[2], os.Args[3], os.Args[4]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) == 4 && os.Args[1] == "services-worker" {
		if err := service.RunManagedServiceWorker(os.Args[2], os.Args[3]); err != nil {
			log.Fatal(err)
		}
		return
	}
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	database, err := db.New(cfg.DBPath)
	if err != nil {
		log.Fatalf("failed to init db: %v", err)
	}
	defer database.Close()
	var recoveryReview string
	if database.QueryRow(`SELECT value FROM system_settings WHERE key='recovery_review_required'`).Scan(&recoveryReview) == nil && recoveryReview == "true" {
		cfg.RecoveryMode = true
	}

	hub := ws.NewHub()
	go hub.Run()

	cfdSvc := service.NewCloudflared(cfg, hub)
	cfdSvc.Logs()
	// Optional companions are created on demand unless the operator opts into staging.
	if !cfg.RecoveryMode && os.Getenv("DOCKTUN_PREPARE_PROVIDERS") == "true" {
		providers := service.NewProviderContainers(cfg)
		for _, name := range []string{"cloudflare", "tailscale", "wireguard"} {
			go func(provider string) {
				if err := providers.Prepare(provider); err != nil {
					log.Printf("Prepare optional %s (left stopped): %v", provider, err)
				}
			}(name)
		}
	}
	auditSvc := service.NewAuditLogger(database)
	authHandler := handler.NewAuth(cfg, database, auditSvc)
	if err := authHandler.BootstrapError(); err != nil {
		log.Fatalf("initialize administrator: %v", err)
	}
	configSvc := service.NewConfigManager(cfg, database)
	configSvc.EnsureInitialBackup()

	authMw := middleware.NewAuth(cfg.JWTSecret).WithValidator(func(raw string) (int, string, error) {
		c, err := authHandler.Sessions().Verify(raw, "access")
		if err != nil {
			return 0, "", err
		}
		return c.UserID, c.Username, nil
	})

	r := chi.NewRouter()
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(middleware.SecurityHeaders)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://localhost:3000", "http://localhost:8080"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE"},
		AllowedHeaders:   []string{"Authorization", "Content-Type"},
		AllowCredentials: true,
	}))

	dataDir := filepath.Dir(cfg.DBPath)
	cfAPISvc := service.NewCloudflareAPI(database)
	// Preserve connector credentials saved by older onboarding versions or environment variables.
	if !cfdSvc.HasConnectorToken() {
		envToken := strings.TrimSpace(os.Getenv("TUNNEL_TOKEN"))
		if envToken == "" {
			envToken = strings.TrimSpace(os.Getenv("CLOUDFLARE_TUNNEL_TOKEN"))
		}
		if envToken == "" {
			envToken = strings.TrimSpace(os.Getenv("CF_TUNNEL_TOKEN"))
		}
		if envToken != "" {
			if err := cfdSvc.SaveConnectorToken(envToken); err != nil {
				log.Printf("[docktun] Failed to save connector token from environment: %v", err)
			} else {
				log.Printf("[docktun] Loaded Cloudflare tunnel connector token from environment")
			}
		} else {
			var legacyToken string
			if database.QueryRow(`SELECT value FROM system_settings WHERE key='cloudflare_tunnel_token'`).Scan(&legacyToken) == nil && legacyToken != "" {
				if err := cfdSvc.SaveConnectorToken(legacyToken); err != nil {
					log.Printf("Migrate connector credentials: %v", err)
				}
			}
		}
	}
	projectsSvc := service.NewProjects(database, cfg.SelfPath)
	dockerRunSvc := service.NewDockerRun(database, projectsSvc)
	dockerContainersSvc := service.NewDockerContainers(cfg, database, configSvc, cfdSvc, cfAPISvc, auditSvc)
	meshSvc := service.NewMeshService(dataDir, configSvc, cfg)
	remoteAccessSvc := service.NewRemoteAccessService(cfg, configSvc, cfdSvc, cfAPISvc, meshSvc, auditSvc, dataDir, dockerContainersSvc)
	storageSvc := service.NewStorageService(database, cfg)
	storageFilesSvc := service.NewStorageFilesService(database, cfg)
	localProxyHandler := handler.NewLocalProxy(service.NewLocalProxy(dataDir))

	statusHandler := handler.NewStatus(cfdSvc, cfg)
	controlHandler := handler.NewControl(cfdSvc, auditSvc)
	logsHandler := handler.NewLogs(hub, cfg.JWTSecret).WithSessionValidator(func(raw string) error { _, err := authHandler.Sessions().Verify(raw, "access"); return err })
	configHandler := handler.NewConfig(configSvc, auditSvc, cfAPISvc)
	tcpHandler := handler.NewTcpAccess(cfdSvc)
	auditHandler := handler.NewAudit(auditSvc)
	healthHandler := handler.NewHealthCheck(cfg.CloudflaredConfig).WithActiveConfig(cfAPISvc, configSvc)
	metricsHandler := handler.NewMetrics(cfg, dockerContainersSvc)
	settingsHandler := handler.NewSettings(database, auditSvc)
	notifHandler := handler.NewNotifications(dataDir).WithDatabase(database)
	projectsHandler := handler.NewProjects(projectsSvc, auditSvc)
	if !cfg.RecoveryMode {
		projectsHandler = projectsHandler.WithNotifications(notifHandler.DeliverEvent)
	}
	templatesHandler := handler.NewTemplates()
	dockerRunHandler := handler.NewDockerRun(dockerRunSvc, auditSvc)
	dockerContainersHandler := handler.NewDockerContainers(dockerContainersSvc)
	meshHandler := handler.NewMesh(meshSvc)
	remoteAccessHandler := handler.NewRemoteAccess(remoteAccessSvc, auditSvc)
	cloudflareHandler := handler.NewCloudflareHandler(cfAPISvc, configSvc, auditSvc)
	storageHandler := handler.NewStorageHandler(storageSvc, storageFilesSvc, auditSvc)
	setupSvc := service.NewSetupService(cfg, database, dockerContainersSvc, cfAPISvc, configSvc, meshSvc, auditSvc, dataDir)
	setupHandler := handler.NewSetup(setupSvc, authHandler)
	operations := service.NewOperationStore(database)
	if err := operations.ReconcileInterrupted(); err != nil {
		log.Fatalf("reconcile interrupted operations: %v", err)
	}
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := operations.ReconcileInterrupted(); err != nil {
				log.Printf("operation reconciliation: %v", err)
			}
		}
	}()
	maintenance := &handler.Maintenance{Containers: dockerContainersSvc, Operations: operations, Notifications: notifHandler}
	recovery := &handler.Recovery{Service: service.NewRecovery(database, cfg, operations)}
	if err := recovery.Service.ReconcileStartup(); err != nil {
		log.Fatalf("recovery startup reconciliation: %v", err)
	}
	support := &handler.Support{Service: &service.SupportBundles{Directory: filepath.Join(dataDir, "support"), Operations: operations, Config: cfg}}

	r.Get("/healthz", handler.Readiness(database))
	r.Get("/downloads/recovery/{id}", recovery.NativeDownload)
	r.Get("/api/setup/status", setupHandler.Status)
	authLimiter := middleware.NewRateLimiter(10, time.Minute)
	refreshLimiter := middleware.NewRateLimiter(30, time.Minute)
	r.With(authLimiter.Limit).Post("/api/setup/complete", setupHandler.Complete)
	r.With(authLimiter.Limit).Post("/auth/login", authHandler.Login)
	r.With(refreshLimiter.Limit).Post("/auth/refresh", authHandler.Refresh)
	r.Get("/api/logs/ws", logsHandler.Stream)

	r.Group(func(r chi.Router) {
		r.Use(authMw.Verify)
		r.Use(handler.AuditMaintenanceMutations(auditSvc))
		r.Get("/api/recovery/settings", recovery.Settings)
		r.Put("/api/recovery/settings", recovery.Settings)
		r.Post("/api/recovery/{id}/remote-upload", recovery.UploadRemote)
		r.Post("/api/recovery/{id}/remote-fetch", recovery.FetchRemote)
		r.Delete("/api/recovery/{id}", recovery.Delete)
		r.Get("/api/recovery/status", recovery.Status)
		r.Get("/api/recovery/inventory", recovery.Inventory)
		r.Post("/api/recovery/activate", recovery.Activate)
		r.Get("/api/recovery/backups", recovery.List)
		r.Post("/api/recovery/backups", recovery.Backup)
		r.Post("/api/recovery/import", recovery.Upload)
		r.Get("/api/recovery/{id}/download", recovery.Download)
		r.Post("/api/recovery/{id}/download-ticket", recovery.Ticket)
		r.Post("/api/recovery/{id}/inspect", recovery.Inspect)
		r.Post("/api/recovery/{id}/restore", recovery.Restore)
		r.Post("/api/support-bundles", support.Create)
		r.Get("/api/support-bundles/{id}", support.Download)
		r.Get("/api/operations", maintenance.History)
		r.Post("/api/operations/{id}/acknowledge", maintenance.Acknowledge)
		r.Get("/api/docker/containers/{id}/update-plan", maintenance.Plan)
		r.Post("/api/docker/containers/{id}/update-plan", maintenance.Plan)
		r.Put("/api/docker/containers/{id}/readiness", maintenance.Readiness)
		r.Get("/api/docker/containers/{id}/import-preview", maintenance.ImportPreview)
		r.Get("/api/notifications/deliveries", maintenance.Deliveries)
		r.Post("/api/notifications/deliveries/{id}/retry", maintenance.RetryDelivery)
		r.Get("/api/templates/catalog", templatesHandler.Catalog)
		r.Post("/api/templates/import", templatesHandler.Import)
		r.Post("/api/templates/setup", templatesHandler.Setup)
		r.Post("/api/templates/ports", templatesHandler.Ports)
		r.Post("/api/templates/files", templatesHandler.Files)
		r.Post("/api/projects/review", templatesHandler.Review)
		updater := handler.NewSelfUpdate(service.NewSelfUpdater(dataDir), auditSvc).WithOperations(operations)
		servicesUpdater := handler.NewManagedServices(service.NewManagedServices(dataDir), auditSvc).WithOperations(operations)
		r.Get("/api/services", servicesUpdater.Status)
		r.Post("/api/services/check", servicesUpdater.Check)
		r.Post("/api/services/update", servicesUpdater.Update)
		r.Get("/api/self-update", updater.Status)
		r.Get("/api/self-update/channel", updater.Channel)
		r.Put("/api/self-update/channel", updater.Channel)
		r.Post("/api/self-update/check", updater.Check)
		r.Post("/api/self-update/install", updater.Install)

		r.Get("/api/setup/details", setupHandler.Details)
		r.Post("/api/setup/reconfigure", setupHandler.Reconfigure)
		r.Post("/auth/logout", authHandler.Logout)

		r.Get("/api/status", statusHandler.Get)
		r.Get("/api/local-proxy", localProxyHandler.Get)
		r.Put("/api/local-proxy", localProxyHandler.Put)
		r.Get("/api/diagnostics", handler.Diagnostics())
		r.Post("/api/control/{action}", controlHandler.Execute)
		r.Get("/api/provider/cloudflare/credentials", controlHandler.Connector)
		r.Put("/api/provider/cloudflare/credentials", controlHandler.Connector)

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
		r.Get("/api/projects/{id}/backup", projectsHandler.Backup)
		r.Post("/api/projects/restore", projectsHandler.Restore)
		r.Post("/api/projects/prune", projectsHandler.Prune)
		r.Put("/api/projects/{id}", projectsHandler.Update)
		r.Delete("/api/projects/{id}", projectsHandler.Delete)
		r.Get("/api/projects/{id}/delete-options", projectsHandler.DeleteOptions)
		r.Get("/api/projects/{id}/file-access", projectsHandler.FileAccess)
		r.Get("/api/projects/{id}/history", projectsHandler.History)
		r.Get("/api/projects/{id}/output", projectsHandler.Output)
		r.Post("/api/projects/{id}/{action}", projectsHandler.Action)
		r.Post("/api/projects/{id}/action/{action}", projectsHandler.Action)

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
		r.Get("/api/docker/containers/{id}/tailscale", dockerContainersHandler.TailscaleNode)
		r.Post("/api/docker/containers/{id}/tailscale", dockerContainersHandler.TailscaleNode)
		r.Post("/api/docker/containers/{id}/exposure", dockerContainersHandler.UpdateExposure)
		r.Post("/api/docker/containers/{id}/exposure-plan", dockerContainersHandler.ExposurePreflight)
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
		r.Post("/api/docker/containers/{id}/relink-database", dockerContainersHandler.RelinkDatabase)
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
		r.Get("/api/cloudflare/dns-check", cloudflareHandler.CheckDNS)
		r.Post("/api/cloudflare/dns-replace", cloudflareHandler.ReplaceDNS)
		r.Post("/api/cloudflare/sync-all", cloudflareHandler.SyncAllRoutes)
		r.Post("/api/cloudflare/create-wildcard", cloudflareHandler.CreateWildcard)
		r.Get("/api/cloudflare/tunnel", cloudflareHandler.GetTunnelStatus)
		r.Get("/api/cloudflare/configuration", cloudflareHandler.GetTunnelConfiguration)
		r.Put("/api/cloudflare/configuration", cloudflareHandler.UpdateTunnelConfiguration)
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
	if !cfg.RecoveryMode {
		go recovery.Service.RunSchedules(ctx)
	}
	if !cfg.RecoveryMode {
		go notifHandler.RunMonitor(ctx, cfg.CloudflaredContainer, service.SampleNotifications)
	}

	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !cfg.RecoveryMode {
					dockerContainersSvc.ReconcileTailscaleAppNetworks()
				}
			}
		}
	}()

	go func() {
		log.Printf("server listening on %s", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Reconcile network and database health on startup and periodically in background
	go func() {
		time.Sleep(2 * time.Second)
		mesh, err := meshSvc.Load()
		if err == nil && !cfg.RecoveryMode && mesh.Enabled && mesh.Mode == "public" {
			if _, err := dockerContainersSvc.ReconcileNetworks(); err != nil {
				log.Printf("[docktun] network reconciliation notice: %v", err)
			}
			time.Sleep(1 * time.Second)
			if mesh.Mode == "public" {
				cfAPISvc.AutoSyncIngressDNS(configSvc)
			}
		}

		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mesh, err := meshSvc.Load()
				if err == nil && !cfg.RecoveryMode && mesh.Enabled && mesh.Mode == "public" {
					_, _ = dockerContainersSvc.ReconcileNetworks()
				}
			}
		}
	}()

	// Periodically check for container updates in background
	go func() {
		time.Sleep(10 * time.Second)
		log.Println("[docktun] running initial container update check...")
		if _, err := dockerContainersSvc.CheckAllUpdates(); err != nil {
			log.Printf("[docktun] update check notice: %v", err)
		}

		ticker := time.NewTicker(4 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				log.Println("[docktun] running periodic container update check...")
				_, _ = dockerContainersSvc.CheckAllUpdates()
			}
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
}
