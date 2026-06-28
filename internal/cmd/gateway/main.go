package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sanke08/api_gateway/internal/cache"
	"github.com/sanke08/api_gateway/internal/config"
	"github.com/sanke08/api_gateway/internal/handlers"
	"github.com/sanke08/api_gateway/internal/middleware"
	"github.com/sanke08/api_gateway/internal/models"
	"github.com/sanke08/api_gateway/internal/observability"
	"github.com/sanke08/api_gateway/internal/pkg/cacheclient"
	"github.com/sanke08/api_gateway/internal/proxy"
	"github.com/sanke08/api_gateway/internal/ratelimit"
	"github.com/sanke08/api_gateway/internal/repository"
	"github.com/sanke08/api_gateway/internal/server"
	"github.com/sanke08/api_gateway/internal/services"
)

func main() {
	// -------------------------------------------------------------------------
	// 1. Logger
	// -------------------------------------------------------------------------
	observability.InitLogger()

	// -------------------------------------------------------------------------
	// 2. Config
	// -------------------------------------------------------------------------
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// -------------------------------------------------------------------------
	// 3. Database
	// -------------------------------------------------------------------------
	db, err := config.NewDatabase(cfg)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	observability.Info("database connected")

	// -------------------------------------------------------------------------
	// 4. Repositories
	// -------------------------------------------------------------------------
	repos := repository.NewPostgresRepositories(db)

	// -------------------------------------------------------------------------
	// 5. JWT manager
	// -------------------------------------------------------------------------
	jwt, err := services.NewJWTManager(
		cfg.JWT.Secret,
		cfg.JWT.Issuer,
		cfg.JWT.AccessTTL,
		cfg.JWT.RefreshTTL,
	)
	if err != nil {
		log.Fatalf("jwt: %v", err)
	}

	// -------------------------------------------------------------------------
	// 6. Services
	// -------------------------------------------------------------------------
	onboardingSvc := services.NewOnboardingService(db, repos)
	authSvc := services.NewAuthService(repos, jwt)
	apiKeyAuthSvc := services.NewAPIKeyAuthService(repos)
	tenantResolver := services.NewTenantResolver(repos, jwt)

	// -------------------------------------------------------------------------
	// 7. Metrics registry
	// -------------------------------------------------------------------------
	reg := observability.NewRegistry()

	// -------------------------------------------------------------------------
	// 8. Cache — HybridStore (remote + local) or MemoryStore (local only).
	// -------------------------------------------------------------------------
	memStore := cache.NewMemoryStore()
	var cacheStore cache.Store = memStore

	if cfg.Cache.RemoteURL != "" {
		remoteClient, err := cacheclient.NewRemoteClient(
			cfg.Cache.RemoteURL,
			cfg.Cache.Timeout,
			cfg.Cache.Namespace,
			cfg.Cache.Token,
		)
		if err != nil {
			log.Fatalf("cache remote client: %v", err)
		}
		cacheStore = cache.NewHybridStore(remoteClient, memStore)
		observability.Info("cache: remote+local hybrid", "url", cfg.Cache.RemoteURL)
	} else {
		observability.Info("cache: local memory only")
	}

	// -------------------------------------------------------------------------
	// 9. Upstreams → StaticRegistry
	// -------------------------------------------------------------------------
	targets := make([]models.UpstreamTarget, 0, len(cfg.Upstreams))
	for _, u := range cfg.Upstreams {
		targets = append(targets, models.UpstreamTarget{
			TenantID:    u.TenantID,
			Name:        u.Name,
			BaseURL:     u.BaseURL,
			StripPrefix: u.StripPrefix,
			AddPrefix:   u.AddPrefix,
			Timeout:     30 * time.Second,
		})
	}

	// -------------------------------------------------------------------------
	// 10. Async usage tracker
	// -------------------------------------------------------------------------
	usageTracker := services.NewAsyncUsageTracker(repos.Usage, 1024, nil, reg)

	// -------------------------------------------------------------------------
	// 11. Rate limiter
	// -------------------------------------------------------------------------
	limiter := ratelimit.NewLimiter()
	rateLimitMw, err := ratelimit.NewMiddleware(limiter, cfg.RateLimit, ratelimit.KeyTenant)
	if err != nil {
		log.Fatalf("rate limit middleware: %v", err)
	}

	// -------------------------------------------------------------------------
	// 12. Proxy + data-plane (only when upstreams are configured)
	// -------------------------------------------------------------------------
	var proxyHandler http.Handler
	var healthRegistry proxy.Registry

	if len(targets) > 0 {
		reg, err := proxy.NewStaticRegistry(targets)
		if err != nil {
			log.Fatalf("proxy registry: %v", err)
		}
		healthRegistry = reg

		ph, err := proxy.NewHandler(reg, nil)
		if err != nil {
			log.Fatalf("proxy handler: %v", err)
		}

		usageMw := middleware.NewUsageMiddleware(usageTracker)
		inner := rateLimitMw(usageMw(ph))

		apiKeyChain := middleware.NewAPIKeyAuthMiddleware(inner, apiKeyAuthSvc)
		tenantChain := middleware.NewTenantResolutionMiddleware(inner, tenantResolver)
		proxyHandler = authDispatch(apiKeyChain, tenantChain)

		observability.Info("proxy configured", "upstreams", len(targets))
	} else {
		observability.Info("no upstreams configured — proxy routes disabled")
	}

	// -------------------------------------------------------------------------
	// 13. Health checker
	// -------------------------------------------------------------------------
	healthChecker := services.NewHealthChecker(db, healthRegistry)

	// -------------------------------------------------------------------------
	// 14. HTTP handlers
	// -------------------------------------------------------------------------
	onboardingHandler := handlers.NewOnboardingHandler(onboardingSvc)
	authHandler := handlers.NewAuthHandler(authSvc)
	healthHandler := handlers.NewHealthHandler(healthChecker)
	readyHandler := handlers.NewReadyHandler(healthChecker)

	// -------------------------------------------------------------------------
	// 15. Edge middleware
	// -------------------------------------------------------------------------
	edgeMw, err := middleware.NewEdgeMiddleware(middleware.EdgePolicy{
		CORS:     cfg.CORS,
		Security: cfg.Security,
	})
	if err != nil {
		log.Fatalf("edge middleware: %v", err)
	}

	// -------------------------------------------------------------------------
	// 16. Response cache middleware
	// -------------------------------------------------------------------------
	responseCacheMw, err := middleware.NewResponseCacheMiddleware(cacheStore, middleware.ResponseCachePolicy{
		TTL:               5 * time.Minute,
		MaxBodyBytes:      1 << 20,
		CacheableStatuses: []int{http.StatusOK},
	})
	if err != nil {
		log.Fatalf("response cache middleware: %v", err)
	}

	// -------------------------------------------------------------------------
	// 17. Router
	// -------------------------------------------------------------------------
	router := server.NewRouter()

	router.Use(edgeMw)
	router.Use(observability.Middleware(reg))
	router.Use(middleware.LoggingMiddleware)

	mustRoute(router.POST("/onboard", onboardingHandler))
	mustRoute(router.POST("/login", authHandler))
	mustRoute(router.GET("/metrics", reg.MetricsHandler()))
	mustRoute(router.GET("/health", healthHandler))
	mustRoute(router.GET("/ready", readyHandler))

	if proxyHandler != nil {
		cachedProxy := responseCacheMw(proxyHandler)
		mustRoute(router.GET("/{path...}", cachedProxy))
		mustRoute(router.POST("/{path...}", cachedProxy))
		mustRoute(router.PUT("/{path...}", cachedProxy))
		mustRoute(router.PATCH("/{path...}", cachedProxy))
		mustRoute(router.DELETE("/{path...}", cachedProxy))
	}

	// -------------------------------------------------------------------------
	// 18. Shutdown manager
	//
	// Created before the http.Server so we can Wrap the router — Wrap adds
	// request tracking (WaitGroup) and returns 503 once shutdown begins.
	//
	// Registration order of hooks = reverse-shutdown order:
	//   usage tracker drains first (requests may still be in-flight writing usage)
	//   then cache pruner stops
	//   then limiter pruner stops
	//
	// http.Server.Shutdown is called by GracefulServer before the manager hooks,
	// so by the time hooks run, no new requests can arrive.
	// -------------------------------------------------------------------------
	shutdownMgr := server.NewShutdownManager()

	// Hook 1 — usage tracker: drain the in-memory queue and flush to DB.
	shutdownMgr.RegisterHook("usage-tracker", usageTracker.Close)

	// Hook 2 — cache pruner: stop the background ticker goroutine.
	//   We start it here (not earlier) so the cancel func is in scope for the hook.
	cacheCtx, cacheCancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				pruned := cacheStore.PruneExpired()
				if pruned > 0 {
					observability.Info("cache pruned", "entries", pruned)
				}
			case <-cacheCtx.Done():
				return
			}
		}
	}()
	shutdownMgr.RegisterHook("cache-pruner", func(_ context.Context) error {
		cacheCancel()
		return nil
	})

	// Hook 3 — rate limiter pruner: evict stale buckets to reclaim memory.
	limiterCtx, limiterCancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				pruned := limiter.PruneIdle(15 * time.Minute)
				if pruned > 0 {
					observability.Info("rate limiter pruned idle buckets", "count", pruned)
				}
			case <-limiterCtx.Done():
				return
			}
		}
	}()
	shutdownMgr.RegisterHook("limiter-pruner", func(_ context.Context) error {
		limiterCancel()
		return nil
	})

	// -------------------------------------------------------------------------
	// 19. Build the http.Server with Wrap applied to the router.
	//
	// Wrap(router) returns a handler that:
	//   - tracks each request in a WaitGroup (Add on entry, Done on exit)
	//   - returns 503 immediately once shutdown has started
	//
	// This is the last middleware layer — it sits outside everything else.
	// -------------------------------------------------------------------------
	httpServer := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: shutdownMgr.Wrap(router),
	}

	graceful := &server.GracefulServer{
		Server:  httpServer,
		Manager: shutdownMgr,
	}

	// -------------------------------------------------------------------------
	// 20. Signal handling — listen for SIGINT (Ctrl-C) or SIGTERM (docker stop /
	//     kubectl rollout).
	//
	// Pattern: start the server in a goroutine, block on the signal channel,
	// then call Shutdown with a timeout. This is the standard Go pattern for
	// graceful shutdown and gives in-flight requests up to 30 s to complete.
	// -------------------------------------------------------------------------
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	serverErr := make(chan error, 1)
	go func() {
		observability.Info("gateway starting", "port", cfg.Port)
		log.Printf("gateway listening on :%s", cfg.Port)
		if err := graceful.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// Block until a signal arrives or the server dies unexpectedly.
	select {
	case sig := <-quit:
		log.Printf("signal received: %s — shutting down", sig)
	case err := <-serverErr:
		log.Fatalf("server error: %v", err)
	}

	// Give everything 30 s to finish cleanly.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	observability.Info("shutting down gracefully", "timeout", "30s")

	if err := graceful.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	} else {
		observability.Info("shutdown complete")
	}
}

// authDispatch routes to the correct auth chain based on which credential
// header is present. APIKeyAuth and TenantResolution cannot be stacked —
// APIKeyAuth returns 401 immediately when X-API-Key is missing.
func authDispatch(apiKeyChain, tenantChain http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.Header.Get("X-API-Key")) != "" {
			apiKeyChain.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer ") {
			tenantChain.ServeHTTP(w, r)
			return
		}
		http.Error(w,
			`{"error":"unauthorized","message":"provide X-API-Key or Authorization: Bearer <token>"}`,
			http.StatusUnauthorized,
		)
	})
}

// mustRoute fatals on route registration errors.
// These only happen from programmer mistakes (duplicate routes, bad patterns).
func mustRoute(err error) {
	if err != nil {
		log.Fatalf("route registration: %v", err)
	}
}
