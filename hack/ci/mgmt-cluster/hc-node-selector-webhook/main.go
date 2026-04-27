package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const maxRequestBodySize = 1 << 20 // 1 MB

var defaultNodeSelector = map[string]string{
	"hypershift.openshift.io/control-plane": "true",
}

type hostedClusterPartial struct {
	Spec *hostedClusterSpec `json:"spec,omitempty"`
}

type hostedClusterSpec struct {
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
}

type webhookServer struct {
	logger           *slog.Logger
	expectedGroup    string
	expectedResource string
	patchNodeSelector []byte
	patchWithSpec     []byte
	mutated           atomic.Int64
	skipped           atomic.Int64
	denied            atomic.Int64
	errors            atomic.Int64
	startTime         time.Time
}

func newWebhookServer(logger *slog.Logger, group, resource string) *webhookServer {
	patchNodeSelector, _ := json.Marshal([]map[string]interface{}{
		{
			"op":    "add",
			"path":  "/spec/nodeSelector",
			"value": defaultNodeSelector,
		},
	})
	patchWithSpec, _ := json.Marshal([]map[string]interface{}{
		{
			"op":   "add",
			"path": "/spec",
			"value": map[string]interface{}{
				"nodeSelector": defaultNodeSelector,
			},
		},
	})
	return &webhookServer{
		logger:            logger,
		expectedGroup:     group,
		expectedResource:  resource,
		patchNodeSelector: patchNodeSelector,
		patchWithSpec:     patchWithSpec,
		startTime:         time.Now(),
	}
}

