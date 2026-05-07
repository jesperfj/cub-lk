package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/jesperfj/cub-lk/internal/cubclient"
	"github.com/jesperfj/cub-lk/internal/kindcli"
	"github.com/jesperfj/cub-lk/internal/state"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List local kind clusters managed by lk",
		Long: `Lists clusters that are lk-managed on this host.

Sources of truth (no state file):
  - Per-cluster kubeconfig at $CUB_CONFIG/lk/<name>.kubeconfig (created by ` + "`lk up`" + `)
  - kind clusters present locally
  - ConfigHub Spaces in the current cub context with Labels.cub-lk=true and
    matching this host's annotation

The shown union is everything that has either a local kubeconfig file or a
matching Space — so a cluster registered with a different cub context still
shows up (with a status note). The STATUS column reports any drift.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			return runList(ctx, cmd.OutOrStdout())
		},
	}
}

// row is the merged view of a single lk-managed cluster.
type row struct {
	name         string
	kubeconfig   string // present if local kubeconfig file exists
	kindPresent  bool
	spaceSlug    string // present if matching Space found in current context
	portRange    string
	createdAt    time.Time
	matchingHost bool // Space's host annotation matches this machine
}

func runList(ctx context.Context, out io.Writer) error {
	hostname, _ := os.Hostname()

	// 1. Local kubeconfigs lk has created.
	localNames, err := state.LkClusterNames()
	if err != nil {
		return err
	}

	// 2. kind clusters on this host.
	kindNames, err := kindcli.ListClusters(ctx)
	if err != nil {
		return fmt.Errorf("list kind clusters: %w", err)
	}
	kindSet := map[string]bool{}
	for _, n := range kindNames {
		kindSet[n] = true
	}

	// 3. Spaces in current cub context with cub-lk label + matching host.
	var lkSpaces []cubclient.LkSpace
	client, clientErr := cubclient.New()
	if clientErr == nil {
		lkSpaces, err = client.ListLkSpacesForHost(ctx, hostname)
		if err != nil {
			fmt.Fprintf(out, "warning: could not list ConfigHub spaces: %v\n\n", err)
		}
	}

	// Union by cluster name.
	rows := map[string]*row{}
	for _, n := range localNames {
		rows[n] = &row{name: n, kubeconfig: state.KubeconfigPathFor(n), kindPresent: kindSet[n]}
	}
	for _, s := range lkSpaces {
		r, ok := rows[s.ClusterName]
		if !ok {
			r = &row{name: s.ClusterName, kindPresent: kindSet[s.ClusterName]}
			rows[s.ClusterName] = r
		}
		r.spaceSlug = s.SpaceSlug
		r.portRange = s.PortRange
		r.createdAt = s.CreatedAt
		r.matchingHost = true
	}

	if len(rows) == 0 {
		fmt.Fprintln(out, "No lk clusters tracked on this host.")
		return nil
	}

	// Stable order: by cluster name.
	names := make([]string, 0, len(rows))
	for n := range rows {
		names = append(names, n)
	}
	sortStrings(names)

	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSPACE\tPORTS\tKUBECONFIG\tSTATUS")
	for _, n := range names {
		r := rows[n]
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			r.name, dash(r.spaceSlug), dash(r.portRange), dash(r.kubeconfig), statusOf(r))
	}
	return tw.Flush()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func statusOf(r *row) string {
	switch {
	case r.kubeconfig != "" && r.kindPresent && r.spaceSlug != "":
		return "Ready"
	case r.kubeconfig != "" && r.kindPresent && r.spaceSlug == "":
		return "Local only (no Space in current context)"
	case r.kubeconfig != "" && !r.kindPresent && r.spaceSlug != "":
		return "Drift: kind cluster missing"
	case r.kubeconfig != "" && !r.kindPresent && r.spaceSlug == "":
		return "Stale kubeconfig (no kind cluster, no Space)"
	case r.kubeconfig == "" && r.kindPresent && r.spaceSlug != "":
		return "Stranded: kubeconfig missing"
	case r.kubeconfig == "" && !r.kindPresent && r.spaceSlug != "":
		return "ConfigHub only (no local cluster)"
	default:
		return "Unknown"
	}
}

// sortStrings sorts a slice of strings in place (no extra import).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
