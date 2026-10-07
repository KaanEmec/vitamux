# Known limitations

Appended to every release's notes by `scripts/release-notes.sh`. Remove an item in the change that fixes it.

- **Apple Health** sync (Swift package and app) arrives in v0.2 ([E15](../plan/E15-apple-health/README.md)).
- **Garmin and WHOOP** connectors come later; no unofficial collector ships in v0.1.
- **Withings** imports measures (blood pressure, body) only; activity and sleep streams are post-MVP ([deferred features](../architecture/project.md#deferred-features)).
- **Cold dashboard:** ten metrics resolved in parallel over 90 uncached days take about 4 s on 2 vCPUs and up to about 400 MiB; cached dates take about 50 ms ([resource budget](../resource-budget.md)).
- **Workout files:** the normalizer writer stores `workouts.file_blob_sha256` without retaining a blob reference, while the NDJSON importer does retain one. Normalized workout files are therefore unprotected from the orphan sweep, and deleting an imported workout leaves its file blob unswept. No connector writes workout files yet.
- **Lab analytes** have no LOINC codes yet; codes are filled only once verified ([analytes](../analytes.md)).
- **AI extraction** with real providers (Gemini, OpenAI) has not been evaluated on real reports yet; review every row before confirming.
- **PDFs with Type0/CMap fonts** count as having no text layer, so evidence checks are skipped for them ([lab documents](../architecture/lab-documents.md)).
- **Manual blood pressure** cannot be entered as one reading; the manual entry form takes single values, not systolic, diastolic and pulse together.
- **Interactive-MFA sign-in** (`POST …/auth/continue`) is not wired up; no v0.1 connector needs it.
- **iOS app on a physical iPhone and Apple Watch** is not yet verified: HealthKit authorization, background delivery, the Bridge upgrade in place, widgets on the lock screen and notification delivery were tested on the simulator only ([J22.23](../plan/E22-ios-app/J22.23-device-campaign-release.md)). The app is built from source; there is no App Store or TestFlight build.
