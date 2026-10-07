// API calls of the Lab results section (docs/architecture/lab-documents.md). Every function
// returns the data or a Problem, never both.
import { api, type Problem, type Schemas } from '#lib/api/client.ts';

export type Document = Schemas['Document'];
export type Extraction = Schemas['Extraction'];
export type Row = Schemas['ExtractionRow'];
export type RowPatch = Schemas['ExtractionRowPatch'];
export type LabResult = Schemas['LabResult'];
export type Extractor = Schemas['Extractor'];
type Alias = Schemas['AnalyteAlias'];

type Result<T> = { data: T; problem: null } | { data: null; problem: Problem };

const ok = <T>(data: T): Result<T> => ({ data, problem: null });
const fail = <T>(problem: Problem): Result<T> => ({ data: null, problem });

/** Upload limits the server enforces (lab-documents.md#storage); checked here only to answer early. */
export const maxUploadBytes = 20 * 1024 * 1024;

/** Stores a PDF; `existing` is true when the same content was already stored. */
export async function upload(file: File): Promise<Result<{ doc: Document; existing: boolean }>> {
	const form = new FormData();
	form.append('file', file, file.name);
	const { data, error, response } = await api.POST('/api/v1/documents', {
		// The multipart body is built here; openapi-fetch passes FormData through unchanged.
		body: form as unknown as { file: string },
		bodySerializer: (b) => b as unknown as FormData
	});
	return error ? fail(error) : ok({ doc: data, existing: response.status === 200 });
}

/** Every page of a keyset-paged list. */
async function allPages<T>(
	fetch: (cursor?: string) => Promise<{ items?: T[]; page?: { has_more: boolean; next_cursor?: string }; error?: Problem }>
): Promise<Result<T[]>> {
	const out: T[] = [];
	let cursor: string | undefined;
	for (;;) {
		const { items, page, error } = await fetch(cursor);
		if (error) return fail(error);
		out.push(...(items ?? []));
		if (!page?.has_more || !page.next_cursor) return ok(out);
		cursor = page.next_cursor;
	}
}

export function listDocuments(): Promise<Result<Document[]>> {
	return allPages(async (cursor) => {
		const { data, error } = await api.GET('/api/v1/documents', { params: { query: { limit: 500, cursor } } });
		return error ? { error } : { items: data.documents, page: data };
	});
}

export async function getDocument(id: string): Promise<Result<Document>> {
	const { data, error } = await api.GET('/api/v1/documents/{id}', { params: { path: { id } } });
	return error ? fail(error) : ok(data);
}

/** The original PDF's bytes, for the viewer. */
export async function documentFile(id: string): Promise<Result<ArrayBuffer>> {
	const { data, error } = await api.GET('/api/v1/documents/{id}/file', {
		params: { path: { id } },
		parseAs: 'arrayBuffer'
	});
	return error ? fail(error) : ok(data);
}

export async function deleteDocument(id: string, derived: 'keep' | 'delete'): Promise<Problem | null> {
	const { error } = await api.DELETE('/api/v1/documents/{id}', { params: { path: { id }, query: { derived } } });
	return error ?? null;
}

export async function listExtractors(): Promise<Result<Extractor[]>> {
	const { data, error } = await api.GET('/api/v1/extractors');
	return error ? fail(error) : ok(data.extractors);
}

/** Runs of a document, newest first. */
export async function listRuns(docId: string): Promise<Result<Extraction[]>> {
	const { data, error } = await api.GET('/api/v1/documents/{id}/extractions', { params: { path: { id: docId } } });
	return error ? fail(error) : ok(data.extractions);
}

/** Starts a run. External providers need the consent the dialog collected. */
export async function startRun(docId: string, ex: Extractor): Promise<Result<Extraction>> {
	const consent = ex.external
		? { provider: ex.id, model: ex.model ?? '', acknowledged_at: new Date().toISOString() }
		: null;
	const { data, error } = await api.POST('/api/v1/documents/{id}/extractions', {
		params: { path: { id: docId } },
		body: { provider: ex.id, consent }
	});
	return error ? fail(error) : ok(data);
}

export async function getRun(id: string): Promise<Result<Extraction>> {
	const { data, error } = await api.GET('/api/v1/extractions/{id}', { params: { path: { id } } });
	return error ? fail(error) : ok(data);
}

export async function patchRow(runId: string, index: number, patch: RowPatch): Promise<Result<Row>> {
	const { data, error } = await api.PATCH('/api/v1/extractions/{id}/rows/{row}', {
		params: { path: { id: runId, row: String(index) } },
		body: patch
	});
	return error ? fail(error) : ok(data);
}

export async function confirmRun(id: string): Promise<Result<Extraction>> {
	const { data, error } = await api.POST('/api/v1/extractions/{id}/confirm', { params: { path: { id } } });
	return error ? fail(error) : ok(data);
}

export async function unconfirmRun(id: string): Promise<Result<Extraction>> {
	const { data, error } = await api.POST('/api/v1/extractions/{id}/unconfirm', { params: { path: { id } } });
	return error ? fail(error) : ok(data);
}

export function listResults(): Promise<Result<LabResult[]>> {
	return allPages(async (cursor) => {
		const { data, error } = await api.GET('/api/v1/lab-results', { params: { query: { limit: 1000, cursor } } });
		return error ? { error } : { items: data.lab_results, page: data };
	});
}

/** Revisions of one result, newest (current) first. */
export async function resultHistory(id: string): Promise<Result<LabResult[]>> {
	const { data, error } = await api.GET('/api/v1/lab-results/{id}/history', { params: { path: { id } } });
	return error ? fail(error) : ok(data.revisions);
}

export async function listAliases(): Promise<Result<Alias[]>> {
	const { data, error } = await api.GET('/api/v1/analytes/aliases');
	return error ? fail(error) : ok(data.aliases);
}
