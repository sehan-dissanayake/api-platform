package adminserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	adminapi "github.com/wso2/api-platform/gateway/gateway-controller/pkg/adminapi/generated"
	"github.com/wso2/api-platform/gateway/gateway-controller/pkg/config"
)

type apiServer interface {
	BuildConfigDumpResponse(log *slog.Logger) (*adminapi.ConfigDumpResponse, error)
	GetXDSSyncStatusResponse() adminapi.XDSSyncStatusResponse
}

// Server is the controller admin HTTP server for debug endpoints.
type Server struct {
	cfg       *config.AdminServerConfig
	apiServer apiServer
	httpSrv   *http.Server
	logger    *slog.Logger
}

// NewServer creates a new admin HTTP server.
func NewServer(cfg *config.AdminServerConfig, apiServer apiServer, logger *slog.Logger) *Server {
	mux := http.NewServeMux()

	s := &Server{
		cfg:       cfg,
		apiServer: apiServer,
		logger:    logger,
	}

	mux.Handle("/config_dump", ipWhitelistMiddleware(cfg.AllowedIPs, http.HandlerFunc(s.handleConfigDump)))
	mux.Handle("/xds_sync_status", ipWhitelistMiddleware(cfg.AllowedIPs, http.HandlerFunc(s.handleXDSSyncStatus)))
	// Health endpoint is registered without IP whitelist so Docker/k8s health probes can reach it
	mux.Handle("/health", http.HandlerFunc(s.handleHealth))

	s.httpSrv = &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: mux,
	}

	return s
}

// Start starts the admin HTTP server in a blocking manner.
func (s *Server) Start() error {
	s.logger.Info("Starting controller admin HTTP server",
		slog.Int("port", s.cfg.Port),
		slog.Any("allowed_ips", s.cfg.AllowedIPs))
	if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("admin server error: %w", err)
	}
	return nil
}

// Stop gracefully stops the admin HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("Stopping controller admin HTTP server")
	return s.httpSrv.Shutdown(ctx)
}

func (s *Server) handleConfigDump(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp, err := s.apiServer.BuildConfigDumpResponse(s.logger)
	if err != nil {
		http.Error(w, "Failed to retrieve configuration dump", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleXDSSyncStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := s.apiServer.GetXDSSyncStatusResponse()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := map[string]string{
		"status":    "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func ipWhitelistMiddleware(allowedIPs []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientIP := extractClientIP(r)
		if !isIPAllowed(clientIP, allowedIPs) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func extractClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isIPAllowed(clientIP string, allowedIPs []string) bool {
	for _, allowedIP := range allowedIPs {
		if allowedIP == "*" || allowedIP == "0.0.0.0/0" {
			return true
		}
		if clientIP == allowedIP {
			return true
		}
	}
	return false
}
