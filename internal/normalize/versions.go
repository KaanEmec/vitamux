package normalize

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/version"
)

// RegisterVersions records every normalizer of r in normalizer_versions under the running build's
// git sha and returns the row id per normalizer ID, for Source.NormalizerVersionID. It is
// idempotent: the same (name, version, sha) keeps its row.
func RegisterVersions(ctx context.Context, q *dbq.Queries, r *Registry) (map[string]int32, error) {
	sha := gitSHA()
	ids := make(map[string]int32, len(r.list))
	for _, n := range r.list {
		id, err := upsert(func() (int32, error) {
			return q.RegisterNormalizerVersion(ctx, dbq.RegisterNormalizerVersionParams{
				Name: n.ID(), Version: int32(n.Version()), GitSha: sha}) //nolint:gosec // versions are small
		})
		if err != nil {
			return nil, fmt.Errorf("normalize: register normalizer version %s: %w", n.ID(), err)
		}
		ids[n.ID()] = id
	}
	return ids, nil
}

// gitSHA is the commit injected at link time, else the VCS stamp of a plain `go build`.
func gitSHA() string {
	if version.Commit != "unknown" && version.Commit != "" {
		return version.Commit
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		rev, dirty := "", false
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		if rev != "" && dirty {
			return rev + "-dirty"
		}
		if rev != "" {
			return rev
		}
	}
	return "unknown"
}
