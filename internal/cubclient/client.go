// Package cubclient is a thin wrapper around github.com/confighub/sdk
// goclient-new, scoped to the operations lk needs. It reads CUB_SERVER and
// CUB_TOKEN from the environment (set by `cub` when invoking a plugin) and
// adds a Bearer auth header to every request.
//
// SDK extraction candidates discovered while building this:
//   - The new-prefix logic (see NewPrefix below): cub-internal today, lives
//     in cmd/cub/space_new_prefix.go in package main. Useful as a stable
//     library helper.
//   - "Create entity, rollback siblings on failure" pattern used by lk up —
//     would be a nice transactional helper.
//   - cub worker install --export manifest generation: 250 lines in
//     cmd/cub/worker_install.go in package main. Re-implementing it would
//     drift from cub; we shell out instead. Prime extraction target.
package cubclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/confighub/sdk/core/cubbyname"
	goclient "github.com/confighub/sdk/core/openapi/goclient-new"
	"github.com/google/uuid"
)

type Client struct {
	api    *goclient.ClientWithResponses
	server string
}

// New constructs a Client from CUB_SERVER and CUB_TOKEN. Both must be set;
// these are populated automatically when running as a cub plugin.
func New() (*Client, error) {
	server := os.Getenv("CUB_SERVER")
	if server == "" {
		return nil, fmt.Errorf("CUB_SERVER not set; lk must be invoked as a cub plugin (try: cub lk ...)")
	}
	token := os.Getenv("CUB_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("CUB_TOKEN not set; run `cub auth login` first")
	}

	authHeader := "Bearer " + token
	api, err := goclient.NewClientWithResponses(strings.TrimRight(server, "/")+"/api",
		goclient.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", authHeader)
			return nil
		}))
	if err != nil {
		return nil, fmt.Errorf("init client: %w", err)
	}
	return &Client{api: api, server: server}, nil
}

// Server returns the configured server URL (without trailing slash or /api).
func (c *Client) Server() string { return strings.TrimRight(c.server, "/") }

