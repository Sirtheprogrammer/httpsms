package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/httpsms/gateway-server/internal/api/handler"
	"github.com/httpsms/gateway-server/internal/api/middleware"
	"github.com/httpsms/gateway-server/internal/api/ws"
	"github.com/httpsms/gateway-server/internal/domain"
	"github.com/httpsms/gateway-server/internal/storage"
	"github.com/httpsms/gateway-server/internal/webhook"
)

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func main() {
	port := getEnv("PORT", "8080")
	dbPath := getEnv("DB_PATH", "./data/httpsms.db")
	masterSecret := getEnv("MASTER_SECRET", "htsms_admin_secret_change_me")
	otaDir := getEnv("OTA_DIR", "./data/ota")
	webDir := getEnv("WEB_DIR", "../web")

	// Verify or resolve web directory
	if _, err := os.Stat(webDir); os.IsNotExist(err) {
		// Try fallback relative path (e.g. inside docker /app/web)
		if _, err := os.Stat("./web"); err == nil {
			webDir = "./web"
		}
	}

	log.Printf("[HttpSMS] Booting Gateway Server on :%s", port)
	log.Printf("[HttpSMS] Database path: %s", dbPath)
	log.Printf("[HttpSMS] Web UI directory: %s", webDir)

	// Initialize Storage
	store, err := storage.NewDB(dbPath)
	if err != nil {
		log.Fatalf("Fatal: Database initialization failed: %v", err)
	}
	defer store.Close()
	if err := store.MarkAllDevicesOffline(); err != nil {
		log.Printf("[HttpSMS] Warning: could not reset device status: %v", err)
	}

	// Initialize Webhook Dispatcher
	dispatcher := webhook.NewDispatcher(store)
	dispatcher.Start(5)
	defer dispatcher.Stop()

	// Initialize WebSocket Hub
	hub := ws.NewHub(store, dispatcher)
	go hub.Run()

	// Initialize Auth Middleware & API Handlers
	authMw := middleware.NewAuthMiddleware(store, masterSecret)
	api := handler.NewAPIHandler(store, hub, dispatcher, otaDir)

	// Main Router
	r := chi.NewRouter()

	// Global Middleware
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Logger)
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.Timeout(60 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token", "X-Device-Token"},
		ExposedHeaders:   []string{"Link", "X-Signature-SHA256", "X-Checksum-SHA256"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// WebSocket Endpoints
	r.Get("/ws/device", hub.HandleDeviceWs)
	r.Get("/ws/dashboard", hub.HandleDashboardWs)

	// Public Health Check
	r.Get("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		handler.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"status":    "operational",
			"timestamp": time.Now().UTC(),
		})
	})

	// Public OTA Endpoints
	r.Get("/api/v1/ota/check", api.CheckOtaUpdate)
	r.Get("/api/v1/ota/download/{id}", api.DownloadOtaRelease)

	// Protected API Routes
	r.Route("/api/v1", func(apiRoute chi.Router) {
		// SMS sending & reading
		apiRoute.With(authMw.RequireScope(domain.ScopeSMSSend)).Post("/sms/send", api.SendSMS)
		apiRoute.With(authMw.RequireScope(domain.ScopeSMSRead)).Get("/messages", api.ListMessages)
		apiRoute.With(authMw.RequireScope(domain.ScopeSMSRead)).Get("/messages/{id}", api.GetMessage)

		// Device Management & Pairing
		apiRoute.With(authMw.RequireScope(domain.ScopeDevicesManage)).Get("/devices", api.ListDevices)
		apiRoute.With(authMw.RequireScope(domain.ScopeDevicesManage)).Post("/devices/pair", api.PairDevice)
		apiRoute.With(authMw.RequireScope(domain.ScopeDevicesManage)).Delete("/devices/{id}", api.DeleteDevice)

		// Webhook Management
		apiRoute.With(authMw.RequireScope(domain.ScopeWebhooksManage)).Get("/webhooks", api.ListWebhooks)
		apiRoute.With(authMw.RequireScope(domain.ScopeWebhooksManage)).Post("/webhooks", api.CreateWebhook)
		apiRoute.With(authMw.RequireScope(domain.ScopeWebhooksManage)).Delete("/webhooks/{id}", api.DeleteWebhook)
		apiRoute.With(authMw.RequireScope(domain.ScopeWebhooksManage)).Post("/webhooks/{id}/test", api.TestWebhook)
		apiRoute.With(authMw.RequireScope(domain.ScopeWebhooksManage)).Get("/webhooks/deliveries", api.ListWebhookDeliveries)

		// API Token Management (requires master scope)
		apiRoute.With(authMw.RequireScope("*")).Get("/tokens", api.ListTokens)
		apiRoute.With(authMw.RequireScope("*")).Post("/tokens", api.CreateToken)
		apiRoute.With(authMw.RequireScope("*")).Delete("/tokens/{id}", api.DeleteToken)

		// OTA Management (upload new APK / UI version)
		apiRoute.With(authMw.RequireScope(domain.ScopeOTAManage)).Post("/ota/upload", api.UploadOtaRelease)

		// Telemetry & Stats
		apiRoute.With(authMw.RequireScope(domain.ScopeSMSRead)).Get("/system/stats", api.GetSystemStats)
	})

	// Serve Static UI Assets & SPA fallback
	workDir, _ := filepath.Abs(webDir)
	fileServer := http.FileServer(http.Dir(workDir))

	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		// Check if requested static file exists
		targetFile := filepath.Join(workDir, filepath.Clean(r.URL.Path))
		info, err := os.Stat(targetFile)
		if err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}

		// Fallback to index.html for SPA client-side routing
		indexPath := filepath.Join(workDir, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			http.ServeFile(w, r, indexPath)
			return
		}

		http.NotFound(w, r)
	})

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown handling
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

