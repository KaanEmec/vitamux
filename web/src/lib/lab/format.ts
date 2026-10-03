// Labels and wording for the Lab results section. Copy states facts only: what was read, what
// differs from the PDF, what is missing. It never rates a value (internal/documents/uicopy_test.go
// scans this directory and the lab routes for judging words).
import type { Status } from '#lib/components/StatusIcon.svelte';
import type { Document, Extraction, Row } from './api.ts';

export const providerNames: Record<string, string> = {
	fake: 'Built-in test extractor',
	gemini: 'Google Gemini',
	openai: 'OpenAI',
	openai_compatible: 'OpenAI-compatible server'
};

export const providerName = (id: string) => providerNames[id] ?? id;

export const documentStatus: Record<Document['status'], { status: Status; label: string }> = {
	uploaded: { status: 'info', label: 'Uploaded' },
	extracting: { status: 'pending', label: 'Extracting' },
	needs_review: { status: 'warn', label: 'Needs review' },
	confirmed: { status: 'ok', label: 'Confirmed' },
	deleted: { status: 'off', label: 'Deleted' }
};

export const runStatus: Record<Extraction['status'], { status: Status; label: string }> = {
	queued: { status: 'pending', label: 'Queued' },
	running: { status: 'pending', label: 'Running' },
	succeeded: { status: 'warn', label: 'Ready for review' },
	failed: { status: 'error', label: 'Failed' },
	confirmed: { status: 'ok', label: 'Confirmed' }
};

export const rowStatus: Record<Row['review_status'], { status: Status; label: string }> = {
	pending: { status: 'pending', label: 'Not reviewed' },
	accepted: { status: 'ok', label: 'Accepted' },
	edited: { status: 'ok', label: 'Edited' },
	rejected: { status: 'off', label: 'Rejected' }
};

/** Why a run ended without rows (extraction_runs.error_class). */
export const errorClasses: Record<string, string> = {
	provider_disabled: 'The provider was disabled after the run was queued.',
	consent_mismatch: 'The configured model changed after consent was given; start a new extraction.',
	auth: 'The provider refused the configured key.',
	rejected: 'The provider refused the request.',
	rate_limited: 'The provider asked to wait; the run is retried.',
	transient: 'The provider could not be reached; the run is retried.',
	too_large: 'The PDF is larger than the provider accepts.',
	refused: 'The provider declined to answer.',
	invalid_output: 'The answer did not match the extraction format.',
	unknown_document: 'The built-in test extractor only reads the synthetic fixture PDFs.',
	provider_unavailable: 'The provider is no longer configured on this server.',
	abandoned: 'The run stopped without an outcome.'
};

/** Upload refusals (422 errors[0].detail). */
export const uploadReasons: Record<string, string> = {
	not_pdf: 'This file is not a PDF.',
	empty: 'This file is empty.',
	encrypted: 'Password-protected PDFs cannot be stored. Save an unprotected copy and upload that.',
	too_many_pages: 'The PDF has more than 50 pages.',
	malformed: 'The PDF structure could not be read.'
};

/** Deterministic checks (lab-documents.md#validation): what to compare with the PDF. */
export const validationText: Record<string, string> = {
	value_mismatch: 'The value and the printed value text read differently.',
	comparator_mismatch: 'The comparator and the printed value text read differently.',
	range_mismatch: 'The range limits and the printed range text read differently.',
	evidence_unverified: 'The evidence text was not found in the PDF text layer.',
	value_not_in_evidence: 'The value text does not appear in the evidence text.',
	unknown_unit: 'The unit is not one listed for this analyte.',
	unit_not_convertible: 'No conversion is listed from this unit for this analyte; the printed value is kept as is.',
	unit_missing: 'No unit was read. Accepting confirms the value as unitless.',
	date_missing: 'No collection date was read. Add it before confirming.',
	date_in_future: 'A date on this row is later than today.',
	date_ambiguous: 'The printed date reads both as day/month and month/day.',
	reported_before_collected: 'The report date is earlier than the collection date.',
	duplicate_in_run: 'Another row of this extraction has the same analyte.',
	already_confirmed: 'A result for this analyte and collection date is already confirmed from another document.',
	unknown_analyte: 'No analyte matches this label.'
};

/** Extractor warnings (schemas/lab-extraction.v1.json). */
export const extractorText: Record<string, string> = {
	unreadable_label: 'The extractor could not read the label.',
	unreadable_value: 'The extractor could not read the value.',
	unreadable_unit: 'The extractor could not read the unit.',
	unreadable_range: 'The extractor could not read the range.',
	unreadable_flag: 'The extractor could not read the flag.',
	uncertain_reading: 'The extractor marked this reading as uncertain.',
	handwritten: 'Part of this row is handwritten.',
	crossed_out: 'Part of this row is crossed out.',
	split_across_pages: 'This row continues on another page.',
	footnote: 'This row has a footnote.',
	multiple_values: 'More than one value is printed for this row.',
	page_unreadable: 'A page could not be read.',
	low_quality_image: 'The scan is of low quality.',
	possibly_truncated: 'The document may be cut off.',
	not_lab_report: 'The extractor did not recognise a lab report.',
	multiple_reports: 'The PDF holds more than one report.',
	dates_not_found: 'No dates were found.'
};

export const warningText = (code: string) => validationText[code] ?? extractorText[code] ?? code;

/** Value as printed, with its comparator: "< 0.5". */
export function printedValue(r: { comparator: string | null; value_text: string | null }): string {
	if (r.value_text === null) return '–';
	return r.comparator && !r.value_text.trim().startsWith(r.comparator) ? `${r.comparator} ${r.value_text}` : r.value_text;
}

/** Local date and time of an RFC 3339 instant; an en dash when missing. */
export function when(iso: string | null | undefined): string {
	const t = iso ? new Date(iso) : null;
	return t && !Number.isNaN(t.getTime()) ? t.toLocaleString() : '–';
}

/** "1.2 MiB" for a byte count. */
export function size(n: number): string {
	if (n < 1024) return `${n} B`;
	if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
	return `${(n / 1024 / 1024).toFixed(1)} MiB`;
}

/** "Row 3 · Glucose" from a problem pointer such as /rows/2/value_text, given the rows. */
export function rowOfPointer(pointer: string): number | null {
	const m = /^\/rows\/(\d+)/.exec(pointer);
	return m ? Number(m[1]) : null;
}
