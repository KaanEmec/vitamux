package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// Store keeps the owner's rule versions (J09.2): immutable versions per metric, at most one
// active version, and the built-ins as the fallback while a metric has none. Every mutation
// is audited with the actor and a diff of the spec.
type Store struct{ db *db.DB }

// NewStore returns a Store on d.
func NewStore(d *db.DB) *Store { return &Store{db: d} }

// By identifies who changes rules: the owner and the audit actor (audit.Owner, "api_key:<id>").
type By struct {
	UserID uuid.UUID
	Actor  string
}

// Version is one rule version: the owner's (Builtin false) or a built-in, which includes the
// default rule (Default true; default.go). Rule is the parsed spec; a stored version may fail
// Validate when the catalogue changed after it was saved.
type Version struct {
	Ref       string // builtin:<metric>:<n>, default:<metric>:<hash> or rule:<metric>:<n>
	Metric    string
	Version   int
	Builtin   bool
	Default   bool
	ID        uuid.UUID // uuid.Nil for built-ins
	Rule      *Rule
	Spec      json.RawMessage
	BasedOn   string // the built-in or default rule this version copied, if any
	Note      string
	CreatedBy string
	CreatedAt time.Time // zero for built-ins
	Active    bool
}

// RuleRef formats the reference of an owner's rule version.
func RuleRef(metric string, version int) string { return fmt.Sprintf("rule:%s:%d", metric, version) }

// Create validates spec and stores it as the metric's next version, and activates it when
// asked. The first version of a metric copies the built-in or default rule in effect as version
// 1, so the edit becomes version 2 and its diff shows the change from the default. Errors:
// *ValidationError for an invalid spec or (when activating) an invalid active set.
func (s *Store) Create(ctx context.Context, by By, spec []byte, note string, activate bool) (Version, error) {
	r, err := ParseRule(spec)
	if err != nil {
		return Version{}, err
	}
	var out Version
	err = s.db.Tx(ctx, func(q *dbq.Queries) error {
		latest, err := q.LatestRuleVersion(ctx, dbq.LatestRuleVersionParams{UserID: by.UserID, Metric: r.Metric})
		if err != nil {
			return err
		}
		var prev json.RawMessage
		if latest > 0 {
			row, err := q.GetRuleVersion(ctx, dbq.GetRuleVersionParams{UserID: by.UserID, Metric: r.Metric, Version: latest})
			if err != nil {
				return err
			}
			prev = row.Spec
		} else {
			set, err := activeSet(ctx, q, by.UserID)
			if err != nil {
				return err
			}
			if i := slices.IndexFunc(set, func(v Version) bool { return v.Metric == r.Metric }); i >= 0 {
				copied, err := s.insert(ctx, q, by, r.Metric, 1, set[i].Spec, set[i].Ref, "")
				if err != nil {
					return err
				}
				latest, prev = 1, copied.Spec
			}
		}
		row, err := s.insert(ctx, q, by, r.Metric, latest+1, spec, "", note)
		if err != nil {
			return err
		}
		if out, err = versionOf(row); err != nil {
			return err
		}
		diff, err := specDiff(prev, row.Spec)
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, q, audit.Event{UserID: &by.UserID, Actor: by.Actor, Action: "rule.create",
			TargetType: "resolution_rule", TargetID: row.ID.String(),
			Detail: map[string]any{"metric": r.Metric, "ref": out.Ref, "diff": diff}}); err != nil {
			return err
		}
		if activate {
			out, err = s.activate(ctx, q, by, out)
		}
		return err
	}, db.Serializable())
	return out, err
}

// insert stores one version and audits a built-in copy (basedOn set).
func (s *Store) insert(ctx context.Context, q *dbq.Queries, by By, metric string, version int32, spec []byte, basedOn, note string) (dbq.ResolutionRule, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return dbq.ResolutionRule{}, err
	}
	row, err := q.InsertRuleVersion(ctx, dbq.InsertRuleVersionParams{ID: id, UserID: by.UserID, Metric: metric,
		Version: version, Spec: spec, BasedOn: storeOptional(basedOn), Note: storeOptional(note), CreatedBy: by.Actor})
	if err != nil || basedOn == "" {
		return row, err
	}
	return row, audit.Record(ctx, q, audit.Event{UserID: &by.UserID, Actor: by.Actor, Action: "rule.copy_builtin",
		TargetType: "resolution_rule", TargetID: id.String(),
		Detail: map[string]any{"metric": metric, "ref": RuleRef(metric, int(version)), "based_on": basedOn}})
}

