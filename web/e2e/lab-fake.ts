// A stateful stand-in for the document, extraction, review and lab-result endpoints used by
// the Lab results section (J12.6), on top of fake-api.ts. It mirrors internal/api/documents.go,
// extractions.go and review.go: non-PDF uploads are 422 not_pdf, external extractors need
// consent naming the configured model, a run succeeds on the first poll after it is queued,
// row patches follow the review statuses, and confirm lists unreviewed rows (422). All values
// and the PDF are synthetic.
import type { Page, Route } from '@playwright/test';
import { test as base, expect } from './fake-api';

type Json = Record<string, unknown>;
type Row = Json & { index: number; review_status: string; validation: string[]; edits: Json[]; lab_result_id: string | null };
interface Run extends Json {
	id: string;
	document_id: string;
	status: string;
	rows: Row[];
}

export const geminiModel = 'gemini-synthetic-model';
const at = '2026-10-01T09:00:00Z';
const hex = (n: number) => n.toString(16).padStart(32, '0');

/** A one-page PDF with a Helvetica text layer: the printed lines of the synthetic report. */
export function syntheticPdf(): Buffer {
	const lines = [
		'SYNTHETIC LAB - not a real report',
		'Glucose 5.1 mmol/L 3.9 - 5.5',
		'Creatinine 112 H umol/L 60 - 110',
		'####### smudged line',
		'HbA1c 39 mmol/mol 20 - 42'
	];
	const text = `BT /F1 12 Tf 72 740 Td ${lines.map((l, i) => `${i ? '0 -20 Td ' : ''}(${l}) Tj`).join(' ')} ET`;
	const objects = [
		'<< /Type /Catalog /Pages 2 0 R >>',
		'<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
		'<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>',
		`<< /Length ${text.length} >>\nstream\n${text}\nendstream`,
		'<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>'
	];
	let out = '%PDF-1.4\n';
	const offsets = objects.map((o, i) => {
		const off = out.length;
		out += `${i + 1} 0 obj\n${o}\nendobj\n`;
		return off;
	});
	const xref = out.length;
	out += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n${offsets.map((o) => `${String(o).padStart(10, '0')} 00000 n \n`).join('')}`;
	out += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
	return Buffer.from(out, 'latin1');
}

/** Row area of printed line n (0 = header) in page fractions, origin top left. */
const lineBox = (n: number) => ({ x0: 0.11, y0: (792 - 740 - 12 + 20 * n) / 792, x1: 0.6, y1: (792 - 740 + 5 + 20 * n) / 792 });

function extractedRows(): Row[] {
	const row = (index: number, r: Json): Row => ({
		index, page: 1, value_numeric: null, comparator: null, ref_low: null, ref_high: null, printed_flag: null,
		specimen_type: 'Serum', collected_at: '2026-09-28', reported_at: '2026-09-29', laboratory: 'Synthetic Lab',
		confidence: 0.9, review_status: 'pending', reviewed_at: null, warnings: [], validation: [], edits: [],
		lab_result_id: null, bbox: lineBox(index + 1), ...r
	}) as Row;
	return [
		row(0, { analyte_label: 'Glucose', value_text: '5.1', value_numeric: 5.1, unit_text: 'mmol/L', reference_range_text: '3.9 - 5.5', ref_low: 3.9, ref_high: 5.5, evidence_text: 'Glucose 5.1 mmol/L 3.9 - 5.5', analyte: 'glucose', suggested_analyte: 'glucose' }),
		row(1, { analyte_label: 'Creatinine', value_text: '112', value_numeric: 112, unit_text: 'umol/L', reference_range_text: '60 - 110', ref_low: 60, ref_high: 110, printed_flag: 'H', evidence_text: 'Creatinine 112 H umol/L 60 - 110', analyte: 'creatinine', suggested_analyte: 'creatinine' }),
		row(2, { analyte_label: '#######', value_text: null, unit_text: null, reference_range_text: null, evidence_text: '####### smudged line', analyte: null, suggested_analyte: null, warnings: ['unreadable_value'], validation: ['unit_missing', 'unknown_analyte'] }),
		row(3, { analyte_label: 'HbA1c', value_text: '39', value_numeric: 39, unit_text: 'mmol/mol', reference_range_text: '20 - 42', ref_low: 20, ref_high: 42, collected_at: null, evidence_text: 'HbA1c 39 mmol/mol 20 - 42', analyte: 'hba1c', suggested_analyte: 'hba1c', validation: ['date_missing'] })
	];
}

const fields = ['analyte_label', 'value_text', 'value_numeric', 'comparator', 'unit_text', 'reference_range_text', 'ref_low', 'ref_high',
	'printed_flag', 'specimen_type', 'collected_at', 'reported_at', 'laboratory', 'analyte'];

export class LabApi {
	docs: Json[] = [];
	runs: Run[] = [];
	results: Json[] = [];
	revisions: Record<string, Json[]> = {};
	extractors = [
		{ id: 'fake', external: false, model: null, enabled: true },
		{ id: 'gemini', external: true, model: geminiModel, enabled: true },
		{ id: 'openai', external: true, model: 'openai-synthetic-model', enabled: false }
	];
	/** Request bodies, for assertions. */
	extractionBodies: Json[] = [];
	rowPatches: Json[] = [];
	deletes: string[] = [];
	private next = 1;

	constructor(private page: Page) {}

	async install() {
		await this.page.route('**/api/v1/**', (r) => this.dispatch(r));
	}

	private dispatch(r: Route) {
		const url = new URL(r.request().url());
		const path = url.pathname.replace('/api/v1', '');
		const method = r.request().method();
		let m: RegExpMatchArray | null;

		if (path === '/documents' && method === 'GET') return json(r, 200, { documents: this.docs, has_more: false });
		if (path === '/documents' && method === 'POST') return this.upload(r);
		if ((m = path.match(/^\/documents\/(doc_\w+)$/))) {
			const doc = this.docs.find((d) => d.id === m![1]);
			if (!doc) return problem(r, 404, 'not_found', 'no such document');
			if (method === 'DELETE') return this.remove(r, doc, url.searchParams.get('derived') ?? '');
			return json(r, 200, doc);
		}
		if ((m = path.match(/^\/documents\/(doc_\w+)\/file$/))) {
			return r.fulfill({ status: 200, contentType: 'application/pdf', body: syntheticPdf() });
		}
		if ((m = path.match(/^\/documents\/(doc_\w+)\/extractions$/))) {
			const doc = this.docs.find((d) => d.id === m![1]);
			if (!doc) return problem(r, 404, 'not_found', 'no such document');
			if (method === 'POST') return this.start(r, doc);
			return json(r, 200, { extractions: this.runsOf(doc).map((x) => this.withoutRows(this.poll(x))) });
		}
		if (path === '/extractors') return json(r, 200, { extractors: this.extractors });
		if ((m = path.match(/^\/extractions\/(ext_\w+)$/))) {
			const run = this.runs.find((x) => x.id === m![1]);
			return run ? json(r, 200, this.withRows(run)) : problem(r, 404, 'not_found', 'no such extraction');
		}
		if ((m = path.match(/^\/extractions\/(ext_\w+)\/rows\/(\d+)$/))) return this.patch(r, m[1], Number(m[2]));
		if ((m = path.match(/^\/extractions\/(ext_\w+)\/confirm$/))) return this.confirm(r, m[1]);
		if ((m = path.match(/^\/extractions\/(ext_\w+)\/unconfirm$/))) return this.unconfirm(r, m[1]);
		if (path === '/lab-results') return json(r, 200, { lab_results: this.results, has_more: false });
		if ((m = path.match(/^\/lab-results\/(lab_\w+)\/history$/))) {
			const cur = this.results.find((x) => x.id === m![1]);
			return cur ? json(r, 200, { revisions: [cur, ...(this.revisions[m[1]] ?? [])] }) : problem(r, 404, 'not_found', 'no such result');
		}
		if (path === '/analytes/aliases') {
			return json(r, 200, {
				aliases: ['glucose', 'creatinine', 'hba1c', 'sodium'].map((a, i) => ({ id: String(i + 1), label: a, analyte: a, source: 'seed', created_at: at }))
			});
		}
		return r.fallback();
	}

	private upload(r: Route) {
		const body = r.request().postDataBuffer() ?? Buffer.alloc(0);
		const filename = /filename="([^"]+)"/.exec(body.toString('latin1'))?.[1] ?? null;
		if (!body.includes('%PDF-')) {
			return problem(r, 422, 'validation_failed', 'the upload is not an acceptable PDF', [{ pointer: '/file', detail: 'not_pdf' }]);
		}
		const doc = {
			id: `doc_${hex(this.next++)}`, status: 'uploaded', sha256: 'ab'.repeat(32), filename, size_bytes: body.length, page_count: 1,
			uploaded_at: new Date(Date.parse(at) + this.next * 60_000).toISOString(), retention_until: null, deleted_at: null
		};
		this.docs.push(doc);
		return json(r, 201, doc);
	}

	private remove(r: Route, doc: Json, derived: string) {
		if (derived !== 'keep' && derived !== 'delete') return problem(r, 422, 'validation_failed', 'derived must be keep or delete');
		this.deletes.push(derived);
		Object.assign(doc, { status: 'deleted', sha256: null, filename: null, deleted_at: at, retention_until: null });
		this.runs = this.runs.filter((x) => x.document_id !== doc.id);
		if (derived === 'delete') this.results = this.results.filter((x) => (x.provenance as Json).document_id !== doc.id);
		else for (const x of this.results) if ((x.provenance as Json).document_id === doc.id) (x.provenance as Json).extraction_id = null;
		return r.fulfill({ status: 204 });
	}

	private runsOf(doc: Json) {
		return this.runs.filter((x) => x.document_id === doc.id).reverse();
	}

	private start(r: Route, doc: Json) {
		const body = r.request().postDataJSON() as Json;
		this.extractionBodies.push(body);
		const ex = this.extractors.find((e) => e.id === body.provider);
		if (!ex) return problem(r, 403, 'forbidden', `${body.provider} is not configured on this server`);
		if (!ex.enabled) return problem(r, 403, 'forbidden', `${ex.id} is disabled; enable it in settings`);
		const consent = body.consent as Json | null;
		if (ex.external && (!consent || consent.provider !== ex.id || consent.model !== ex.model)) {
			return problem(r, 409, 'consent_required', `consent must name provider ${ex.id} and model ${ex.model}`);
		}
		const run: Run = {
			id: `ext_${hex(this.next++)}`, document_id: doc.id as string, status: 'queued', provider: ex.id, model: ex.model,
			external: ex.external, consent: ex.external ? consent : null, schema_version: 'vitamux.lab.extraction/1',
			prompt_version: 'lab-extraction/v1', provider_request_id: null, document: {}, usage: {}, warnings: [], error_class: null,
			row_count: 0, created_by: 'owner', created_at: at, started_at: null, finished_at: null, rows: []
		};
		this.runs.push(run);
		doc.status = 'extracting';
		return json(r, 202, this.withoutRows(run));
	}

	/** A queued run finishes on the first poll: rows stored, document waiting for review. */
	private poll(run: Run) {
		if (run.status === 'queued') {
			Object.assign(run, { status: 'succeeded', rows: extractedRows(), row_count: 4, started_at: at, finished_at: at });
			const doc = this.docs.find((d) => d.id === run.document_id);
			if (doc) doc.status = 'needs_review';
		}
		return run;
	}

	private withoutRows(run: Run): Json {
		return Object.fromEntries(Object.entries(run).filter(([k]) => k !== 'rows'));
	}

	private withRows(run: Run) {
		return { ...run, rows: run.rows.map((x) => ({ ...x, validation: this.validate(x) })) };
	}

	private validate(row: Row): string[] {
		const out = row.validation.filter((v) => v !== 'unit_missing' && v !== 'date_missing');
		if (row.unit_text === null) out.push('unit_missing');
		if (row.collected_at === null) out.push('date_missing');
		return out;
	}

	private patch(r: Route, runId: string, index: number) {
		const run = this.runs.find((x) => x.id === runId);
		const row = run?.rows.find((x) => x.index === index);
		if (!run || !row) return problem(r, 404, 'not_found', 'no such row');
		const body = r.request().postDataJSON() as Json;
		this.rowPatches.push(body);
		const { review, ...set } = body;
		if (typeof set.collected_at === 'string' && !/^\d{4}-\d{2}-\d{2}(T\d{2}:\d{2})?$/.test(set.collected_at)) {
			return problem(r, 422, 'validation_failed', 'the review is incomplete or the edit is invalid', [
				{ pointer: '/collected_at', detail: 'must be an ISO 8601 local date or date-time without offset' }
			]);
		}
		const changes: Json = {};
		for (const [k, v] of Object.entries(set)) {
			if (!fields.includes(k)) return problem(r, 422, 'validation_failed', 'invalid edit', [{ pointer: `/${k}`, detail: 'is not an editable field' }]);
			if (row[k] !== v) changes[k] = { from: row[k], to: v };
			row[k] = v;
		}
		const edited = Object.keys(changes).length > 0;
		if (review === 'reject') row.review_status = 'rejected';
		else if (edited) row.review_status = 'edited';
		else if (review === 'accept') row.review_status = 'accepted';
		row.reviewed_at = at;
		if (edited) row.edits.push({ action: 'edit', changes, actor: 'owner', created_at: at });
		if (review === 'reject' || (review === 'accept' && !edited)) row.edits.push({ action: review, changes: {}, actor: 'owner', created_at: at });
		return json(r, 200, { ...row, validation: this.validate(row) });
	}

	private confirm(r: Route, runId: string) {
		const run = this.runs.find((x) => x.id === runId);
		if (!run) return problem(r, 404, 'not_found', 'no such extraction');
		const errors: { pointer: string; detail: string }[] = [];
		for (const row of run.rows) {
			if (row.review_status === 'pending') errors.push({ pointer: `/rows/${row.index}`, detail: 'not reviewed: accept, edit or reject it' });
			else if (row.review_status !== 'rejected' && row.collected_at === null) {
				errors.push({ pointer: `/rows/${row.index}/collected_at`, detail: 'is required: edit the row to add the collection date' });
			}
		}
		if (errors.length) return problem(r, 422, 'validation_failed', 'the review is incomplete or the edit is invalid', errors);
		for (const row of run.rows) {
			if (row.review_status === 'rejected') continue;
			const values = {
				analyte: row.analyte, original_label: row.analyte_label, value_text: row.value_text, value_numeric: row.value_numeric,
				comparator: row.comparator, unit_text: row.unit_text, reference_range_text: row.reference_range_text, ref_low: row.ref_low,
				ref_high: row.ref_high, printed_flag: row.printed_flag, specimen_type: row.specimen_type, collected_date: row.collected_at,
				page: row.page, evidence_text: row.evidence_text
			};
			const cur = this.results.find((x) => x.id === row.lab_result_id);
			if (cur) {
				if (Object.entries(values).some(([k, v]) => cur[k] !== v)) {
					(this.revisions[cur.id as string] ??= []).unshift({ ...cur });
					Object.assign(cur, values, { revision: (cur.revision as number) + 1, updated_at: '2026-10-02T09:00:00Z' });
				}
				continue;
			}
			const id = `lab_${hex(this.next++)}`;
			row.lab_result_id = id;
			this.results.push({
				id, revision: 1, ...values, canonical_value: null, canonical_unit: null, conversion_factor: null, conversion_offset: null,
				catalog_version: null, collected_at: null, created_at: at, updated_at: at,
				provenance: {
					report_id: `rpt_${hex(1)}`, document_id: run.document_id, extraction_id: run.id, row_index: row.index,
					laboratory: row.laboratory, reported_at: null, provider: run.provider, model: run.model, schema_version: run.schema_version,
					prompt_version: run.prompt_version, confirmed_by: 'owner', confirmed_at: at
				}
			});
		}
		run.status = 'confirmed';
		const doc = this.docs.find((d) => d.id === run.document_id);
		if (doc) doc.status = 'confirmed';
		return json(r, 200, this.withRows(run));
	}

	private unconfirm(r: Route, runId: string) {
		const run = this.runs.find((x) => x.id === runId);
		if (!run || run.status !== 'confirmed') return problem(r, 409, 'conflict', "this extraction is not the document's confirmed one");
		this.results = this.results.filter((x) => (x.provenance as Json).extraction_id !== run.id);
		for (const row of run.rows) row.lab_result_id = null;
		run.status = 'succeeded';
		return json(r, 200, this.withRows(run));
	}
}

function json(r: Route, status: number, body: unknown) {
	return r.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
}
function problem(r: Route, status: number, code: string, detail: string, errors?: { pointer: string; detail: string }[]) {
	return r.fulfill({
		status,
		contentType: 'application/problem+json',
		body: JSON.stringify({ type: 'about:blank', title: code, status, code, detail, request_id: 'req-e2e', errors })
	});
}

/** `test` with the base FakeApi (signed in) plus a fresh LabApi per test. */
export const test = base.extend<{ lab: LabApi }>({
	lab: [
		async ({ page, api }, use) => {
			api.signedIn = true;
			const lab = new LabApi(page);
			await lab.install();
			await use(lab);
		},
		{ auto: true }
	]
});

export { expect };
