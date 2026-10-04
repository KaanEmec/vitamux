package resolve

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// The default rule (J24.1) covers every catalogue code with neither a built-in nor an owner
// rule: the owner's source order (SettingSourcePriority), then a generic device ladder. Its ref
// is default:<metric>:<hash of its spec>, so a new source order is a new ref and the resolved
// cache, keyed by ref, misses. The first edit copies it into the owner's version 1.

// SettingSourcePriority is the owner's source order: a JSON array of provider codes.
const SettingSourcePriority = "sources.priority"

// ErrInvalidPriority wraps the reason a source order was rejected.
var ErrInvalidPriority = errors.New("resolve: invalid source priority")

// defaultFollows are the leaders a default rule follows (E5), as body composition follows weight.
var defaultFollows = map[string]string{"basal_energy": "active_energy"}

// SourcePriority returns the owner's source order; none set is an empty order.
func SourcePriority(ctx context.Context, q *dbq.Queries, user uuid.UUID) ([]string, error) {
	v, err := q.GetUserSetting(ctx, dbq.GetUserSettingParams{UserID: user, Key: SettingSourcePriority})
	if errors.Is(db.MapErr(err), db.ErrNotFound) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	if err := json.Unmarshal(v, &out); err != nil {
		return nil, fmt.Errorf("resolve: setting %s: %w", SettingSourcePriority, err)
	}
	return out, nil
}

// SetSourcePriority stores the owner's source order and audits the change, inside the caller's
// transaction. Every code must be a known provider, at most once. Errors: ErrInvalidPriority.
func SetSourcePriority(ctx context.Context, q *dbq.Queries, user uuid.UUID, actor string, order []string) error {
	for i, code := range order {
		if slices.Index(order, code) != i {
			return fmt.Errorf("%w: %s is listed twice", ErrInvalidPriority, code)
		}
		ok, err := q.ProviderExists(ctx, code)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: unknown provider %q", ErrInvalidPriority, code)
		}
	}
	before, err := SourcePriority(ctx, q, user)
	if err != nil {
		return err
	}
	if order == nil {
		order = []string{}
	}
	b, err := json.Marshal(order)
	if err != nil {
		return err
	}
	if err := q.PutUserSetting(ctx, dbq.PutUserSettingParams{UserID: user, Key: SettingSourcePriority, Value: b}); err != nil {
		return err
	}
	return audit.Record(ctx, q, audit.Event{UserID: &user, Actor: actor, Action: "settings.update",
		TargetType: "setting", TargetID: SettingSourcePriority, Detail: map[string]any{"from": before, "to": order}})
}

// defaultRule synthesises the default rule of m from the owner's source order. leader is the
// rule in effect for m's follow leader, if it has one.
func defaultRule(m catalog.Metric, priority []string, leader *Rule) Version {
	r := Rule{Schema: SchemaV1, Metric: m.Code, Window: RuleWindow{Kind: catalog.WindowLocalDay},
		Strategy: Strategy{Op: OpFirstAvailable}}
	if !m.AllowsWindow(catalog.WindowLocalDay) {
		r.Window.Kind = catalog.WindowLocalNight
	}
	switch {
	case m.ProviderScoped: // <provider>_<name>: one source, nothing to order
		provider, _, _ := strings.Cut(m.Code, "_")
		r.Groups = biDirect(provider)
	case leader != nil:
		r.Follow, r.Window, r.Groups = defaultFollows[m.Code], leader.Window, leader.Groups
	default:
		r.Groups = defaultGroups(priority)
	}
	spec, err := json.Marshal(r)
	if err != nil {
		panic("resolve: default rule for " + m.Code + " does not marshal: " + err.Error()) // covered by TestDefaultRulesValid
	}
	sum := sha256.Sum256(spec)
	return Version{Ref: fmt.Sprintf("default:%s:%s", m.Code, hex.EncodeToString(sum[:4])), Metric: m.Code, Version: 1,
		Builtin: true, Default: true, Rule: &r, Spec: spec, Active: true}
}

// defaultGroups is the owner's providers in order (each with its Apple Health relay group), then
// the generic ladder. A provider group whose id is already taken is skipped, so the manual
// provider never moves the generic manual group up.
func defaultGroups(priority []string) []Group {
	generic := biLadder(biWorn("watch"), biWorn("band"), biWorn("ring"), biDevice("chest_strap"), biDevice("arm_band"),
		biDevice("phone"), biGroup("device", Selector{Entry: EntryDevice}), biManual())
	var out []Group
	for _, p := range priority {
		for _, g := range biBrand(p) {
			if !hasGroup(out, g.ID) && !hasGroup(generic, g.ID) {
				out = append(out, g)
			}
		}
	}
	return append(out, generic...)
}

func hasGroup(gs []Group, id string) bool {
	return slices.ContainsFunc(gs, func(g Group) bool { return g.ID == id })
}