func (s *webhookServer) handleMutate(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	if r.Method != http.MethodPost {
		s.logger.Warn("rejected request with wrong method", "method", r.Method, "remote_addr", r.RemoteAddr)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		s.errors.Add(1)
		return
	}

	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		s.logger.Warn("rejected request with wrong content type", "content_type", ct, "remote_addr", r.RemoteAddr)
		http.Error(w, "unsupported content type, expected application/json", http.StatusUnsupportedMediaType)
		s.errors.Add(1)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodySize+1))
	if err != nil {
		s.logger.Error("failed to read request body", "error", err, "remote_addr", r.RemoteAddr)
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		s.errors.Add(1)
		return
	}
	if int64(len(body)) > maxRequestBodySize {
		s.logger.Warn("request body too large", "size", len(body), "remote_addr", r.RemoteAddr)
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		s.errors.Add(1)
		return
	}

	var review admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &review); err != nil {
		s.logger.Error("failed to unmarshal admission review", "error", err, "body_size", len(body))
		http.Error(w, "failed to unmarshal admission review", http.StatusBadRequest)
		s.errors.Add(1)
		return
	}

	if review.Request == nil {
		s.logger.Error("admission review has nil request", "body_size", len(body))
		http.Error(w, "admission review has nil request", http.StatusBadRequest)
		s.errors.Add(1)
		return
	}

	reqLog := s.logger.With(
		"uid", review.Request.UID,
		"name", review.Request.Name,
		"namespace", review.Request.Namespace,
		"operation", review.Request.Operation,
		"user", review.Request.UserInfo.Username,
	)

	if review.Request.DryRun != nil && *review.Request.DryRun {
		reqLog = reqLog.With("dry_run", true)
	}

	reqLog.Debug("received admission request", "body_size", len(body))

	review.Response = &admissionv1.AdmissionResponse{
		UID:     review.Request.UID,
		Allowed: true,
	}

	if review.Request.Resource.Group != s.expectedGroup ||
		review.Request.Resource.Resource != s.expectedResource ||
		review.Request.SubResource != "" {
		reqLog.Warn("unexpected resource, allowing without mutation",
			"group", review.Request.Resource.Group,
			"resource", review.Request.Resource.Resource,
			"sub_resource", review.Request.SubResource,
		)
		s.skipped.Add(1)
		resp, err := json.Marshal(review)
		if err != nil {
			reqLog.Error("failed to marshal response", "error", err)
			http.Error(w, "failed to marshal response", http.StatusInternalServerError)
			s.errors.Add(1)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(resp)
		return
	}

	if review.Request.Operation == admissionv1.Create {
		var hc hostedClusterPartial
		if err := json.Unmarshal(review.Request.Object.Raw, &hc); err != nil {
			reqLog.Error("failed to unmarshal HostedCluster object", "error", err)
			review.Response = &admissionv1.AdmissionResponse{
				UID:     review.Request.UID,
				Allowed: false,
				Result:  &metav1.Status{Message: "failed to unmarshal HostedCluster"},
			}
			s.denied.Add(1)
		} else if hc.Spec != nil && len(hc.Spec.NodeSelector) > 0 {
			s.skipped.Add(1)
			reqLog.Info("nodeSelector already set",
				"action", "skipped",
				"existing_node_selector", hc.Spec.NodeSelector,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		} else {
			patch := s.patchNodeSelector
			if hc.Spec == nil {
				patch = s.patchWithSpec
				reqLog.Debug("spec is nil, using full-spec patch")
			}
			pt := admissionv1.PatchTypeJSONPatch
			review.Response.Patch = patch
			review.Response.PatchType = &pt
			s.mutated.Add(1)
			reqLog.Info("injected default nodeSelector",
				"action", "mutated",
				"spec_was_nil", hc.Spec == nil,
				"node_selector", defaultNodeSelector,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		}
	} else {
		s.skipped.Add(1)
		reqLog.Debug("non-CREATE operation, passing through",
			"action", "skipped",
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}

	resp, err := json.Marshal(review)
	if err != nil {
		reqLog.Error("failed to marshal response", "error", err)
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		s.errors.Add(1)
		return
	}

	reqLog.Debug("sending response", "response_size", len(resp))

	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

func (s *webhookServer) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (s *webhookServer) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (s *webhookServer) handleStats(w http.ResponseWriter, _ *http.Request) {
	mutated := s.mutated.Load()
	skipped := s.skipped.Load()
	denied := s.denied.Load()
	errs := s.errors.Load()

	stats := map[string]interface{}{
		"uptime_seconds": int(time.Since(s.startTime).Seconds()),
		"mutated":        mutated,
		"skipped":        skipped,
		"denied":         denied,
		"errors":         errs,
		"total":          mutated + skipped + denied + errs,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func main() {
	group := flag.String("group", "", "API group to validate (required, e.g. hypershift.openshift.io)")
	resource := flag.String("resource", "", "API resource to validate (required, e.g. hostedclusters)")
	flag.Parse()

	if *group == "" || *resource == "" {
		fmt.Fprintln(os.Stderr, "both -group and -resource flags are required")
		flag.Usage()
		os.Exit(1)
	}

	level := parseLogLevel(os.Getenv("LOG_LEVEL"))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	certPath := "/etc/webhook/certs/tls.crt"
	keyPath := "/etc/webhook/certs/tls.key"

	srv := newWebhookServer(logger, *group, *resource)

	mux := http.NewServeMux()
	mux.HandleFunc("/mutate-hostedcluster", srv.handleMutate)
	mux.HandleFunc("/healthz", srv.handleHealthz)
	mux.HandleFunc("/readyz", srv.handleReadyz)
	mux.HandleFunc("/stats", srv.handleStats)

	httpServer := &http.Server{
		Addr:    ":8443",
		Handler: mux,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				cert, err := tls.LoadX509KeyPair(certPath, keyPath)
				if err != nil {
					logger.Error("failed to load TLS certificate", "error", err)
					return nil, err
				}
				return &cert, nil
			},
		},
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		logger.Info("starting webhook server",
			"addr", httpServer.Addr,
			"log_level", level.String(),
			"cert_path", certPath,
			"key_path", keyPath,
			"default_node_selector", defaultNodeSelector,
		)
		if err := httpServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-stop
	logger.Info("shutting down",
		"uptime_seconds", int(time.Since(srv.startTime).Seconds()),
		"mutated", srv.mutated.Load(),
		"skipped", srv.skipped.Load(),
		"denied", srv.denied.Load(),
		"errors", srv.errors.Load(),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("shutdown error", "error", err)
	}

	logger.Info("server stopped")
}
