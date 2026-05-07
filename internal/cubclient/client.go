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
	"time"

	"github.com/confighub/sdk/core/cubbyname"
	goclient "github.com/confighub/sdk/core/openapi/goclient-new"
	"github.com/google/uuid"
)

// Annotation keys lk writes onto every Space it creates.
const (
	AnnotationClusterName = "ijn.me/cub-lk-cluster-name"
	AnnotationPortRange   = "ijn.me/cub-lk-port-range"
	AnnotationHost        = "ijn.me/cub-lk-host"
	// LabelLk is the queryable marker label.
	LabelLk = "cub-lk"
)

// LkSpace represents one lk-managed Space, decoded from the cub-lk Label
// and ijn.me/cub-lk-* annotations.
type LkSpace struct {
	SpaceID     uuid.UUID
	SpaceSlug   string
	ClusterName string // from AnnotationClusterName; falls back to SpaceSlug
	PortRange   string // from AnnotationPortRange
	Host        string // from AnnotationHost
	CreatedAt   time.Time
}

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

// ListLkSpacesForHost returns all Spaces in the current cub context that
// carry the cub-lk Label and have AnnotationHost matching the given
// hostname. The Label filter runs server-side via --where; the host
// filter is applied client-side because Annotations.X is not currently
// queryable in cub --where.
func (c *Client) ListLkSpacesForHost(ctx context.Context, hostname string) ([]LkSpace, error) {
	where := fmt.Sprintf("Labels.%s = 'true'", LabelLk)
	resp, err := c.api.ListSpacesWithResponse(ctx, &goclient.ListSpacesParams{Where: &where})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("list lk spaces: %s: %s", resp.Status(), string(resp.Body))
	}
	if resp.JSON200 == nil {
		return nil, nil
	}
	var out []LkSpace
	for _, ext := range *resp.JSON200 {
		if ext.Space == nil {
			continue
		}
		host := ext.Space.Annotations[AnnotationHost]
		if host != hostname {
			continue
		}
		name := ext.Space.Annotations[AnnotationClusterName]
		if name == "" {
			name = ext.Space.Slug
		}
		out = append(out, LkSpace{
			SpaceID:     ext.Space.SpaceID,
			SpaceSlug:   ext.Space.Slug,
			ClusterName: name,
			PortRange:   ext.Space.Annotations[AnnotationPortRange],
			Host:        host,
			CreatedAt:   ext.Space.CreatedAt,
		})
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
func (c *Client) CreateSpace(ctx context.Context, slug, displayName string, labels, annotations map[string]string) (uuid.UUID, error) {
	body := goclient.Space{
		Slug:        slug,
		DisplayName: displayName,
		Labels:      labels,
		Annotations: annotations,
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

// DeleteUnitBySlug deletes a unit by slug within a space.
func (c *Client) DeleteUnitBySlug(ctx context.Context, spaceID uuid.UUID, slug string) error {
	resp, err := c.api.ListUnitsWithResponse(ctx, spaceID, &goclient.ListUnitsParams{})
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return nil
	}
	for _, ext := range *resp.JSON200 {
		if ext.Unit != nil && ext.Unit.Slug == slug {
			r, err := c.api.DeleteUnitWithResponse(ctx, spaceID, ext.Unit.UnitID)
			if err != nil {
				return err
			}
			if r.StatusCode() != http.StatusOK && r.StatusCode() != http.StatusNoContent {
				return fmt.Errorf("delete unit %q: %s: %s", slug, r.Status(), string(r.Body))
			}
			return nil
		}
	}
	return nil
}

// DeleteTargetBySlug deletes a target by slug within a space.
func (c *Client) DeleteTargetBySlug(ctx context.Context, spaceID uuid.UUID, slug string) error {
	resp, err := c.api.ListTargetsWithResponse(ctx, spaceID, &goclient.ListTargetsParams{})
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return nil
	}
	for _, ext := range *resp.JSON200 {
		if ext.Target != nil && ext.Target.Slug == slug {
			r, err := c.api.DeleteTargetWithResponse(ctx, spaceID, ext.Target.TargetID)
			if err != nil {
				return err
			}
			if r.StatusCode() != http.StatusOK && r.StatusCode() != http.StatusNoContent {
				return fmt.Errorf("delete target %q: %s: %s", slug, r.Status(), string(r.Body))
			}
			return nil
		}
	}
	return nil
}

// DeleteBridgeWorkerBySlug deletes a worker by slug within a space.
func (c *Client) DeleteBridgeWorkerBySlug(ctx context.Context, spaceID uuid.UUID, slug string) error {
	resp, err := c.api.ListBridgeWorkersWithResponse(ctx, spaceID, &goclient.ListBridgeWorkersParams{})
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return nil
	}
	for _, ext := range *resp.JSON200 {
		if ext.BridgeWorker != nil && ext.BridgeWorker.Slug == slug {
			r, err := c.api.DeleteBridgeWorkerWithResponse(ctx, spaceID, ext.BridgeWorker.BridgeWorkerID)
			if err != nil {
				return err
			}
			if r.StatusCode() != http.StatusOK && r.StatusCode() != http.StatusNoContent {
				return fmt.Errorf("delete worker %q: %s: %s", slug, r.Status(), string(r.Body))
			}
			return nil
		}
	}
	return nil
}

