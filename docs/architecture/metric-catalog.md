# Metric catalogue

Rules for metric codes, and the codes not yet implemented. Implemented codes (the `v1` seed from [J07.1](../plan/E07-normalization/J07.1-catalogue-units.md)) with their units, kinds, aggregation, plausible ranges and windows live in the generated [metrics.md](../metrics.md); the source is [`internal/catalog`](../../internal/catalog). Lab analytes live in [analyte-catalog.md](analyte-catalog.md), and the table shapes in [data-model.md](data-model.md).

Adding a code means appending it to `internal/catalog` in the job that needs it with a new seed marker (`Since`, plus its file in `seedFiles`), so `go run ./internal/catalog/gen` writes its own seed migration and released ones never change, and then moving its row out of this file.

## Rules

- **One code per quantity and method.** Sources combine only when they share a code. Measurements made with different methods get different codes and never share a rule, e.g. `hrv_sdnn` vs `hrv_rmssd`, or `hrv_rmssd` (samples) vs `hrv_rmssd_nightly` (provider overnight average).
- **Device, origin and context are selectors, not codes.** Fingerstick vs CGM glucose, or Watch vs iPhone steps, stay one code each. The difference lives in `device`, `origin` and `context`.
- **Every provider value is catalogued.** A value a provider reports gets a code, provider-scoped (`<provider>_<name>`) when the definition is proprietary. Only identifiers, baselines, UI text and values derivable from stored rows stay raw, each with a reason. The per-stream field ledgers (`testdata/<stream>/fields.json`, [adapters.md](../adapters.md#field-ledgers)) map every raw field to a code or to `raw: <reason>`, and a test fails on an unlisted field.
- **Reported totals only.** Active, basal and total energy are separate codes. Only the totals a provider reports are stored, and they are never summed or subtracted across codes. Known mis-mappings of other aggregators are not copied: WHOOP kJ as active energy, a BMR rate as basal energy, total minus active as basal, Oura equivalent distance as distance, and the lowest sleep heart rate as resting heart rate.
- **Selection-only metrics.** When providers define a metric differently, the catalogue drops `mean`, `min` and `max` for it, the same way it does for provider-scoped scores. The provider's definition goes in `context`. This applies to `resting_heart_rate` (sleep-based, awake-inactive, lowest 30 min in 24 h, or still periods), `hrv_rmssd_nightly` (vendors use different overnight windows) and `sleep_temperature_deviation` (provider baselines, a delta that never shares a code with absolute temperatures). The same goes for `spo2_nightly`, `respiratory_rate_nightly`, `skin_temperature_nightly` and the intensity and `sedentary_time` codes (definitions in `context`). They carry `SelectionOnly` in `internal/catalog` ([J09.2](../plan/E09-resolution/J09.2-rules-storage-defaults.md)). Suggested ladders: [resolution-defaults.md](resolution-defaults.md).
- **Proprietary scores are provider-namespaced** (`<provider>_<name>`) and never pooled across providers. They land in the catalogue together with their connector.
- **Derived codes** have no rows of their own. Resolution computes them from a source metric (rule [extension](resolution.md#extensions) E2), so they get a catalogue entry for rules and APIs without breaking the rule below. The implemented ones (`resting_heart_rate_nocturnal`, `spo2_night_min`) carry `DerivedFrom` and are listed in [metrics.md](../metrics.md#derived).
- **Intraday resolution is catalogue metadata**, planned in [J22.26](../plan/E22-ios-app/J22.26-intraday-views.md) and built in [E26](../plan/E26-intraday-views/README.md): `intraday {default, finest}` per metric, set by aggregation (high-frequency intensive 1 min → 30 s and raw; sparse intensive 5 min → raw; additive 30 min → 1 min; `daily_summary`, `latest`, `sleep_derived` and nightly codes none). Clients pick the bucket from the visible span and never draw a source finer than it was sent; no metric has its own chart code.
- **Calculated values are never stored as measurements.** BMI, MAP, pulse pressure, time-in-range, GMI and sleep debt come from resolution or views. A value the *provider* reports (e.g., BMI from a scale) is stored as-is with its origin.
- **Canonical units** use SI-style units, kept readable: s for durations, m for distances, kg, kcal, °C, mmol/L, % (0–100). The source value and unit are kept whenever conversion changed the value ([data-model.md](data-model.md#measurements)).
- **Phase:** codes marked below are not implemented yet; the J07.1 seed (`v1`, MVP), the Apple bridge codes (J15.2) and the mapping codes of [J25.1](../plan/E25-catalogue-mappings/J25.1-policy-ledger-seed.md) (Apple HK activity, audio, mobility and respiratory types, segmental body composition, ECG intervals, nightly and provider-scoped codes) are implemented and listed in [metrics.md](../metrics.md). `J08.6` = Withings activity and sleep (post-MVP) · `later` = backlog, added with the first connector that needs it. A connector that needs a code not yet in the catalogue adds it in its own job.
- **Withings `meastype` codes** were verified against the official `getmeas` reference in [J08.1](../providers/withings.md#assumptions-checked). Measures outside `bp_reading` and `body_composition` (SpO2, temperature, VO2max) are stored as plain samples.

Kinds: `S` sample · `I` interval · `C` cumulative · `D` daily_value. Aggregation values are defined in [resolution.md](resolution.md#within-source-aggregation). HK ids omit the `HKQuantityTypeIdentifier` / `HKCategoryTypeIdentifier` prefix. W = Withings `meastype`.

## Activity

| Code | Unit | Kinds | Aggregation | Apple HK | W | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| `cadence_steps` | steps/min | S | intensive 5 min | | | later |

## Heart and circulation

| Code | Unit | Kinds | Aggregation | Apple HK | W | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| `rr_interval` | s | S | raw series, not resolved | HKHeartbeatSeriesSample | | later |

## Blood pressure (group `bp_reading`)

The three cuff codes are implemented (see [metrics.md](../metrics.md)). Group context: position, arm, cuff, irregular-heartbeat flag, part of an averaged session. MAP and pulse pressure are calculated, never stored.

Cuffless estimates from calibrated optical watches get their own codes, so they can never enter a cuff average. Micro-cuff oscillometric watches use the cuff codes, with their `device_type` recorded.

| Code | Unit | Kinds | Aggregation | Apple HK | W | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| `bp_systolic_estimated`, `bp_diastolic_estimated` | mmHg | S (group `bp_estimate`) | latest | | | later |

## Body composition (group `body_composition`)

| Code | Unit | Kinds | Aggregation | Apple HK | W | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| `hip_circumference`, `chest_circumference`, `arm_circumference`, `thigh_circumference` | m | S | latest | | | later |

`lean_body_mass` and `fat_free_mass` are separate codes (decision 2 below).

## Glucose and metabolism

| Code | Unit | Kinds | Aggregation | Apple HK | W | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| `blood_ketones` | mmol/L | S | latest | | | later |
| `blood_lactate` | mmol/L | S | latest | | | later |

The CGM trend arrow and meal or fasting context belong in `context`. TIR, TAR, TBR, GMI, CV and mean glucose are resolution outputs.

## Nutrition and intake (`I`, additive)

| Code | Unit | Apple HK | Phase |
| --- | --- | --- | --- |
| `diet_sodium`, `diet_potassium`, `diet_calcium`, `diet_magnesium`, `diet_iron`, `diet_zinc`, `diet_phosphorus`, `diet_chloride`, `diet_copper`, `diet_manganese` | mg | Dietary… | later |
| `diet_selenium`, `diet_chromium`, `diet_iodine`, `diet_molybdenum` | µg | Dietary… | later |
| `diet_vitamin_a`, `diet_folate`, `diet_vitamin_b12`, `diet_vitamin_d`, `diet_vitamin_k`, `diet_biotin` | µg | Dietary… | later |
| `diet_vitamin_b6`, `diet_vitamin_c`, `diet_vitamin_e`, `diet_thiamin`, `diet_riboflavin`, `diet_niacin`, `diet_pantothenic_acid` | mg | Dietary… | later |

HealthKit food correlations become a meal group (proposed group kind `meal`), with nutrient rows as members.

## Environment and hearing

| Code | Unit | Kinds | Aggregation | Apple HK | Phase |
| --- | --- | --- | --- | --- | --- |
| `bedroom_temperature`, `bedroom_humidity`, `bedroom_noise`, `bedroom_light` | °C, %, dBA, lx | S | intensive 5 min | | later |

## Device-measured urine

Home urine analysers such as the Withings U-Scan produce time series. These are metrics, not lab analytes. They are linked to the [urine analytes](../analytes.md#urine) only for display.

| Code | Unit | W | Phase |
| --- | --- | --- | --- |
| `urine_ph` | pH | 147 | J25.2 |
| `urine_specific_gravity` | ratio | 148 | J25.2 |
| `urine_nitrites` | µmol/L | 151 | J25.2 |
| `urine_ketones` | mmol/L | 204 | J25.2 |
| `urine_vitamin_c` | mmol/L | 205 | J25.2 |
| `urine_calcium`, `urine_creatinine` | mmol/L | 248, 249 | J25.2 |
| `urine_calcium_creatinine_ratio` | mmol/mmol | 251 | J25.2 |

## Sleep (tables `sleep_sessions`, `sleep_stages`)

Sleep is an episode with stages, not scalar rows. Derived codes use `sleep_derived` aggregation over the aligned main episode ([resolution.md](resolution.md)).

| Code | Unit | Source | Phase |
| --- | --- | --- | --- |
| `sleep_rem_episodes` | count | provider summary | J08.6 |
| `sleep_regularity` | % | provider summary | later |

Sleep vitals (HR, HRV, respiratory rate, SpO2, temperature) use the regular codes, aligned to the episode through windows. They are not separate codes.

## Workouts (tables `workouts`, `workout_segments`)

A workout is an event. Its scalar fields are columns or segment `data`, not catalogue codes:

- Session: sport (canonical and provider), start/end, duration, moving time, distance, energy, average and max HR, elevation, route file, planned workout reference.
- Segments: laps, splits, intervals, strength sets (exercise, reps, weight), swim lengths (stroke, SWOLF).
- Zones: time in HR, power and pace zones, with zone bounds stored as given by the provider.
- Provider effort and load: HK WorkoutEffortScore / EstimatedWorkoutEffortScore, training load, TSS, intensity factor and normalized power are stored as provider-namespaced fields in segment `data`.

## Provider-namespaced scores

Each score is added together with its connector. They are never pooled across providers, and `mean`, `min` and `max` are rejected for them (`ProviderScoped` in the catalogue).

| Pattern | Examples | Aggregation |
| --- | --- | --- |
| Readiness / recovery | `oura_readiness`, `polar_nightly_recharge`, `fitbit_daily_readiness`, `ultrahuman_recovery` | daily_summary |
| Sleep score | `withings_sleep_score`, `oura_sleep_score`, `eight_sleep_sleep_score` | daily_summary |
| Activity score | `oura_activity_score`, `ultrahuman_movement_index` | daily_summary |
| Stress / energy | `oura_daytime_stress`, `fitbit_stress_management` | daily_summary or intensive |
| Fitness estimates | `fitbit_cardio_fitness`, `withings_nerve_health_score` (W 167), `withings_nerve_response_score` (W 196), `withings_esc` (W 229), `withings_metabolic_age` (W 227) | latest |
| Breathing quality | `withings_breathing_quality` | daily_summary |

Implemented ([J25.1](../plan/E25-catalogue-mappings/J25.1-policy-ledger-seed.md)): `whoop_sleep_need`, `whoop_sleep_debt`, `whoop_sleep_consistency`, `whoop_sleep_disturbances`, `whoop_max_heart_rate`, `garmin_body_battery_charged`, `garmin_body_battery_drained`, `garmin_fitness_age`, `garmin_acute_load`, `garmin_chronic_load`, `withings_sleep_score`, `withings_breathing_quality`, `withings_nerve_health_score`, `withings_nerve_response_score`, `withings_metabolic_age`, `withings_esc`.

Implemented ([J25.4](../plan/E25-catalogue-mappings/J25.4-whoop.md)): `whoop_workout_strain` (interval, latest), `whoop_hr_zone_0_time` to `whoop_hr_zone_5_time` (seconds, additive intervals over a workout), `elevation_change` (net, additive), `whoop_sleep_debt_post`, `whoop_sleep_need_habitual`, `whoop_sleep_need_from_strain`, `whoop_sleep_nap_credit`, `whoop_sleep_cycles`. The `calibrating` quality flag marks rows of a WHOOP recovery scored during calibration.

Implemented with their connectors ([J18.4](../plan/E18-garmin/J18.4-normalizers.md), [J19.4](../plan/E19-whoop/J19.4-normalizers.md)): `garmin_stress`, `garmin_body_battery` (samples, intensive), `garmin_training_readiness`, `garmin_sleep_score`, `whoop_recovery`, `whoop_strain`, `whoop_sleep_performance` (daily_summary). WHOOP SpO2 and skin temperature stay raw until a matching method is confirmed.

## Events

These are typed events with a value or level, not numbers that can be resolved. **Decided in [ADR-0014](../adr/0014-healthkit-contract.md): one `health_events` table** (code, start/end, level or value, context), not one table per family. Implemented codes are in [metrics.md](../metrics.md#events) (`internal/catalog/events.go`); the rows below are not implemented yet. Implemented ([J25.2](../plan/E25-catalogue-mappings/J25.2-withings-measures.md)): `afib_ecg_result` (W 130) and `afib_ppg_result` (W 139), the Withings AFib category (0 to 13) as level.

| Family | Codes | Sources | Phase |
| --- | --- | --- | --- |
| ECG recording | `ecg_recording` (waveform blob, classification, average HR) | HKElectrocardiogram; Withings heart list (no job yet) | later |
| Rhythm results | `irregular_rhythm_alert` | HK IrregularHeartRhythmEvent | later |
| Cycle tracking | `menstrual_flow`, `intermenstrual_bleeding`, `ovulation_test`, `pregnancy_test`, `progesterone_test`, `cervical_mucus`, `sexual_activity`, `contraceptive`, `pregnancy`, `lactation`, cycle-deviation alerts | HK categories with the same names | later |
| Mind | `mindful_session`, `state_of_mind` (valence, labels) | HK MindfulSession, HKStateOfMind | later |
| Hygiene | `handwashing`, `toothbrushing` | HK HandwashingEvent, ToothbrushingEvent | later |
| Symptoms (`symptom_<name>`, severity level) | abdominal_cramps, acne, appetite_changes, bladder_incontinence, bloating, breast_pain, chest_tightness_or_pain, chills, constipation, coughing, diarrhea, dizziness, dry_skin, fainting, fatigue, fever, generalized_body_ache, hair_loss, headache, heartburn, hot_flashes, loss_of_smell, loss_of_taste, lower_back_pain, memory_lapse, mood_changes, nausea, night_sweats, pelvic_pain, rapid_pounding_or_fluttering_heartbeat, runny_nose, shortness_of_breath, sinus_congestion, skipped_heartbeat, sleep_changes, sore_throat, vaginal_dryness, vomiting, wheezing | HK symptom categories | later |
| Medication | `medication_dose` | HKMedicationDoseEvent; manual entry | later |
| Clinical records | allergies, conditions, immunizations, procedures (FHIR resources kept raw) | HK clinical records | later |

## Open questions and decisions

1. **Decided ([ADR-0014](../adr/0014-healthkit-contract.md)): events live in one `health_events` table.**
2. **Decided (J07.1): `fat_free_mass` and `lean_body_mass` are separate codes.** Withings `fat_free_mass` (5) and HK `LeanBodyMass` are not known to share a definition, and a wrong merge cannot be undone in stored data. Withings maps to `fat_free_mass`, HealthKit to `lean_body_mass` (E15). Revisit only with evidence that a provider defines them identically.
3. **Decided (J07.1): `hrv_rmssd` is split.** `hrv_rmssd` holds samples (`sample`, `intensive`); `hrv_rmssd_nightly` holds the provider's overnight average (`daily_value`, `daily_summary`). A 5-min sample series and a nightly mean are different methods, and averaging samples over a whole day would mix waking values into a sleep-time metric. Both are in the v1 seed and are not combinable. `hrv_sdnn` stays separate from both.
4. **Decided (J07.1): one code per body segment.** A segment is a different quantity, not a context of one, and rules and windows see only codes. The codes are `<quantity>_<segment>` (`fat_free_mass_trunk`, `muscle_mass_left_arm`, ...), implemented in J25.1.
