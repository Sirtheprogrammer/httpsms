package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/httpsms/gateway-server/internal/api/handler"
	"github.com/httpsms/gateway-server/internal/api/middleware"
	"github.com/httpsms/gateway-server/internal/api/ws"
	"github.com/httpsms/gateway-server/internal/domain"
	"github.com/httpsms/gateway-server/internal/secrets"
	"github.com/httpsms/gateway-server/internal/storage"
	"github.com/httpsms/gateway-server/internal/webhook"
	"github.com/httpsms/gateway-server/internal/workflow"
)

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

// resolveWebDir finds the built dashboard (dashboard/dist), falling back to common layouts.
func resolveWebDir(configured string) string {
	candidates := []string{configured, "../dashboard/dist", "./dashboard/dist", "./web"}
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "index.html")); err == nil {
			return dir
		}
	}
	return configured
}

func main() {
	port := getEnv("PORT", "8080")
	dbPath := getEnv("DB_PATH", "./data/httpsms.db")
	masterSecret := os.Getenv("MASTER_SECRET")
	otaDir := getEnv("OTA_DIR", "./data/ota")
	webDir := resolveWebDir(os.Getenv("WEB_DIR"))

	log.Printf("[HttpSMS] Booting Gateway Server on :%s", port)
	log.Printf("[HttpSMS] Database path: %s", dbPath)
	log.Printf("[HttpSMS] Web UI directory: %s", webDir)
	if masterSecret == "htsms_admin_secret_change_me" {
		log.Printf("[HttpSMS] WARNING: MASTER_SECRET is the public default; anyone can use it as admin. Change or unset it.")
	}

	store, err := storage.NewDB(dbPath)
	if err != nil {
		log.Fatalf("Fatal: Database initialization failed: %v", err)
	}
	defer store.Close()
	if err := store.MarkAllDevicesOffline(); err != nil {
		log.Printf("[HttpSMS] Warning: could not reset device status: %v", err)
	}

	// Encrypts users' AI provider keys. Keep SECRET_KEY (or data/secret.key) safe and stable.
	box, err := secrets.Load(os.Getenv("SECRET_KEY"), filepath.Join(filepath.Dir(dbPath), "secret.key"))
	if err != nil {
		log.Fatalf("Fatal: could not load encryption key: %v", err)
	}

	dispatcher := webhook.NewDispatcher(store)
	dispatcher.Start(5)
	defer dispatcher.Stop()

	authMw := middleware.NewAuthMiddleware(store, masterSecret)

	hub := ws.NewHub(store, dispatcher)
	engine := workflow.NewEngine(store, box)
	api := handler.NewAPIHandler(store, hub, dispatcher, engine, box, otaDir)
	engine.Wire(api, hub)
	hub.SetAuthenticator(authMw.Resolve)
	hub.SetInboundHook(engine.HandleInbound)
	go hub.Run()

	go func() {
		for range time.Tick(6 * time.Hour) {
			_ = store.PruneExpiredSessions()
		}
	}()

	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Logger)
	r.Use(chimiddleware.Recoverer)
	// API clients may call from anywhere with a Bearer token, but browsers never get
	// credentialed cross-origin access, which (with the X-Requested-With check) blocks CSRF.
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Device-Token"},
		ExposedHeaders:   []string{"X-Signature-SHA256", "X-Checksum-SHA256"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	// WebSockets (no request timeout)
	r.Get("/ws/device", hub.HandleDeviceWs)
	r.Get("/ws/dashboard", hub.HandleDashboardWs)

	r.Route("/api/v1", func(apiRoute chi.Router) {
		apiRoute.Use(chimiddleware.Timeout(4 * time.Minute))

		// Public
		apiRoute.Get("/health", func(w http.ResponseWriter, r *http.Request) {
			handler.WriteJSON(w, http.StatusOK, map[string]interface{}{"status": "operational", "timestamp": time.Now().UTC()})
		})
		apiRoute.Get("/ota/check", api.CheckOtaUpdate)
		apiRoute.Get("/ota/download/{id}", api.DownloadOtaRelease)
		apiRoute.Get("/auth/status", api.AuthStatus)
		limiter := middleware.NewRateLimiter(10, time.Minute)
		apiRoute.With(limiter.Middleware).Post("/auth/register", api.Register)
		apiRoute.With(limiter.Middleware).Post("/auth/login", api.Login)
		apiRoute.Post("/auth/logout", api.Logout)

		// Signed-in users and API tokens
		apiRoute.Group(func(p chi.Router) {
			p.Use(authMw.Authenticate)
			scope := authMw.RequireScope

			p.Get("/auth/me", api.Me)
			p.Patch("/auth/me", api.UpdateMe)
			p.Post("/auth/password", api.ChangePassword)

			p.With(scope(domain.ScopeSMSSend)).Post("/sms/send", api.SendSMS)
			p.With(scope(domain.ScopeSMSRead)).Get("/messages", api.ListMessages)
			p.With(scope(domain.ScopeSMSRead)).Get("/messages/{id}", api.GetMessage)
			p.With(scope(domain.ScopeSMSRead)).Get("/system/stats", api.GetSystemStats)

			p.With(scope(domain.ScopeDevicesManage)).Get("/devices", api.ListDevices)
			p.With(scope(domain.ScopeDevicesManage)).Post("/devices/pair", api.PairDevice)
			p.With(scope(domain.ScopeDevicesManage)).Patch("/devices/{id}", api.UpdateDevice)
			p.With(scope(domain.ScopeDevicesManage)).Delete("/devices/{id}", api.DeleteDevice)

			p.With(scope(domain.ScopeWebhooksManage)).Get("/webhooks", api.ListWebhooks)
			p.With(scope(domain.ScopeWebhooksManage)).Post("/webhooks", api.CreateWebhook)
			p.With(scope(domain.ScopeWebhooksManage)).Get("/webhooks/deliveries", api.ListWebhookDeliveries)
			p.With(scope(domain.ScopeWebhooksManage)).Delete("/webhooks/{id}", api.DeleteWebhook)
			p.With(scope(domain.ScopeWebhooksManage)).Post("/webhooks/{id}/test", api.TestWebhook)

			p.With(scope("*")).Get("/tokens", api.ListTokens)
			p.With(scope("*")).Post("/tokens", api.CreateToken)
			p.With(scope("*")).Delete("/tokens/{id}", api.DeleteToken)

			p.With(scope(domain.ScopeWorkflows)).Get("/ai/providers", api.ListAIProviders)
			p.With(scope(domain.ScopeWorkflows)).Get("/ai/credentials", api.ListAICredentials)
			p.With(scope(domain.ScopeWorkflows)).Post("/ai/credentials", api.CreateAICredential)
			p.With(scope(domain.ScopeWorkflows)).Get("/ai/credentials/{id}/models", api.ListCredentialModels)
			p.With(scope(domain.ScopeWorkflows)).Delete("/ai/credentials/{id}", api.DeleteAICredential)

			p.With(scope(domain.ScopeWorkflows)).Get("/workflows", api.ListWorkflows)
			p.With(scope(domain.ScopeWorkflows)).Post("/workflows", api.CreateWorkflow)
			p.With(scope(domain.ScopeWorkflows)).Get("/workflow-runs", api.ListWorkflowRuns)
			p.With(scope(domain.ScopeWorkflows)).Get("/workflows/{id}", api.GetWorkflow)
			p.With(scope(domain.ScopeWorkflows)).Put("/workflows/{id}", api.UpdateWorkflow)
			p.With(scope(domain.ScopeWorkflows)).Delete("/workflows/{id}", api.DeleteWorkflow)
			p.With(scope(domain.ScopeWorkflows)).Post("/workflows/{id}/test", api.TestWorkflow)
			p.With(scope(domain.ScopeWorkflows)).Get("/workflows/{id}/runs", api.ListWorkflowRuns)

			// Administrator only
			p.Group(func(admin chi.Router) {
				admin.Use(authMw.RequireAdmin)
				admin.Get("/admin/users", api.AdminListUsers)
				admin.Patch("/admin/users/{id}", api.AdminUpdateUser)
				admin.Delete("/admin/users/{id}", api.AdminDeleteUser)
				admin.Get("/ota/releases", api.ListOtaReleases)
				admin.Post("/ota/upload", api.UploadOtaRelease)
			})
		})

		apiRoute.NotFound(func(w http.ResponseWriter, r *http.Request) {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "Unknown API endpoint")
		})
	})

	// Static dashboard with SPA fallback
	workDir, _ := filepath.Abs(webDir)
	fileServer := http.FileServer(http.Dir(workDir))
	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		targetFile := filepath.Join(workDir, filepath.Clean("/"+r.URL.Path))
		if info, err := os.Stat(targetFile); err == nil && !info.IsDir() {
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		indexPath := filepath.Join(workDir, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, indexPath)
			return
		}
		http.Error(w, "Dashboard not built. Run: cd dashboard && npm install && npm run build", http.StatusNotFound)
	})

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           r,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("[HttpSMS] Ready to accept HTTP & WebSocket connections on :%s", port)
		serverErrors <- server.ListenAndServe()
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		log.Fatalf("Server error: %v", err)
	case sig := <-shutdown:
		log.Printf("[HttpSMS] Shutdown signal %v received, draining connections...", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("[HttpSMS] Graceful shutdown failed: %v", err)
			_ = server.Close()
		}
	}
	fmt.Println("[HttpSMS] Server terminated cleanly.")
}
