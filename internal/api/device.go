package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// DeviceStart is the response from POST /v1/auth/github/device/start.
type DeviceStart struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// DevicePoll is the response from POST /v1/auth/github/device/poll.
type DevicePoll struct {
	Status           string         `json:"status"`
	Interval         int            `json:"interval"`
	Token            string         `json:"token"`
	User             map[string]any `json:"user"`
	Message          string         `json:"message"`
	Error            string         `json:"error"`
	ErrorDescription string         `json:"error_description"`
}

// DeviceStatusPending is the normal in-progress status.
const DeviceStatusPending = "pending"

// StartGitHubDevice begins the GitHub device flow.
func (c *Client) StartGitHubDevice(ctx context.Context) (*DeviceStart, error) {
	data, err := c.do(ctx, http.MethodPost, "/v1/auth/github/device/start", struct{}{})
	if err != nil {
		return nil, err
	}
	var start DeviceStart
	if err := json.Unmarshal(data, &start); err != nil {
		return nil, fmt.Errorf("decode device login response: %w", err)
	}
	if start.DeviceCode == "" || start.UserCode == "" {
		return nil, errors.New("device login response was missing device_code or user_code")
	}
	if start.VerificationURI == "" {
		start.VerificationURI = "https://github.com/login/device"
	}
	return &start, nil
}

// PollGitHubDevice checks whether the device flow has completed.
func (c *Client) PollGitHubDevice(ctx context.Context, deviceCode string) (*DevicePoll, error) {
	data, err := c.do(ctx, http.MethodPost, "/v1/auth/github/device/poll", map[string]string{
		"device_code": deviceCode,
	})
	if err != nil {
		// The control plane may report terminal device states with a non-2xx
		// status. Decode those into poll statuses rather than generic errors.
		var httpErr *HTTPError
		if errors.As(err, &httpErr) {
			if poll := decodePollStatus(httpErr.Body); poll != nil {
				if poll.Status != "" && poll.Status != DeviceStatusPending {
					return poll, nil
				}
			}
			switch httpErr.Code {
			case "device_flow_expired":
				return &DevicePoll{Status: "expired"}, nil
			case "device_flow_denied":
				return &DevicePoll{Status: "denied"}, nil
			}
			if httpErr.StatusCode == http.StatusTooManyRequests {
				return &DevicePoll{Status: "slow_down"}, nil
			}
		}
		return nil, err
	}
	poll, decodeErr := decodePoll(data)
	if decodeErr != nil {
		return nil, decodeErr
	}
	return poll, nil
}

func decodePoll(data []byte) (*DevicePoll, error) {
	var poll DevicePoll
	if err := json.Unmarshal(data, &poll); err != nil {
		return nil, fmt.Errorf("decode device login status: %w", err)
	}
	status := strings.ToLower(strings.TrimSpace(poll.Status))
	if status == "" && poll.Error != "" {
		status = strings.ToLower(strings.TrimSpace(poll.Error))
	}
	if status == "" {
		return nil, errors.New("device login status response was missing status")
	}
	poll.Status = status
	return &poll, nil
}

func decodePollStatus(data []byte) *DevicePoll {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil
	}
	poll, err := decodePoll(trimmed)
	if err != nil {
		return nil
	}
	return poll
}
