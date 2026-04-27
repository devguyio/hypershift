package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

const (
	patchNodeSelector = `[{"op":"add","path":"/spec/nodeSelector","value":{"hypershift.openshift.io/control-plane":"true"}}]`
	patchWithSpec     = `[{"op":"add","path":"/spec","value":{"nodeSelector":{"hypershift.openshift.io/control-plane":"true"}}}]`
)

func testServer() *webhookServer {
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return newWebhookServer(logger, "hypershift.openshift.io", "hostedclusters")
}

func makeReview(op admissionv1.Operation, raw string) []byte {
	review := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{APIVersion: "admission.k8s.io/v1", Kind: "AdmissionReview"},
		Request: &admissionv1.AdmissionRequest{
			UID:       types.UID("test-uid"),
			Operation: op,
			Resource:  metav1.GroupVersionResource{Group: "hypershift.openshift.io", Version: "v1beta1", Resource: "hostedclusters"},
			Object:    runtime.RawExtension{Raw: []byte(raw)},
			Name:      "test-hc",
			Namespace: "clusters",
		},
	}
	data, _ := json.Marshal(review)
	return data
}

func postMutate(srv *webhookServer, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/mutate-hostedcluster", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handleMutate(rec, req)
	return rec
}

func TestMutateHostedCluster(t *testing.T) {
	for _, tc := range []struct {
		name          string
		body          []byte
		wantStatus    int
		wantAllowed   bool
		wantPatch     bool
		wantPatchJSON string
	}{
		{
			name:          "When nodeSelector is nil it should inject default nodeSelector",
			body:          makeReview(admissionv1.Create, `{"spec":{}}`),
			wantStatus:    http.StatusOK,
			wantAllowed:   true,
			wantPatch:     true,
			wantPatchJSON: patchNodeSelector,
		},
		{
			name:          "When nodeSelector is empty map it should inject default nodeSelector",
			body:          makeReview(admissionv1.Create, `{"spec":{"nodeSelector":{}}}`),
			wantStatus:    http.StatusOK,
			wantAllowed:   true,
			wantPatch:     true,
			wantPatchJSON: patchNodeSelector,
		},
		{
			name:          "When spec is null it should inject nodeSelector via full-spec patch",
			body:          makeReview(admissionv1.Create, `{"spec":null}`),
			wantStatus:    http.StatusOK,
			wantAllowed:   true,
			wantPatch:     true,
			wantPatchJSON: patchWithSpec,
		},
		{
			name:          "When spec is absent it should inject nodeSelector via full-spec patch",
			body:          makeReview(admissionv1.Create, `{}`),
			wantStatus:    http.StatusOK,
			wantAllowed:   true,
			wantPatch:     true,
			wantPatchJSON: patchWithSpec,
		},
		{
			name:        "When nodeSelector is already set it should not mutate",
			body:        makeReview(admissionv1.Create, `{"spec":{"nodeSelector":{"foo":"bar"}}}`),
			wantStatus:  http.StatusOK,
			wantAllowed: true,
			wantPatch:   false,
		},
		{
			name:        "When nodeSelector has a different control-plane label it should not mutate",
			body:        makeReview(admissionv1.Create, `{"spec":{"nodeSelector":{"node-role.kubernetes.io/infra":""}}}`),
			wantStatus:  http.StatusOK,
			wantAllowed: true,
			wantPatch:   false,
		},
		{
			name:        "When request is UPDATE it should allow without mutation",
			body:        makeReview(admissionv1.Update, `{"spec":{}}`),
			wantStatus:  http.StatusOK,
			wantAllowed: true,
			wantPatch:   false,
		},
		{
			name:        "When request is DELETE it should allow without mutation",
			body:        makeReview(admissionv1.Delete, `{"spec":{}}`),
			wantStatus:  http.StatusOK,
			wantAllowed: true,
			wantPatch:   false,
		},
		{
			name:        "When HostedCluster object is malformed it should deny",
			body:        makeReview(admissionv1.Create, `{"spec":{"nodeSelector": 42}}`),
			wantStatus:  http.StatusOK,
			wantAllowed: false,
			wantPatch:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := testServer()
			rec := postMutate(srv, tc.body)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}

			var review admissionv1.AdmissionReview
			if err := json.Unmarshal(rec.Body.Bytes(), &review); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}

			if review.Response.UID != "test-uid" {
				t.Fatalf("expected UID test-uid, got %s", review.Response.UID)
			}

			if review.Response.Allowed != tc.wantAllowed {
				t.Fatalf("expected allowed=%v, got %v", tc.wantAllowed, review.Response.Allowed)
			}

			if tc.wantPatch {
				if review.Response.Patch == nil {
					t.Fatal("expected patch but got nil")
				}
				var got, want any
				if err := json.Unmarshal(review.Response.Patch, &got); err != nil {
					t.Fatalf("failed to unmarshal actual patch: %v", err)
				}
				if err := json.Unmarshal([]byte(tc.wantPatchJSON), &want); err != nil {
					t.Fatalf("failed to unmarshal expected patch: %v", err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("patch mismatch\n  got:  %s\n  want: %s", string(review.Response.Patch), tc.wantPatchJSON)
				}
				if review.Response.PatchType == nil || *review.Response.PatchType != admissionv1.PatchTypeJSONPatch {
					t.Fatal("expected patch type JSONPatch")
				}
			} else if review.Response.Patch != nil {
				t.Fatalf("expected no patch but got: %s", string(review.Response.Patch))
			}
		})
	}
}

