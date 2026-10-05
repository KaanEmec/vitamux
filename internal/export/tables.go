package export

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// refTable is seeded reference data. It is exported so the files are readable on their own
// (metric_id → code) and checked, not imported: the target must have the same rows.
type refTable struct {
	name string
	rows func(*dbq.Queries, context.Context) ([]json.RawMessage, error)
}

var refTables = []refTable{
	{"providers", (*dbq.Queries).ExportProviders},
	{"units", (*dbq.Queries).ExportUnits},
	{"metric_catalog", (*dbq.Queries).ExportMetricCatalog},
	{"analytes", (*dbq.Queries).ExportAnalytes},
}

// table is one exported table, listed in import (foreign key) order. A new table holding
// owner data must be added here, or exports silently miss it.
type table struct {
	name string
	seq  string // identity sequence; the importer shifts exported ids into a reserved range
	// export emits the rows in key order and returns the largest identity id.
	export func(*exporter) (maxID int64, err error)
	// patch rewrites one row for the target; false drops it.
	patch func(*importer, row) (keep bool, err error)
	// insert writes a JSON array of patched rows and returns how many were new.
	insert func(*importer, []byte) (int64, error)
	// link runs after the table, for superseded_by.
	link func(*importer) error
}

// uuidKeyed and intKeyed adapt a generated page query to paged.
func uuidKeyed[R any](e *exporter, fetch func(after uuid.UUID) ([]R, error), item func(R) (uuid.UUID, json.RawMessage)) (int64, error) {
	_, err := paged(fetch, item, e.emit)
	return 0, err
}

func intKeyed[R any](e *exporter, fetch func(after int64) ([]R, error), item func(R) (int64, json.RawMessage)) (int64, error) {
	return paged(fetch, item, e.emit)
}

func all(e *exporter, rows []json.RawMessage, err error) (int64, error) {
	for _, r := range rows {
		if err == nil {
			err = e.emit(r)
		}
	}
	return 0, err
}

func owned(im *importer, r row) (bool, error) { im.own(r); return true, nil }

// notImported drops every row of a table that is exported to read only; noInsert is then never reached.
func notImported(*importer, row) (bool, error)  { return false, nil }
func noInsert(*importer, []byte) (int64, error) { return 0, nil }

func execrows(f func(*dbq.Queries, context.Context, json.RawMessage) (int64, error)) func(*importer, []byte) (int64, error) {
	return func(im *importer, rows []byte) (int64, error) { return f(im.q, im.ctx, rows) }
}

