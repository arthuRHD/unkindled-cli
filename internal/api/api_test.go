package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeBaseURL(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "https://api.unkindled.dev/", want: "https://api.unkindled.dev"},
		{in: "", want: DefaultBaseURL},
		{in: "http://localhost:8080/api/", want: "http://localhost:8080/api"},
		{in: "ftp://example.test", wantErr: true},
		{in: "not a url", wantErr: true},
		{in: "https://user:pass@example.test", wantErr: true},
	}
	for _, tt := range tests {
		got, err := NormalizeBaseURL(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("NormalizeBaseURL(%q) = %q, want error", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeBaseURL(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("NormalizeBaseURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestListWorkstations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"workstations":[{"id":"ws-1","name":"dev","state":"running","plan":"pro","ipv4":"203.0.113.9"}]}`))
	}))
	defer server.Close()

	client, err := New(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	list, err := client.ListWorkstations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "ws-1" || list[0].Address() != "203.0.113.9" {
		t.Fatalf("list = %+v", list)
	}
}

func TestListWorkstationsArrayAndErrors(t *testing.T) {
	for _, body := range []string{
		`[{"id":"a"}]`,
		`{"items":[{"id":"b"}]}`,
		`{"data":[{"id":"c"}]}`,
	} {
		got, err := decodeWorkstations([]byte(body))
		if err != nil {
			t.Errorf("decodeWorkstations(%s): %v", body, err)
			continue
		}
		if len(got) != 1 {
			t.Errorf("decodeWorkstations(%s) = %+v", body, got)
		}
	}
	if _, err := decodeWorkstations([]byte(`{"unexpected":true}`)); err == nil {
		t.Fatal("expected error for missing list")
	}
}

func TestHTTPErrorDoesNotLeakBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"invalid token"},"secret":"do-not-print"}`))
	}))
	defer server.Close()

	client, err := New(server.URL, "bad-token")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListWorkstations(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "do-not-print") {
		t.Fatalf("error leaked response body: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid token") {
		t.Fatalf("error did not include API message: %v", err)
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error type = %T", err)
	}
	if httpErr.StatusCode != http.StatusUnauthorized || httpErr.Code != "unauthorized" || !strings.Contains(httpErr.Hint(), "unkindled login") {
		t.Fatalf("HTTPError = %+v", httpErr)
	}
}

func TestDecodeAPIErrorEnvelope(t *testing.T) {
	tests := []struct {
		body    string
		message string
		code    string
	}{
		{`{"error":{"code":"device_flow_denied","message":"GitHub sign-in was denied"}}`, "GitHub sign-in was denied", "device_flow_denied"},
		{`{"error":"unauthorized","code":"auth"}`, "unauthorized", "auth"},
		{`{"message":"bad request","detail":"field missing"}`, "bad request", ""},
		{`{"error":{"code":"internal"}}`, "", "internal"},
	}
	for _, tt := range tests {
		message, code := decodeAPIError([]byte(tt.body))
		if message != tt.message || code != tt.code {
			t.Errorf("decodeAPIError(%s) = %q, %q want %q, %q", tt.body, message, code, tt.message, tt.code)
		}
	}
}

func TestGetConnection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/workstations/ws-1/connection" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"connection":{"workstation_id":"ws-1","ipv4":"203.0.113.9","user":"agent","ssh_host_alias":"unkindled-dev","identity_file":"~/.ssh/id_ed25519","platform_generated_key":true,"name":"dev"}}`))
	}))
	defer server.Close()
	client, err := New(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := client.GetConnection(context.Background(), "ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if conn.Address() != "203.0.113.9" || conn.LoginUser() != "agent" || !conn.Generated() {
		t.Fatalf("connection = %+v", conn)
	}
	if conn.PreferredAlias() != "unkindled-dev" || conn.PreferredKeyPath() != "~/.ssh/id_ed25519" {
		t.Fatalf("connection preferences = %+v", conn)
	}
	if conn.PortNumber() != 22 {
		t.Fatalf("port = %d", conn.PortNumber())
	}
}

func TestRevealSSHKey(t *testing.T) {
	for _, body := range []string{
		`{"private_key":"KEY"}`,
		`{"private_key_pem":"KEY"}`,
		`{"privateKey":"KEY"}`,
		`"KEY"`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/v1/workstations/ws-1/ssh-key/reveal" {
				t.Errorf("request = %s %s", r.Method, r.URL.Path)
			}
			_, _ = w.Write([]byte(body))
		}))
		client, err := New(server.URL, "token")
		if err != nil {
			t.Fatal(err)
		}
		key, err := client.RevealSSHKey(context.Background(), "ws-1")
		server.Close()
		if err != nil {
			t.Errorf("body %s: %v", body, err)
			continue
		}
		if key != "KEY" {
			t.Errorf("body %s: key = %q", body, key)
		}
	}
	if _, err := decodeRevealedKey([]byte(`{"nope":true}`)); err == nil {
		t.Fatal("expected missing key error")
	}
}

func TestDevicePollStatuses(t *testing.T) {
	tests := []struct {
		status int
		body   string
		want   string
	}{
		{status: http.StatusOK, body: `{"status":"pending"}`, want: "pending"},
		{status: http.StatusOK, body: `{"status":"complete","token":"tok"}`, want: "complete"},
		{status: http.StatusTooManyRequests, body: `{}`, want: "slow_down"},
		{status: http.StatusBadRequest, body: `{"status":"denied"}`, want: "denied"},
		{status: http.StatusBadRequest, body: `{"error":"expired"}`, want: "expired"},
		{status: http.StatusBadRequest, body: `{"error":{"code":"device_flow_expired","message":"expired"}}`, want: "expired"},
		{status: http.StatusForbidden, body: `{"error":{"code":"device_flow_denied","message":"denied"}}`, want: "denied"},
	}
	for _, tt := range tests {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/auth/github/device/poll" {
				t.Errorf("path = %q", r.URL.Path)
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["device_code"] != "dev" {
				t.Errorf("poll body = %+v, err = %v", body, err)
			}
			w.WriteHeader(tt.status)
			_, _ = w.Write([]byte(tt.body))
		}))
		client, err := New(server.URL, "")
		if err != nil {
			t.Fatal(err)
		}
		poll, err := client.PollGitHubDevice(context.Background(), "dev")
		server.Close()
		if err != nil {
			t.Errorf("status %d body %s: %v", tt.status, tt.body, err)
			continue
		}
		if poll.Status != tt.want {
			t.Errorf("status %d body %s = %q, want %q", tt.status, tt.body, poll.Status, tt.want)
		}
	}
}

func TestStartGitHubDeviceDefaultsURI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"device_code":"dev","user_code":"CODE"}`))
	}))
	defer server.Close()
	client, err := New(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	start, err := client.StartGitHubDevice(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if start.VerificationURI != "https://github.com/login/device" {
		t.Fatalf("verification URI = %q", start.VerificationURI)
	}
	if start.DeviceCode != "dev" || start.UserCode != "CODE" {
		t.Fatalf("start = %+v", start)
	}
}
