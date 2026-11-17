// Package api implements the Unkindled control-plane client.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the public control plane.
	DefaultBaseURL = "https://api.unkindled.dev"
	userAgent      = "unkindled-cli/0.1.0"
	maxBody        = 2 << 20
)

// Client is a small JSON client for the Unkindled API.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New returns a client rooted at baseURL. baseURL is normalized and validated.
func New(baseURL, token string) (*Client, error) {
	normalized, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	return &Client{
		BaseURL: normalized,
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// NormalizeBaseURL validates an API URL and removes trailing slashes.
func NormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = DefaultBaseURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid API URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("invalid API URL %q: scheme must be http or https", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid API URL %q: missing host", raw)
	}
	if u.User != nil {
		return "", fmt.Errorf("invalid API URL %q: credentials in URLs are not supported", raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), nil
}

// HTTPError is a non-2xx API response. Its message never includes raw request
// or response bodies so secrets cannot leak through error output.
type HTTPError struct {
	Method     string
	Path       string
	StatusCode int
	Code       string
	Message    string
	Body       []byte
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.Path, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("%s %s: HTTP %d", e.Method, e.Path, e.StatusCode)
}

// Hint returns an actionable suggestion for common API failures.
func (e *HTTPError) Hint() string {
	switch e.StatusCode {
	case http.StatusUnauthorized:
		return "authentication failed; run `unkindled login` to sign in again"
	case http.StatusForbidden:
		return "the token does not have access to this resource"
	case http.StatusNotFound:
		return "the resource was not found; check that the workstation still exists"
	case http.StatusTooManyRequests:
		return "the API is rate limiting requests; wait a moment and try again"
	default:
		if e.StatusCode >= 500 {
			return "the control plane is unavailable; try again shortly"
		}
		return ""
	}
}

func (c *Client) do(ctx context.Context, method, path string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, fmt.Errorf("cannot reach %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("read response from %s: %w", c.BaseURL, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		message, code := decodeAPIError(data)
		return data, &HTTPError{
			Method:     method,
			Path:       path,
			StatusCode: resp.StatusCode,
			Code:       code,
			Message:    message,
			Body:       data,
		}
	}
	return data, nil
}

// decodeAPIError parses both the nested error envelope used by the control
// plane and simpler OAuth-style error responses.
func decodeAPIError(data []byte) (message, code string) {
	var probe struct {
		Error            json.RawMessage `json:"error"`
		Code             string          `json:"code"`
		Message          string          `json:"message"`
		ErrorDescription string          `json:"error_description"`
		Detail           string          `json:"detail"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return "", ""
	}

	var nested struct {
		Code             string `json:"code"`
		Message          string `json:"message"`
		ErrorDescription string `json:"error_description"`
		Detail           string `json:"detail"`
	}
	if len(probe.Error) > 0 {
		if err := json.Unmarshal(probe.Error, &nested); err == nil {
			code = strings.TrimSpace(nested.Code)
			for _, candidate := range []string{nested.Message, nested.ErrorDescription, nested.Detail} {
				if strings.TrimSpace(candidate) != "" {
					return strings.TrimSpace(candidate), code
				}
			}
		}
		var simple string
		if err := json.Unmarshal(probe.Error, &simple); err == nil && strings.TrimSpace(simple) != "" {
			return strings.TrimSpace(simple), strings.TrimSpace(probe.Code)
		}
	}
	if code == "" {
		code = strings.TrimSpace(probe.Code)
	}
	for _, candidate := range []string{probe.Message, probe.ErrorDescription, probe.Detail} {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate), code
		}
	}
	return "", code
}

// Workstation is a user workstation returned by the control plane.
type Workstation struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	State                string `json:"state"`
	Plan                 string `json:"plan"`
	IPv4                 string `json:"ipv4"`
	Hostname             string `json:"hostname"`
	SSHAlias             string `json:"ssh_alias"`
	PlatformGeneratedKey bool   `json:"platform_generated_key"`
}

// Address returns the best available address for the workstation.
func (w Workstation) Address() string {
	if w.IPv4 != "" {
		return w.IPv4
	}
	return w.Hostname
}

// ListWorkstations returns the authenticated user's workstations.
func (c *Client) ListWorkstations(ctx context.Context) ([]Workstation, error) {
	data, err := c.do(ctx, http.MethodGet, "/v1/workstations", nil)
	if err != nil {
		return nil, err
	}
	return decodeWorkstations(data)
}

func decodeWorkstations(data []byte) ([]Workstation, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("empty response from /v1/workstations")
	}
	if trimmed[0] == '[' {
		var list []Workstation
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return nil, fmt.Errorf("decode workstations: %w", err)
		}
		return list, nil
	}

	var wrapper struct {
		Workstations []Workstation `json:"workstations"`
		Items        []Workstation `json:"items"`
		Data         []Workstation `json:"data"`
	}
	if err := json.Unmarshal(trimmed, &wrapper); err != nil {
		return nil, fmt.Errorf("decode workstations: %w", err)
	}
	switch {
	case wrapper.Workstations != nil:
		return wrapper.Workstations, nil
	case wrapper.Items != nil:
		return wrapper.Items, nil
	case wrapper.Data != nil:
		return wrapper.Data, nil
	default:
		return nil, errors.New("unexpected response from /v1/workstations: missing workstation list")
	}
}

// Connection describes how to reach a workstation over SSH.
type Connection struct {
	WorkstationID        string `json:"workstation_id"`
	IPv4                 string `json:"ipv4"`
	Hostname             string `json:"hostname"`
	Host                 string `json:"host"`
	SSHHost              string `json:"ssh_host"`
	User                 string `json:"user"`
	SSHUser              string `json:"ssh_user"`
	Port                 int    `json:"port"`
	SSHPort              int    `json:"ssh_port"`
	SSHAlias             string `json:"ssh_alias"`
	SSHHostAlias         string `json:"ssh_host_alias"`
	Alias                string `json:"alias"`
	PlatformGeneratedKey bool   `json:"platform_generated_key"`
	GeneratedKey         bool   `json:"generated_key"`
	KeyPath              string `json:"key_path"`
	PrivateKeyPath       string `json:"private_key_path"`
	IdentityFile         string `json:"identity_file"`
}

// Address returns the host to put in an SSH config HostName line.
func (c Connection) Address() string {
	for _, candidate := range []string{c.IPv4, c.Host, c.SSHHost, c.Hostname} {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
	}
	return ""
}

// LoginUser returns the SSH user, defaulting to root when omitted.
func (c Connection) LoginUser() string {
	for _, candidate := range []string{c.User, c.SSHUser} {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
	}
	return "root"
}

// PortNumber returns the SSH port, defaulting to 22.
func (c Connection) PortNumber() int {
	if c.Port != 0 {
		return c.Port
	}
	if c.SSHPort != 0 {
		return c.SSHPort
	}
	return 22
}

// Generated reports whether the platform holds the private key.
func (c Connection) Generated() bool {
	return c.PlatformGeneratedKey || c.GeneratedKey
}

// PreferredAlias returns the server-suggested SSH alias, if any.
func (c Connection) PreferredAlias() string {
	for _, candidate := range []string{c.SSHHostAlias, c.SSHAlias, c.Alias} {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
	}
	return ""
}

// PreferredKeyPath returns the server-suggested identity file, if any.
func (c Connection) PreferredKeyPath() string {
	for _, candidate := range []string{c.KeyPath, c.PrivateKeyPath, c.IdentityFile} {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
	}
	return ""
}

// GetConnection fetches SSH connection details for a workstation.
func (c *Client) GetConnection(ctx context.Context, workstationID string) (Connection, error) {
	path := "/v1/workstations/" + url.PathEscape(workstationID) + "/connection"
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return Connection{}, err
	}
	return decodeConnection(data)
}

func decodeConnection(data []byte) (Connection, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return Connection{}, fmt.Errorf("decode connection: %w", err)
	}
	for _, key := range []string{"connection", "data"} {
		if raw, ok := probe[key]; ok && len(bytes.TrimSpace(raw)) > 0 && string(bytes.TrimSpace(raw)) != "null" {
			var conn Connection
			if err := json.Unmarshal(raw, &conn); err != nil {
				return Connection{}, fmt.Errorf("decode connection: %w", err)
			}
			return conn, nil
		}
	}
	var conn Connection
	if err := json.Unmarshal(data, &conn); err != nil {
		return Connection{}, fmt.Errorf("decode connection: %w", err)
	}
	return conn, nil
}

// RevealSSHKey requests the private key for a platform-generated workstation
// key. The returned key must never be logged.
func (c *Client) RevealSSHKey(ctx context.Context, workstationID string) (string, error) {
	path := "/v1/workstations/" + url.PathEscape(workstationID) + "/ssh-key/reveal"
	data, err := c.do(ctx, http.MethodPost, path, struct{}{})
	if err != nil {
		return "", err
	}
	return decodeRevealedKey(data)
}

func decodeRevealedKey(data []byte) (string, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(trimmed, &value); err == nil && strings.TrimSpace(value) != "" {
			return value, nil
		}
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return "", fmt.Errorf("decode SSH key response: %w", err)
	}
	for _, key := range []string{"private_key", "private_key_pem", "key", "privateKey", "key_material"} {
		raw, ok := probe[key]
		if !ok {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err == nil && strings.TrimSpace(value) != "" {
			return value, nil
		}
		var nested struct {
			Content string `json:"content"`
			Value   string `json:"value"`
		}
		if err := json.Unmarshal(raw, &nested); err == nil {
			if nested.Content != "" {
				return nested.Content, nil
			}
			if nested.Value != "" {
				return nested.Value, nil
			}
		}
	}
	return "", errors.New("SSH key response did not contain a private key")
}

// Template is a workstation template returned by the control plane.
type Template struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Plan        string `json:"plan"`
	Builtin     bool   `json:"builtin"`
}

// LaunchTemplateRequest is the body for launching a workstation from a template.
type LaunchTemplateRequest struct {
	Name               string `json:"name,omitempty"`
	Plan               string `json:"plan,omitempty"`
	SSHPublicKey       string `json:"ssh_public_key,omitempty"`
	RepositoryURL      string `json:"repository_url,omitempty"`
	RepositoryBranch   string `json:"repository_branch,omitempty"`
	RepositoryAuthType string `json:"repository_auth_type,omitempty"`
}

// ListTemplates returns the templates available to the signed-in user.
func (c *Client) ListTemplates(ctx context.Context) ([]Template, error) {
	data, err := c.do(ctx, http.MethodGet, "/v1/templates", nil)
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("empty response from /v1/templates")
	}
	if trimmed[0] == '[' {
		var list []Template
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return nil, fmt.Errorf("decode templates: %w", err)
		}
		return list, nil
	}
	var wrapper struct {
		Templates []Template `json:"templates"`
		Items     []Template `json:"items"`
		Data      []Template `json:"data"`
	}
	if err := json.Unmarshal(trimmed, &wrapper); err != nil {
		return nil, fmt.Errorf("decode templates: %w", err)
	}
	switch {
	case wrapper.Templates != nil:
		return wrapper.Templates, nil
	case wrapper.Items != nil:
		return wrapper.Items, nil
	case wrapper.Data != nil:
		return wrapper.Data, nil
	default:
		return nil, errors.New("unexpected response from /v1/templates: missing template list")
	}
}

// LaunchTemplate creates a workstation from a template.
func (c *Client) LaunchTemplate(ctx context.Context, templateID string, in LaunchTemplateRequest) (Workstation, error) {
	path := "/v1/templates/" + url.PathEscape(templateID) + "/launch"
	data, err := c.do(ctx, http.MethodPost, path, in)
	if err != nil {
		return Workstation{}, err
	}
	return decodeWorkstation(data)
}

func decodeWorkstation(data []byte) (Workstation, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return Workstation{}, errors.New("empty workstation response")
	}
	if trimmed[0] == '{' {
		var wrapper struct {
			Workstation *Workstation `json:"workstation"`
			Data        *Workstation `json:"data"`
		}
		if err := json.Unmarshal(trimmed, &wrapper); err != nil {
			return Workstation{}, fmt.Errorf("decode workstation: %w", err)
		}
		if wrapper.Workstation != nil {
			return *wrapper.Workstation, nil
		}
		if wrapper.Data != nil {
			return *wrapper.Data, nil
		}
		var ws Workstation
		if err := json.Unmarshal(trimmed, &ws); err == nil && ws.ID != "" {
			return ws, nil
		}
	}
	return Workstation{}, errors.New("unexpected workstation response")
}

// StopWorkstation requests a graceful stop.
func (c *Client) StopWorkstation(ctx context.Context, workstationID string) error {
	path := "/v1/workstations/" + url.PathEscape(workstationID) + "/stop"
	_, err := c.do(ctx, http.MethodPost, path, struct{}{})
	return err
}

// KillWorkstation deletes a workstation and its disk resources. The caller must
// explicitly confirm before using it.
func (c *Client) KillWorkstation(ctx context.Context, workstationID string) error {
	path := "/v1/workstations/" + url.PathEscape(workstationID) + "?confirm=true"
	_, err := c.do(ctx, http.MethodDelete, path, nil)
	return err
}