var tables = []table{
	{
		name: "normalizer_versions", seq: "normalizer_versions_id_seq",
		export: func(e *exporter) (int64, error) {
			return intKeyed(e, func(after int64) ([]dbq.ExportNormalizerVersionsRow, error) {
				return e.q.ExportNormalizerVersions(e.ctx, dbq.ExportNormalizerVersionsParams{After: after, Lim: pageSize})
			}, func(r dbq.ExportNormalizerVersionsRow) (int64, json.RawMessage) { return r.ID, r.Row })
		},
		patch: func(im *importer, r row) (bool, error) { return true, im.shift(r, "normalizer_versions") },
		insert: func(im *importer, rows []byte) (n int64, err error) {
			res, err := im.q.ImportNormalizerVersions(im.ctx, rows)
			for _, x := range res {
				n += im.resolved(im.nvs, "normalizer_versions", x.SrcID, x.ID, x.Inserted)
			}
			return n, err
		},
	},
	{
		name: "timezone_periods",
		export: func(e *exporter) (int64, error) {
			return uuidKeyed(e, func(after uuid.UUID) ([]dbq.ExportTimezonePeriodsRow, error) {
				return e.q.ExportTimezonePeriods(e.ctx, dbq.ExportTimezonePeriodsParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportTimezonePeriodsRow) (uuid.UUID, json.RawMessage) { return r.ID, r.Row })
		},
		patch: owned, insert: execrows((*dbq.Queries).ImportTimezonePeriods),
	},
	{
		name: "settings",
		export: func(e *exporter) (int64, error) {
			rows, err := e.q.ExportSettings(e.ctx, e.user)
			return all(e, rows, err)
		},
		patch: owned, insert: execrows((*dbq.Queries).ImportSettings),
	},
	{
		name: "resolution_rules",
		export: func(e *exporter) (int64, error) {
			return uuidKeyed(e, func(after uuid.UUID) ([]dbq.ExportResolutionRulesRow, error) {
				return e.q.ExportResolutionRules(e.ctx, dbq.ExportResolutionRulesParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportResolutionRulesRow) (uuid.UUID, json.RawMessage) { return r.ID, r.Row })
		},
		patch: owned, insert: execrows((*dbq.Queries).ImportResolutionRules),
	},
	{
		name: "active_rules",
		export: func(e *exporter) (int64, error) {
			rows, err := e.q.ExportActiveRules(e.ctx, e.user)
			return all(e, rows, err)
		},
		patch: owned, insert: execrows((*dbq.Queries).ImportActiveRules),
	},
	{
		name: "connections",
		export: func(e *exporter) (int64, error) {
			return uuidKeyed(e, func(after uuid.UUID) ([]dbq.ExportConnectionsRow, error) {
				return e.q.ExportConnections(e.ctx, dbq.ExportConnectionsParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportConnectionsRow) (uuid.UUID, json.RawMessage) { return r.ID, r.Row })
		},
		patch: owned,
		insert: func(im *importer, rows []byte) (n int64, err error) {
			res, err := im.q.ImportConnections(im.ctx, rows)
			for _, x := range res {
				remapped(im.conns, x.SrcID, x.ID, x.Inserted, &n)
			}
			return n, err
		},
	},
	{
		name: "devices",
		export: func(e *exporter) (int64, error) {
			return uuidKeyed(e, func(after uuid.UUID) ([]dbq.ExportDevicesRow, error) {
				return e.q.ExportDevices(e.ctx, dbq.ExportDevicesParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportDevicesRow) (uuid.UUID, json.RawMessage) { return r.ID, r.Row })
		},
		// merged_into may point at a device of a later page, or one the target already has under
		// another id: it is cleared here and set by link.
		patch: func(im *importer, r row) (bool, error) {
			im.own(r)
			if !r.null("merged_into") {
				id, err1 := r.uuid("id")
				into, err2 := r.uuid("merged_into")
				if err := errors.Join(err1, err2); err != nil {
					return false, err
				}
				im.merges[id] = into
				r["merged_into"] = jsonNull
			}
			return true, nil
		},
		insert: func(im *importer, rows []byte) (n int64, err error) {
			res, err := im.q.ImportDevices(im.ctx, rows)
			for _, x := range res {
				remapped(im.devices, x.SrcID, x.ID, x.Inserted, &n)
				if !x.Inserted {
					delete(im.merges, x.SrcID) // the target's own device keeps its state
				}
			}
			return n, err
		},
		link: func(im *importer) error {
			ids, into := make([]uuid.UUID, 0, len(im.merges)), make([]uuid.UUID, 0, len(im.merges))
			for id, to := range im.merges {
				if n, ok := im.devices[to]; ok {
					to = n
				}
				ids, into = append(ids, id), append(into, to)
			}
			return im.q.LinkImportedDevices(im.ctx, dbq.LinkImportedDevicesParams{Ids: ids, IntoIds: into})
		},
	},
	{
		name: "data_origins",
		export: func(e *exporter) (int64, error) {
			return uuidKeyed(e, func(after uuid.UUID) ([]dbq.ExportDataOriginsRow, error) {
				return e.q.ExportDataOrigins(e.ctx, dbq.ExportDataOriginsParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportDataOriginsRow) (uuid.UUID, json.RawMessage) { return r.ID, r.Row })
		},
		patch: owned,
		insert: func(im *importer, rows []byte) (n int64, err error) {
			res, err := im.q.ImportDataOrigins(im.ctx, rows)
			for _, x := range res {
				remapped(im.origins, x.SrcID, x.ID, x.Inserted, &n)
			}
			return n, err
		},
	},
	{
		// Exported without token_hash: an imported client is revoked and must pair again.
		name: "clients",
		export: func(e *exporter) (int64, error) {
			return uuidKeyed(e, func(after uuid.UUID) ([]dbq.ExportClientsRow, error) {
				return e.q.ExportClients(e.ctx, dbq.ExportClientsParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportClientsRow) (uuid.UUID, json.RawMessage) { return r.ID, r.Row })
		},
		patch: func(im *importer, r row) (bool, error) {
			im.own(r)
			r["token_hash"] = json.RawMessage(`"\\x` + zeroHash + `"`) // matches no token
			if r.null("revoked_at") {
				r["revoked_at"] = im.now
			}
			return true, im.refUUID(r, "connection_id", im.conns)
		},
		insert: execrows((*dbq.Queries).ImportClients),
	},
	{
		name: "ingest_batches",
		export: func(e *exporter) (int64, error) {
			return uuidKeyed(e, func(after uuid.UUID) ([]dbq.ExportIngestBatchesRow, error) {
				return e.q.ExportIngestBatches(e.ctx, dbq.ExportIngestBatchesParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportIngestBatchesRow) (uuid.UUID, json.RawMessage) { return r.ID, r.Row })
		},
		patch: func(im *importer, r row) (bool, error) {
			im.own(r)
			return true, im.refUUID(r, "connection_id", im.conns)
		},
		insert: func(im *importer, rows []byte) (n int64, err error) {
			res, err := im.q.ImportIngestBatches(im.ctx, rows)
			for _, x := range res {
				remapped(im.batches, x.SrcID, x.ID, x.Inserted, &n)
			}
			return n, err
		},
	},
	{
		name: "raw_payloads", seq: "raw_payloads_id_seq",
		export: func(e *exporter) (int64, error) {
			return intKeyed(e, func(after int64) ([]dbq.ExportRawPayloadsRow, error) {
				return e.q.ExportRawPayloads(e.ctx, dbq.ExportRawPayloadsParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportRawPayloadsRow) (int64, json.RawMessage) { return r.ID, r.Row })
		},
		patch: func(im *importer, r row) (bool, error) {
			im.own(r)
			return true, errors.Join(im.takeBlob(r), im.shift(r, "raw_payloads"),
				im.refUUID(r, "connection_id", im.conns), im.refUUID(r, "batch_id", im.batches),
				im.ref(r, "supersedes_id", "raw_payloads", im.raws),
				im.ref(r, "normalizer_version_id", "normalizer_versions", im.nvs))
		},
		insert: func(im *importer, rows []byte) (n int64, err error) {
			if err := im.flushBlobs(); err != nil {
				return 0, err
			}
			res, err := im.q.ImportRawPayloads(im.ctx, rows)
			for _, x := range res {
				n += im.resolved(im.raws, "raw_payloads", x.SrcID, x.ID, x.Inserted)
			}
			return n, err
		},
	},
	{
		name: "import_runs",
		export: func(e *exporter) (int64, error) {
			return uuidKeyed(e, func(after uuid.UUID) ([]dbq.ExportImportRunsRow, error) {
				return e.q.ExportImportRuns(e.ctx, dbq.ExportImportRunsParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportImportRunsRow) (uuid.UUID, json.RawMessage) { return r.ID, r.Row })
		},
		patch: func(im *importer, r row) (bool, error) {
			im.own(r)
			return true, im.refUUID(r, "connection_id", im.conns)
		},
		insert: execrows((*dbq.Queries).ImportImportRuns),
	},
	{
		name: "import_items", seq: "import_items_id_seq",
		export: func(e *exporter) (int64, error) {
			return intKeyed(e, func(after int64) ([]dbq.ExportImportItemsRow, error) {
				return e.q.ExportImportItems(e.ctx, dbq.ExportImportItemsParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportImportItemsRow) (int64, json.RawMessage) { return r.ID, r.Row })
		},
		patch: func(im *importer, r row) (bool, error) {
			return true, errors.Join(im.shift(r, "import_items"), im.ref(r, "raw_payload_id", "raw_payloads", im.raws))
		},
		insert: execrows((*dbq.Queries).ImportImportItems),
	},
	{
		name: "measurement_groups", seq: "measurement_groups_id_seq",
		export: func(e *exporter) (int64, error) {
			return intKeyed(e, func(after int64) ([]dbq.ExportMeasurementGroupsRow, error) {
				return e.q.ExportMeasurementGroups(e.ctx, dbq.ExportMeasurementGroupsParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportMeasurementGroupsRow) (int64, json.RawMessage) { return r.ID, r.Row })
		},
		patch: func(im *importer, r row) (bool, error) {
			return true, errors.Join(im.shift(r, "measurement_groups"), im.canonical(r))
		},
		insert: func(im *importer, rows []byte) (n int64, err error) {
			res, err := im.q.ImportMeasurementGroups(im.ctx, rows)
			for _, x := range res {
				n += im.resolved(im.groups, "measurement_groups", x.SrcID, x.ID, x.Inserted)
			}
			return n, err
		},
		link: func(im *importer) error {
			return im.linkInt("measurement_groups", func(ctx context.Context, ids, by []int64) error {
				return im.q.LinkImportedMeasurementGroups(ctx, dbq.LinkImportedMeasurementGroupsParams{Ids: ids, NewIds: by})
			})
		},
	},
	{
		name: "measurements", seq: "measurements_id_seq",
		export: func(e *exporter) (int64, error) {
			return intKeyed(e, func(after int64) ([]dbq.ExportMeasurementsRow, error) {
				return e.q.ExportMeasurements(e.ctx, dbq.ExportMeasurementsParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportMeasurementsRow) (int64, json.RawMessage) { return r.ID, r.Row })
		},
		patch: func(im *importer, r row) (bool, error) {
			return true, errors.Join(im.shift(r, "measurements"), im.canonical(r),
				im.ref(r, "group_id", "measurement_groups", im.groups))
		},
		insert: execrows((*dbq.Queries).ImportMeasurements),
		link: func(im *importer) error {
			return im.linkInt("measurements", func(ctx context.Context, ids, by []int64) error {
				return im.q.LinkImportedMeasurements(ctx, dbq.LinkImportedMeasurementsParams{Ids: ids, NewIds: by})
			})
		},
	},
	{
		name: "sleep_sessions",
		export: func(e *exporter) (int64, error) {
			return uuidKeyed(e, func(after uuid.UUID) ([]dbq.ExportSleepSessionsRow, error) {
				return e.q.ExportSleepSessions(e.ctx, dbq.ExportSleepSessionsParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportSleepSessionsRow) (uuid.UUID, json.RawMessage) { return r.ID, r.Row })
		},
		patch: func(im *importer, r row) (bool, error) { return true, im.canonical(r) },
		insert: func(im *importer, rows []byte) (int64, error) {
			ids, err := im.q.ImportSleepSessions(im.ctx, rows)
			for _, id := range ids {
				im.sessions[id] = true
			}
			return int64(len(ids)), err
		},
		link: func(im *importer) error {
			return im.linkUUID("sleep_sessions", func(ctx context.Context, ids, by []uuid.UUID) error {
				return im.q.LinkImportedSleepSessions(ctx, dbq.LinkImportedSleepSessionsParams{Ids: ids, NewIds: by})
			})
		},
	},
	{
		name: "sleep_stages", seq: "sleep_stages_id_seq",
		export: func(e *exporter) (int64, error) {
			return intKeyed(e, func(after int64) ([]dbq.ExportSleepStagesRow, error) {
				return e.q.ExportSleepStages(e.ctx, dbq.ExportSleepStagesParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportSleepStagesRow) (int64, json.RawMessage) { return r.ID, r.Row })
		},
		patch: func(im *importer, r row) (bool, error) {
			s, err := r.uuid("session_id")
			if err != nil || !im.sessions[s] {
				return false, err
			}
			return true, im.shift(r, "sleep_stages")
		},
		insert: execrows((*dbq.Queries).ImportSleepStages),
	},
	{
		name: "workouts",
		export: func(e *exporter) (int64, error) {
			return uuidKeyed(e, func(after uuid.UUID) ([]dbq.ExportWorkoutsRow, error) {
				return e.q.ExportWorkouts(e.ctx, dbq.ExportWorkoutsParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportWorkoutsRow) (uuid.UUID, json.RawMessage) { return r.ID, r.Row })
		},
		patch: func(im *importer, r row) (bool, error) { return true, errors.Join(im.takeBlob(r), im.canonical(r)) },
		insert: func(im *importer, rows []byte) (int64, error) {
			if err := im.flushBlobs(); err != nil {
				return 0, err
			}
			ids, err := im.q.ImportWorkouts(im.ctx, rows)
			for _, id := range ids {
				im.workouts[id] = true
			}
			return int64(len(ids)), err
		},
		link: func(im *importer) error {
			return im.linkUUID("workouts", func(ctx context.Context, ids, by []uuid.UUID) error {
				return im.q.LinkImportedWorkouts(ctx, dbq.LinkImportedWorkoutsParams{Ids: ids, NewIds: by})
			})
		},
	},
	{
		name: "workout_segments",
		export: func(e *exporter) (int64, error) {
			type key struct {
				w   uuid.UUID
				seq int32
			}
			_, err := paged(func(after key) ([]dbq.ExportWorkoutSegmentsRow, error) {
				return e.q.ExportWorkoutSegments(e.ctx, dbq.ExportWorkoutSegmentsParams{UserID: e.user, AfterWorkout: after.w, AfterSeq: after.seq, Lim: pageSize})
			}, func(r dbq.ExportWorkoutSegmentsRow) (key, json.RawMessage) { return key{r.WorkoutID, r.Seq}, r.Row }, e.emit)
			return 0, err
		},
		patch: func(im *importer, r row) (bool, error) {
			w, err := r.uuid("workout_id")
			return err == nil && im.workouts[w], err
		},
		insert: execrows((*dbq.Queries).ImportWorkoutSegments),
	},
	{
		name: "health_events",
		export: func(e *exporter) (int64, error) {
			return uuidKeyed(e, func(after uuid.UUID) ([]dbq.ExportHealthEventsRow, error) {
				return e.q.ExportHealthEvents(e.ctx, dbq.ExportHealthEventsParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportHealthEventsRow) (uuid.UUID, json.RawMessage) { return r.ID, r.Row })
		},
		patch: func(im *importer, r row) (bool, error) { return true, errors.Join(im.takeBlob(r), im.canonical(r)) },
		insert: func(im *importer, rows []byte) (int64, error) {
			if err := im.flushBlobs(); err != nil {
				return 0, err
			}
			return im.q.ImportHealthEvents(im.ctx, rows)
		},
		link: func(im *importer) error {
			return im.linkUUID("health_events", func(ctx context.Context, ids, by []uuid.UUID) error {
				return im.q.LinkImportedHealthEvents(ctx, dbq.LinkImportedHealthEventsParams{Ids: ids, NewIds: by})
			})
		},
	},
	{
		// input_id is a measurements.id without a foreign key; an override whose row a merge
		// skipped is then reported as ignored, as after any correction.
		name: "manual_overrides",
		export: func(e *exporter) (int64, error) {
			return uuidKeyed(e, func(after uuid.UUID) ([]dbq.ExportManualOverridesRow, error) {
				return e.q.ExportManualOverrides(e.ctx, dbq.ExportManualOverridesParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportManualOverridesRow) (uuid.UUID, json.RawMessage) { return r.ID, r.Row })
		},
		patch: func(im *importer, r row) (bool, error) {
			im.own(r)
			return true, im.ref(r, "input_id", "measurements", nil)
		},
		insert: execrows((*dbq.Queries).ImportManualOverrides),
	},
	{
		// Lab documents (E12): metadata only. The PDF, its filename and the document key are
		// never exported, so imported documents are tombstones whose confirmed results stay, as
		// after a delete with derived=keep (docs/architecture/lab-documents.md#storage).
		name: "documents",
		export: func(e *exporter) (int64, error) {
			return uuidKeyed(e, func(after uuid.UUID) ([]dbq.ExportDocumentsRow, error) {
				return e.q.ExportDocuments(e.ctx, dbq.ExportDocumentsParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportDocumentsRow) (uuid.UUID, json.RawMessage) { return r.ID, r.Row })
		},
		patch: func(im *importer, r row) (bool, error) {
			im.own(r)
			r["status"], r["retention_until"] = json.RawMessage(`"deleted"`), jsonNull
			if r.null("deleted_at") {
				r["deleted_at"] = im.now
			}
			return true, nil
		},
		insert: execrows((*dbq.Queries).ImportDocuments),
	},
	{
		// Runs, rows and their review trail (without the sealed raw response) are exported to
		// read but not imported: a tombstoned document keeps no runs.
		name: "extraction_runs",
		export: func(e *exporter) (int64, error) {
			return uuidKeyed(e, func(after uuid.UUID) ([]dbq.ExportExtractionRunsRow, error) {
				return e.q.ExportExtractionRuns(e.ctx, dbq.ExportExtractionRunsParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportExtractionRunsRow) (uuid.UUID, json.RawMessage) { return r.ID, r.Row })
		},
		patch: notImported, insert: noInsert,
	},
	{
		name: "lab_extracted_rows",
		export: func(e *exporter) (int64, error) {
			return intKeyed(e, func(after int64) ([]dbq.ExportLabExtractedRowsRow, error) {
				return e.q.ExportLabExtractedRows(e.ctx, dbq.ExportLabExtractedRowsParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportLabExtractedRowsRow) (int64, json.RawMessage) { return r.ID, r.Row })
		},
		patch: notImported, insert: noInsert,
	},
	{
		name: "extraction_row_edits",
		export: func(e *exporter) (int64, error) {
			return intKeyed(e, func(after int64) ([]dbq.ExportExtractionRowEditsRow, error) {
				return e.q.ExportExtractionRowEdits(e.ctx, dbq.ExportExtractionRowEditsParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportExtractionRowEditsRow) (int64, json.RawMessage) { return r.ID, r.Row })
		},
		patch: notImported, insert: noInsert,
	},
	{
		name: "lab_reports",
		export: func(e *exporter) (int64, error) {
			return uuidKeyed(e, func(after uuid.UUID) ([]dbq.ExportLabReportsRow, error) {
				return e.q.ExportLabReports(e.ctx, dbq.ExportLabReportsParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportLabReportsRow) (uuid.UUID, json.RawMessage) { return r.ID, r.Row })
		},
		patch:  func(im *importer, r row) (bool, error) { im.own(r); r["run_id"] = jsonNull; return true, nil },
		insert: execrows((*dbq.Queries).ImportLabReports),
	},
	{
		name: "lab_results",
		export: func(e *exporter) (int64, error) {
			return uuidKeyed(e, func(after uuid.UUID) ([]dbq.ExportLabResultsRow, error) {
				return e.q.ExportLabResults(e.ctx, dbq.ExportLabResultsParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportLabResultsRow) (uuid.UUID, json.RawMessage) { return r.ID, r.Row })
		},
		patch:  func(im *importer, r row) (bool, error) { im.own(r); r["source_row_id"] = jsonNull; return true, nil },
		insert: execrows((*dbq.Queries).ImportLabResults),
	},
	{
		name: "lab_result_revisions",
		export: func(e *exporter) (int64, error) {
			return intKeyed(e, func(after int64) ([]dbq.ExportLabResultRevisionsRow, error) {
				return e.q.ExportLabResultRevisions(e.ctx, dbq.ExportLabResultRevisionsParams{UserID: e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportLabResultRevisionsRow) (int64, json.RawMessage) { return r.ID, r.Row })
		},
		patch:  func(*importer, row) (bool, error) { return true, nil },
		insert: execrows((*dbq.Queries).ImportLabResultRevisions),
	},
	{
		// The owner's aliases; analyte ids are checked through the analytes reference file.
		// Revisions and aliases are inserted with new ids (see labexport.sql).
		name: "analyte_aliases",
		export: func(e *exporter) (int64, error) {
			return intKeyed(e, func(after int64) ([]dbq.ExportAnalyteAliasesRow, error) {
				return e.q.ExportAnalyteAliases(e.ctx, dbq.ExportAnalyteAliasesParams{UserID: &e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportAnalyteAliasesRow) (int64, json.RawMessage) { return r.ID, r.Row })
		},
		patch:  owned,
		insert: execrows((*dbq.Queries).ImportAnalyteAliases),
	},
	{
		// Imported into an empty instance only (see importer.run).
		name: "audit_events", seq: "audit_events_id_seq",
		export: func(e *exporter) (int64, error) {
			return intKeyed(e, func(after int64) ([]dbq.ExportAuditEventsRow, error) {
				return e.q.ExportAuditEvents(e.ctx, dbq.ExportAuditEventsParams{UserID: &e.user, After: after, Lim: pageSize})
			}, func(r dbq.ExportAuditEventsRow) (int64, json.RawMessage) { return r.ID, r.Row })
		},
		patch:  func(im *importer, r row) (bool, error) { im.own(r); return true, im.shift(r, "audit_events") },
		insert: execrows((*dbq.Queries).ImportAuditEvents),
	},
}

// withoutFile names every other table and why it has no file of its own; a test fails for
// a table that is in neither list.
var withoutFile = map[string]string{ //nolint:unused,nolintlint // read by TestEveryTableClassified (integration build only)
	"blobs":               "carried as _blob in the referencing rows, content in blob_content.ndjson",
	"goose_db_version":    "migration state",
	"users":               "the target keeps its own owner account",
	"sessions":            "login state",
	"api_keys":            "secrets",
	"recovery_codes":      "secrets",
	"credentials":         "secrets: connections are exported without them and need re-authorization",
	"oauth_states":        "short-lived login state",
	"pairing_codes":       "short-lived pairing state",
	"idempotency_keys":    "per-client request replay state",
	"known_relay_origins": "seeded defaults",
	"schedules":           "operational: recreated from connector descriptors",
	"jobs":                "operational",
	"job_runs":            "operational",
	"sync_cursors":        "operational: the next sync starts over and dedupes",
	"backfills":           "operational",
	"backfill_units":      "operational",
	"provider_rate_state": "operational",
	"resolution_dirty":    "derived: the importer marks imported days",
	"exports":             "the exports themselves",
	"document_keys":       "secrets: lab PDFs, filenames and raw extractor responses stay sealed and are never exported",
	// Source setup (ADR-0021): instance setup with sealed secrets, entered again on the target.
	"provider_app_credentials": "secrets: the owner's provider app, set up again in the panel",
	"sidecars":                 "secrets: sidecars added in the panel, added again on the target",
	// Rebuildable resolution state (J09.9).
	"resolved_cache": "derived: recomputed on read", "source_hourly_aggregates": "derived: rebuilt from resolution_dirty",
}

const zeroHash = "0000000000000000000000000000000000000000000000000000000000000000"