func TestMutateHostedClusterInputValidation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		method     string
		ct         string
		body       string
		wantStatus int
	}{
		{
			name:       "When body is malformed JSON it should return HTTP 400",
			method:     http.MethodPost,
			ct:         "application/json",
			body:       "not json",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "When content type is text/plain it should return HTTP 415",
			method:     http.MethodPost,
			ct:         "text/plain",
			body:       "{}",
			wantStatus: http.StatusUnsupportedMediaType,
		},
		{
			name:       "When content type is empty it should return HTTP 415",
			method:     http.MethodPost,
			ct:         "",
			body:       "{}",
			wantStatus: http.StatusUnsupportedMediaType,
		},
		{
			name:       "When method is GET it should return HTTP 405",
			method:     http.MethodGet,
			ct:         "application/json",
			body:       "{}",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "When method is PUT it should return HTTP 405",
			method:     http.MethodPut,
			ct:         "application/json",
			body:       "{}",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "When admission review has nil request it should return HTTP 400",
			method:     http.MethodPost,
			ct:         "application/json",
			body:       `{"apiVersion":"admission.k8s.io/v1","kind":"AdmissionReview"}`,
			wantStatus: http.StatusBadRequest,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := testServer()
			req := httptest.NewRequest(tc.method, "/mutate-hostedcluster", strings.NewReader(tc.body))
			if tc.ct != "" {
				req.Header.Set("Content-Type", tc.ct)
			}
			rec := httptest.NewRecorder()
			srv.handleMutate(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMutateHostedClusterCounters(t *testing.T) {
	t.Run("When processing multiple requests it should track counters accurately", func(t *testing.T) {
		srv := testServer()

		postMutate(srv, makeReview(admissionv1.Create, `{"spec":{}}`))          // mutated (nodeSelector)
		postMutate(srv, makeReview(admissionv1.Create, `{}`))                   // mutated (full spec)
		postMutate(srv, makeReview(admissionv1.Create, `{"spec":{"nodeSelector":{"x":"y"}}}`)) // skipped
		postMutate(srv, makeReview(admissionv1.Update, `{"spec":{}}`))          // skipped (non-CREATE)
		postMutate(srv, makeReview(admissionv1.Create, `{"spec":{"nodeSelector": 42}}`)) // denied

		if got := srv.mutated.Load(); got != 2 {
			t.Fatalf("expected mutated=2, got %d", got)
		}
		if got := srv.skipped.Load(); got != 2 {
			t.Fatalf("expected skipped=2, got %d", got)
		}
		if got := srv.denied.Load(); got != 1 {
			t.Fatalf("expected denied=1, got %d", got)
		}
	})
}

func TestHealthzEndpoint(t *testing.T) {
	t.Run("When checking health it should return 200 ok", func(t *testing.T) {
		srv := testServer()
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		srv.handleHealthz(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if rec.Body.String() != "ok" {
			t.Fatalf("expected body 'ok', got %q", rec.Body.String())
		}
	})
}

func TestReadyzEndpoint(t *testing.T) {
	t.Run("When checking readiness it should return 200 ok", func(t *testing.T) {
		srv := testServer()
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		rec := httptest.NewRecorder()
		srv.handleReadyz(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})
}

func TestStatsEndpoint(t *testing.T) {
	t.Run("When requesting stats it should return valid JSON with counters", func(t *testing.T) {
		srv := testServer()

		postMutate(srv, makeReview(admissionv1.Create, `{"spec":{}}`))
		postMutate(srv, makeReview(admissionv1.Create, `{"spec":{"nodeSelector":{"x":"y"}}}`))

		req := httptest.NewRequest(http.MethodGet, "/stats", nil)
		rec := httptest.NewRecorder()
		srv.handleStats(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		ct := rec.Header().Get("Content-Type")
		if ct != "application/json" {
			t.Fatalf("expected content-type application/json, got %s", ct)
		}

		var stats map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
			t.Fatalf("failed to unmarshal stats: %v", err)
		}

		if stats["mutated"] != float64(1) {
			t.Fatalf("expected mutated=1, got %v", stats["mutated"])
		}
		if stats["skipped"] != float64(1) {
			t.Fatalf("expected skipped=1, got %v", stats["skipped"])
		}
		if stats["total"] != float64(2) {
			t.Fatalf("expected total=2, got %v", stats["total"])
		}
	})
}

func TestParseLogLevel(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"", slog.LevelInfo},
		{"unknown", slog.LevelInfo},
	} {
		t.Run("When log level is "+tc.input+" it should parse correctly", func(t *testing.T) {
			got := parseLogLevel(tc.input)
			if got != tc.want {
				t.Fatalf("parseLogLevel(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}