// Activate makes an existing version (an older one included) the metric's active rule.
// Errors: db.ErrNotFound for an unknown version; *ValidationError when the active set would
// gain a problem (e.g. a follower's window no longer matches its leader).
func (s *Store) Activate(ctx context.Context, by By, metric string, version int) (Version, error) {
	var out Version
	err := s.db.Tx(ctx, func(q *dbq.Queries) error {
		row, err := q.GetRuleVersion(ctx, dbq.GetRuleVersionParams{UserID: by.UserID, Metric: metric, Version: int32(version)}) //nolint:gosec // rule versions are small
		if err != nil {
			return err
		}
		if out, err = versionOf(row); err != nil {
			return err
		}
		out, err = s.activate(ctx, q, by, out)
		return err
	}, db.Serializable())
	return out, err
}

func (s *Store) activate(ctx context.Context, q *dbq.Queries, by By, target Version) (Version, error) {
	set, err := activeSet(ctx, q, by.UserID)
	if err != nil {
		return Version{}, err
	}
	before := make([]*Rule, len(set))
	idx := len(set)
	for i, v := range set {
		before[i] = v.Rule
		if v.Metric == target.Metric {
			idx = i
		}
	}
	var prev *Version
	after := append([]*Rule(nil), before...)
	if idx < len(set) {
		prev = &set[idx]
		if prev.Ref == target.Ref {
			target.Active = true
			return target, nil // already active
		}
		after[idx] = target.Rule
	} else {
		after = append(after, target.Rule)
	}
	// Reject only problems the change introduces: a stored rule that a later catalogue change
	// made invalid must not block activations of other metrics.
	if err := introduced(ValidateSet(before), ValidateSet(after)); err != nil {
		return Version{}, err
	}
	if err := q.SetActiveRule(ctx, dbq.SetActiveRuleParams{UserID: by.UserID, Metric: target.Metric,
		Version: int32(target.Version), ActivatedBy: by.Actor}); err != nil { //nolint:gosec // rule versions are small
		return Version{}, err
	}
	var from any
	var prevSpec json.RawMessage
	if prev != nil {
		from, prevSpec = prev.Ref, prev.Spec
	}
	diff, err := specDiff(prevSpec, target.Spec)
	if err != nil {
		return Version{}, err
	}
	target.Active = true
	return target, audit.Record(ctx, q, audit.Event{UserID: &by.UserID, Actor: by.Actor, Action: "rule.activate",
		TargetType: "resolution_rule", TargetID: target.ID.String(),
		Detail: map[string]any{"metric": target.Metric, "from": from, "to": target.Ref, "diff": diff}})
}