// SpaceIDBySlug exposes the internal lookup so callers can chain
// per-entity deletes.
func (c *Client) SpaceIDBySlug(ctx context.Context, slug string) (uuid.UUID, error) {
	return c.spaceIDBySlug(ctx, slug)
}

// DeleteSpaceBySlug deletes a space by its slug. If recursive is true, the
// server cascades deletion to units, targets, and workers within the space
// (subject to delete gates).
func (c *Client) DeleteSpaceBySlug(ctx context.Context, slug string, recursive bool) error {
	id, err := c.spaceIDBySlug(ctx, slug)
	if err != nil {
		return err
	}
	params := &goclient.DeleteSpaceParams{}
	if recursive {
		t := "true"
		params.Recursive = &t
	}
	resp, err := c.api.DeleteSpaceWithResponse(ctx, id, params)
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

// CreateBridgeWorker creates a worker entity and pre-declares its supported
// ConfigTypes so we don't have to wait for the worker pod to connect before
// binding targets. Returns the worker's UUID.
//
// configTypes is a list of (ProviderType, ToolchainType) pairs, e.g.
// {{"Kubernetes", "Kubernetes/YAML"}}. LiveStateType is set to ToolchainType.
func (c *Client) CreateBridgeWorker(ctx context.Context, spaceID uuid.UUID, slug, displayName string, configTypes [][2]string) (uuid.UUID, error) {
	supported := make([]goclient.SupportedConfigType, 0, len(configTypes))
	for _, ct := range configTypes {
		supported = append(supported, goclient.SupportedConfigType{
			ProviderType:  ct[0],
			ToolchainType: ct[1],
			LiveStateType: ct[1],
		})
	}
	body := goclient.BridgeWorker{
		SpaceID:     spaceID,
		Slug:        slug,
		DisplayName: displayName,
		ProvidedInfo: &goclient.WorkerInfo{
			BridgeWorkerInfo: &goclient.BridgeWorkerInfo{
				SupportedConfigTypes: supported,
			},
		},
	}
	resp, err := c.api.CreateBridgeWorkerWithResponse(ctx, spaceID, &goclient.CreateBridgeWorkerParams{}, body)
	if err != nil {
		return uuid.Nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		return uuid.Nil, fmt.Errorf("create worker %q: %s: %s", slug, resp.Status(), string(resp.Body))
	}
	if resp.JSON200 == nil {
		return uuid.Nil, fmt.Errorf("create worker %q: empty response", slug)
	}
	return resp.JSON200.BridgeWorkerID, nil
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
