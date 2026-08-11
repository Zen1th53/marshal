package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/Zen1th53/slaves/internal/app"
	"github.com/Zen1th53/slaves/internal/model"
)

type Client struct {
	http *http.Client
}

func NewClient(socket string) *Client {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", socket)
	}}
	return &Client{http: &http.Client{Transport: transport, Timeout: 35 * time.Second}}
}

func (c *Client) HTTP() *http.Client { return c.http }

func (c *Client) Version(ctx context.Context) (Version, string, error) {
	var value Version
	id, err := c.do(ctx, http.MethodGet, "/v1/version", nil, &value)
	return value, id, err
}

func (c *Client) Status(ctx context.Context) (app.Status, string, error) {
	var value app.Status
	id, err := c.do(ctx, http.MethodGet, "/v1/status", nil, &value)
	return value, id, err
}

func (c *Client) RegisterAgent(ctx context.Context, input app.RegisterAgentRequest) (model.Agent, string, error) {
	var value model.Agent
	id, err := c.do(ctx, http.MethodPost, "/v1/agents", input, &value)
	return value, id, err
}

func (c *Client) Agents(ctx context.Context) ([]model.Agent, string, error) {
	var value []model.Agent
	id, err := c.do(ctx, http.MethodGet, "/v1/agents", nil, &value)
	return value, id, err
}

func (c *Client) ImportTasks(ctx context.Context, input []model.Task) (model.ImportResult, string, error) {
	var value model.ImportResult
	id, err := c.do(ctx, http.MethodPost, "/v1/tasks/import", input, &value)
	return value, id, err
}

func (c *Client) Tasks(ctx context.Context) ([]model.Task, string, error) {
	var value []model.Task
	id, err := c.do(ctx, http.MethodGet, "/v1/tasks", nil, &value)
	return value, id, err
}

func (c *Client) Task(ctx context.Context, taskID string) (model.Task, string, error) {
	var value model.Task
	id, err := c.do(ctx, http.MethodGet, "/v1/tasks/"+taskID, nil, &value)
	return value, id, err
}

func (c *Client) Claim(ctx context.Context, input app.ClaimRequest) (app.ClaimResult, string, error) {
	var value app.ClaimResult
	id, err := c.do(ctx, http.MethodPost, "/v1/tasks/"+input.TaskID+"/claim", input, &value)
	return value, id, err
}

func (c *Client) Release(ctx context.Context, input app.ReleaseRequest) (string, error) {
	return c.do(ctx, http.MethodPost, "/v1/tasks/"+input.TaskID+"/release", input, nil)
}

func (c *Client) Run(ctx context.Context, input app.RunRequest) (app.RunResult, string, error) {
	var value app.RunResult
	id, err := c.do(ctx, http.MethodPost, "/v1/tasks/"+input.TaskID+"/run", input, &value)
	return value, id, err
}

func (c *Client) Events(ctx context.Context) ([]model.Event, string, error) {
	var value []model.Event
	id, err := c.do(ctx, http.MethodGet, "/v1/events", nil, &value)
	return value, id, err
}

func (c *Client) Artifacts(ctx context.Context) ([]model.Artifact, string, error) {
	var value []model.Artifact
	id, err := c.do(ctx, http.MethodGet, "/v1/artifacts", nil, &value)
	return value, id, err
}

func (c *Client) Verify(ctx context.Context, input app.VerifyRequest) (app.VerifyResult, string, error) {
	var value app.VerifyResult
	id, err := c.do(ctx, http.MethodPost, "/v1/verify", input, &value)
	return value, id, err
}

func (c *Client) do(ctx context.Context, method, path string, input, output any) (string, error) {
	var body bytes.Buffer
	if input != nil {
		if err := json.NewEncoder(&body).Encode(input); err != nil {
			return "", fmt.Errorf("encode API request: %w", err)
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://unix"+path, &body)
	if err != nil {
		return "", fmt.Errorf("create API request: %w", err)
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return "", fmt.Errorf("%w: connect to local runtime: %v", model.ErrUnavailable, err)
	}
	defer response.Body.Close()
	var envelope Envelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return "", fmt.Errorf("decode API response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return envelope.RequestID, remoteError(envelope.Error)
	}
	if output != nil && len(envelope.Data) != 0 {
		if err := json.Unmarshal(envelope.Data, output); err != nil {
			return envelope.RequestID, fmt.Errorf("decode API result: %w", err)
		}
	}
	return envelope.RequestID, nil
}