// introduced returns the field errors of after that before did not have, as a *ValidationError.
func introduced(before, after error) error {
	if after == nil {
		return nil
	}
	known := map[FieldError]bool{}
	var ve *ValidationError
	if errors.As(before, &ve) {
		for _, f := range ve.Errors {
			known[f] = true
		}
	}
	if !errors.As(after, &ve) {
		return after
	}
	var fresh []FieldError
	for _, f := range ve.Errors {
		if !known[f] {
			fresh = append(fresh, f)
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	return &ValidationError{Errors: fresh}
}

// Active returns the rule in effect for a metric: the owner's active version, else the
// built-in, else the default rule. db.ErrNotFound for a code outside the catalogue and families.
func (s *Store) Active(ctx context.Context, userID uuid.UUID, metric string) (Version, error) {
	set, err := s.ActiveSet(ctx, userID)
	if err != nil {
		return Version{}, err
	}
	for _, v := range set {
		if v.Metric == metric {
			return v, nil
		}
	}
	return Version{}, db.ErrNotFound
}

// ActiveSet returns the rule in effect for every metric that has one: built-in order first,
// with the owner's active versions in place of their built-ins, then the other catalogue codes
// in catalogue order (the owner's version, else the default rule), then the owner's other metrics.
func (s *Store) ActiveSet(ctx context.Context, userID uuid.UUID) ([]Version, error) {
	set, err := activeSet(ctx, s.db.Q(), userID)
	return set, db.MapErr(err)
}

func activeSet(ctx context.Context, q *dbq.Queries, userID uuid.UUID) ([]Version, error) {
	rows, err := q.ListActiveRules(ctx, userID)
	if err != nil {
		return nil, err
	}
	owned := map[string]Version{}
	var order []string
	for _, row := range rows {
		v, err := versionOf(row)
		if err != nil {
			return nil, err
		}
		v.Active = true
		owned[v.Metric] = v
		order = append(order, v.Metric)
	}
	var out []Version
	for _, b := range Builtins() {
		if v, ok := owned[b.Rule.Metric]; ok {
			out = append(out, v)
			delete(owned, b.Rule.Metric)
			continue
		}
		out = append(out, Version{Ref: b.Ref(), Metric: b.Rule.Metric, Version: b.Version, Builtin: true,
			Rule: &b.Rule, Spec: builtinSpec(b), Active: true})
	}
	priority, err := SourcePriority(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	for _, m := range catalog.Metrics() {
		if m.Unresolved || RuleMetric(m.Code) != m.Code || slices.ContainsFunc(out, func(v Version) bool { return v.Metric == m.Code }) {
			continue // a raw series without rules, a family member, or a built-in
		}
		if v, ok := owned[m.Code]; ok {
			out = append(out, v)
			delete(owned, m.Code)
			continue
		}
		var leader *Rule
		if f := defaultFollows[m.Code]; f != "" {
			if i := slices.IndexFunc(out, func(v Version) bool { return v.Metric == f }); i >= 0 {
				leader = out[i].Rule
			}
		}
		out = append(out, defaultRule(m, priority, leader))
	}
	for _, m := range order {
		if v, ok := owned[m]; ok {
			out = append(out, v)
		}
	}
	return out, nil
}

// History returns the owner's versions of a metric, newest first. A metric the owner never
// edited has none; its rule is the built-in or default rule (Active).
func (s *Store) History(ctx context.Context, userID uuid.UUID, metric string) ([]Version, error) {
	rows, err := s.db.Q().ListRuleVersions(ctx, dbq.ListRuleVersionsParams{UserID: userID, Metric: metric})
	if err != nil {
		return nil, db.MapErr(err)
	}
	out := make([]Version, len(rows))
	for i, row := range rows {
		if out[i], err = versionOf(row.ResolutionRule); err != nil {
			return nil, err
		}
		out[i].Active = row.Active
	}
	return out, nil
}

// Diff compares two of the owner's versions of a metric field by field (audit.Diff on the
// top-level spec fields). db.ErrNotFound when either version does not exist.
func (s *Store) Diff(ctx context.Context, userID uuid.UUID, metric string, from, to int) (map[string]any, error) {
	specs := make([]json.RawMessage, 2)
	for i, v := range []int{from, to} {
		row, err := s.db.Q().GetRuleVersion(ctx, dbq.GetRuleVersionParams{UserID: userID, Metric: metric, Version: int32(v)}) //nolint:gosec // rule versions are small
		if err != nil {
			return nil, db.MapErr(err)
		}
		specs[i] = row.Spec
	}
	return specDiff(specs[0], specs[1])
}

func versionOf(row dbq.ResolutionRule) (Version, error) {
	r, err := ParseRule(row.Spec)
	if r == nil { // undecodable; a validation error alone is tolerated (see Version)
		return Version{}, fmt.Errorf("resolve: stored rule %s: %w", RuleRef(row.Metric, int(row.Version)), err)
	}
	return Version{Ref: RuleRef(row.Metric, int(row.Version)), Metric: row.Metric, Version: int(row.Version),
		ID: row.ID, Rule: r, Spec: row.Spec, BasedOn: storeDeref(row.BasedOn), Note: storeDeref(row.Note),
		CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt}, nil
}

func builtinSpec(b Builtin) json.RawMessage {
	spec, err := json.Marshal(b.Rule)
	if err != nil {
		panic("resolve: built-in " + b.Ref() + " does not marshal: " + err.Error()) // covered by TestBuiltinsValid
	}
	return spec
}

// specDiff diffs the top-level fields of two specs; a nil spec is empty.
func specDiff(before, after json.RawMessage) (map[string]any, error) {
	var a, b map[string]any
	if before != nil {
		if err := json.Unmarshal(before, &a); err != nil {
			return nil, err
		}
	}
	if err := json.Unmarshal(after, &b); err != nil {
		return nil, err
	}
	return audit.Diff(a, b), nil
}

func storeOptional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func storeDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