// NewPrefix mirrors `cub space new-prefix`: generate a random name that is
// not a prefix of any existing space slug. Returns after up to 1000 tries.
func (c *Client) NewPrefix(ctx context.Context) (string, error) {
	all, err := c.listAllSpaces(ctx)
	if err != nil {
		return "", fmt.Errorf("list spaces: %w", err)
	}
	for range 1000 {
		candidate := cubbyname.Random()
		collide := false
		for _, slug := range all {
			if strings.HasPrefix(slug, candidate) {
				collide = true
				break
			}
		}
		if !collide {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not generate a unique prefix after 1000 attempts")
}

func (c *Client) listAllSpaces(ctx context.Context) ([]string, error) {
	resp, err := c.api.ListSpacesWithResponse(ctx, &goclient.ListSpacesParams{})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("list spaces: %s: %s", resp.Status(), string(resp.Body))
	}
	if resp.JSON200 == nil {
		return nil, nil
	}
	out := make([]string, 0, len(*resp.JSON200))
	for _, ext := range *resp.JSON200 {
		if ext.Space != nil {
			out = append(out, ext.Space.Slug)
		}
	}
	return out, nil
}

// SpaceExists checks whether a space with the given slug already exists.
func (c *Client) SpaceExists(ctx context.Context, slug string) (bool, error) {
	resp, err := c.api.ListSpacesWithResponse(ctx, &goclient.ListSpacesParams{})
	if err != nil {
		return false, err
	}
	if resp.StatusCode() != http.StatusOK {
		return false, fmt.Errorf("list spaces: %s: %s", resp.Status(), string(resp.Body))
	}
	if resp.JSON200 == nil {
		return false, nil
	}
	for _, ext := range *resp.JSON200 {
		if ext.Space != nil && ext.Space.Slug == slug {
			return true, nil
		}
	}
	return false, nil
}

// CreateSpace creates a new space and returns its UUID.
func (c *Client) CreateSpace(ctx context.Context, slug, displayName string, labels map[string]string) (uuid.UUID, error) {
	body := goclient.Space{
		Slug:        slug,
		DisplayName: displayName,
		Labels:      labels,
	}
	resp, err := c.api.CreateSpaceWithResponse(ctx, &goclient.CreateSpaceParams{}, body)
	if err != nil {
		return uuid.Nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		return uuid.Nil, fmt.Errorf("create space %q: %s: %s", slug, resp.Status(), string(resp.Body))
	}
	if resp.JSON200 == nil {
		return uuid.Nil, fmt.Errorf("create space %q: empty response", slug)
	}
	return resp.JSON200.SpaceID, nil
}

// DeleteSpaceBySlug deletes a space by its slug. Cascade-deletes its
// workers, targets, and units server-side.
func (c *Client) DeleteSpaceBySlug(ctx context.Context, slug string) error {
	id, err := c.spaceIDBySlug(ctx, slug)
	if err != nil {
		return err
	}
	resp, err := c.api.DeleteSpaceWithResponse(ctx, id, &goclient.DeleteSpaceParams{})
	if err != nil {
		return err
	}
	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusNoContent {
		return fmt.Errorf("delete space %q: %s: %s", slug, resp.Status(), string(resp.Body))
	}
	return nil
}

func (c *Client) spaceIDBySlug(ctx context.Context, slug string) (uuid.UUID, error) {
	resp, err := c.api.ListSpacesWithResponse(ctx, &goclient.ListSpacesParams{})
	if err != nil {
		return uuid.Nil, err
	}
	if resp.JSON200 == nil {
		return uuid.Nil, fmt.Errorf("space %q not found", slug)
	}
	for _, ext := range *resp.JSON200 {
		if ext.Space != nil && ext.Space.Slug == slug {
			return ext.Space.SpaceID, nil
		}
	}
	return uuid.Nil, fmt.Errorf("space %q not found", slug)
}

// LookupBridgeWorker returns the UUID of a worker by slug within a space.
// Use this after `cub worker install` has implicitly created it.
func (c *Client) LookupBridgeWorker(ctx context.Context, spaceID uuid.UUID, slug string) (uuid.UUID, error) {
	resp, err := c.api.ListBridgeWorkersWithResponse(ctx, spaceID, &goclient.ListBridgeWorkersParams{})
	if err != nil {
		return uuid.Nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		return uuid.Nil, fmt.Errorf("list workers: %s: %s", resp.Status(), string(resp.Body))
	}
	if resp.JSON200 == nil {
		return uuid.Nil, fmt.Errorf("worker %q not found", slug)
	}
	for _, ext := range *resp.JSON200 {
		if ext.BridgeWorker != nil && ext.BridgeWorker.Slug == slug {
			return ext.BridgeWorker.BridgeWorkerID, nil
		}
	}
	return uuid.Nil, fmt.Errorf("worker %q not found in space", slug)
}

// CreateKubernetesTarget creates a Kubernetes/YAML target bound to the
// given worker, with KubeContext set to the kind cluster's context.
func (c *Client) CreateKubernetesTarget(ctx context.Context, spaceID, workerID uuid.UUID, slug, displayName, kubeContext, namespace string) (uuid.UUID, error) {
	params := map[string]string{
		"KubeContext":   kubeContext,
		"KubeNamespace": namespace,
		"WaitTimeout":   "2m0s",
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return uuid.Nil, err
	}
	body := goclient.Target{
		SpaceID:        spaceID,
		Slug:           slug,
		DisplayName:    displayName,
		BridgeWorkerID: workerID,
		ToolchainType:  "Kubernetes/YAML",
		ProviderType:   "Kubernetes",
		Parameters:     string(paramsJSON),
	}
	resp, err := c.api.CreateTargetWithResponse(ctx, spaceID, &goclient.CreateTargetParams{}, body)
	if err != nil {
		return uuid.Nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		return uuid.Nil, fmt.Errorf("create target %q: %s: %s", slug, resp.Status(), string(resp.Body))
	}
	if resp.JSON200 == nil {
		return uuid.Nil, fmt.Errorf("create target %q: empty response", slug)
	}
	return resp.JSON200.TargetID, nil
}

// CreateYAMLUnit creates a Kubernetes/YAML Unit with manifest bytes,
// bound to the given target.
func (c *Client) CreateYAMLUnit(ctx context.Context, spaceID, targetID uuid.UUID, slug, displayName string, manifest []byte) (uuid.UUID, error) {
	body := goclient.Unit{
		SpaceID:       spaceID,
		Slug:          slug,
		DisplayName:   displayName,
		ToolchainType: "Kubernetes/YAML",
		Data:          base64.StdEncoding.EncodeToString(manifest),
		TargetID:      &targetID,
	}
	resp, err := c.api.CreateUnitWithResponse(ctx, spaceID, &goclient.CreateUnitParams{}, body)
	if err != nil {
		return uuid.Nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		return uuid.Nil, fmt.Errorf("create unit %q: %s: %s", slug, resp.Status(), string(resp.Body))
	}
	if resp.JSON200 == nil {
		return uuid.Nil, fmt.Errorf("create unit %q: empty response", slug)
	}
	return resp.JSON200.UnitID, nil
}
