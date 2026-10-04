# Metric catalogue

Rules for metric codes, and the codes not yet implemented. Implemented codes (the `v1` seed from [J07.1](../plan/E07-normalization/J07.1-catalogue-units.md)) with their units, kinds, aggregation, plausible ranges and windows live in the generated [metrics.md](../metrics.md); the source is [`internal/catalog`](../../internal/catalog). Lab analytes live in [analyte-catalog.md](analyte-catalog.md), and the table shapes in [data-model.md](data-model.md).

Adding a code means appending it to `internal/catalog` in the job that needs it with a new seed marker (`Since`, plus its file in `seedFiles`), so `go run ./internal/catalog/gen` writes its own seed migration and released ones never change, and then moving its row out of this file.

## Rules

- **One code per quantity and method.** Sources combine only when they share a code. Measurements made with different methods get different codes and never share a rule, e.g. `hrv_sdnn` vs `hrv_rmssd`, or `hrv_rmssd` (samples) vs `hrv_rmssd_nightly` (provider overnight average).
- **Device, origin and context are selectors, not codes.** Fingerstick vs CGM glucose, or Watch vs iPhone steps, stay one code each. The difference lives in `device`, `origin` and `context`.
- **Selection-only metrics.** When providers define a metric differently, the catalogue drops `mean`, `min` and `max` for it, the same way it does for provider-scoped scores. The provider's definition goes in `context`. This applies to `resting_heart_rate` (sleep-based, awake-inactive, lowest 30 min in 24 h, or still periods), `hrv_rmssd_nightly` (vendors use different overnight windows) and `sleep_temperature_deviation` (provider baselines). The two implemented codes carry `SelectionOnly` in `internal/catalog` ([J09.2](../plan/E09-resolution/J09.2-rules-storage-defaults.md)). Suggested ladders: [resolution-defaults.md](resolution-defaults.md).
- **Proprietary scores are provider-namespaced** (`<provider>_<name>`) and never pooled across providers. They land in the catalogue together with their connector.
- **Derived codes** have no rows of their own. Resolution computes them from a source metric (rule [extension](resolution.md#extensions) E2), so they get a catalogue entry for rules and APIs without breaking the rule below. The implemented ones (`resting_heart_rate_nocturnal`, `spo2_night_min`) carry `DerivedFrom` and are listed in [metrics.md](../metrics.md#derived).
- **Calculated values are never stored as measurements.** BMI, MAP, pulse pressure, time-in-range, GMI and sleep debt come from resolution or views. A value the *provider* reports (e.g., BMI from a scale) is stored as-is with its origin.
- **Canonical units** use SI-style units, kept readable: s for durations, m for distances, kg, kcal, °C, mmol/L, % (0–100). The source value and unit are kept whenever conversion changed the value ([data-model.md](data-model.md#measurements)).
- **Phase:** codes marked below are not implemented yet; the J07.1 seed (`v1`, MVP) and the Apple bridge codes (J15.2) are implemented and listed in [metrics.md](../metrics.md). `J08.6` = Withings activity and sleep (post-MVP) · `later` = backlog, added with the first connector that needs it. A connector that needs a code not yet in the catalogue adds it in its own job.
- **Withings `meastype` codes** were verified against the official `getmeas` reference in [J08.1](../providers/withings.md#assumptions-checked). Measures outside `bp_reading` and `body_composition` (SpO2, temperature, VO2max) are stored as plain samples.

Kinds: `S` sample · `I` interval · `C` cumulative · `D` daily_value. Aggregation values are defined in [resolution.md](resolution.md#within-source-aggregation). HK ids omit the `HKQuantityTypeIdentifier` / `HKCategoryTypeIdentifier` prefix. W = Withings `meastype`.

## Activity

| Code | Unit | Kinds | Aggregation | Apple HK | W | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| `distance_rowing`, `distance_paddle`, `distance_skating`, `distance_xc_ski`, `distance_downhill_snow` | m | I | additive | DistanceRowing, DistancePaddleSports, DistanceSkatingSports, DistanceCrossCountrySkiing, DistanceDownhillSnowSports | | later |
| `elevation_gain` | m | I D | additive | | | J08.6 |
| `total_energy` | kcal | D | daily_summary | | | J08.6 |
| `intensity_moderate_time`, `intensity_vigorous_time` | s | I D | additive | | | J08.6 |
| `sedentary_time` | s | D | daily_summary | | | later |
| `move_time` | s | I | additive | AppleMoveTime | | later |
| `daylight_time` | s | I | additive | TimeInDaylight | | later |
| `wheelchair_pushes` | count | I | additive | PushCount | | later |
| `swim_strokes` | count | I | additive | SwimmingStrokeCount | | later |
| `speed_walking`, `speed_running`, `speed_cycling`, `speed_rowing`, `speed_paddle` | m/s | S | intensive 5 min | WalkingSpeed, RunningSpeed, CyclingSpeed, RowingSpeed, PaddleSportsSpeed | | later |
| `cadence_steps` | steps/min | S | intensive 5 min | | | later |
| `cadence_cycling` | rpm | S | intensive 5 min | CyclingCadence | | later |
| `power_running`, `power_cycling` | W | S | intensive 5 min | RunningPower, CyclingPower | | later |
| `ftp_cycling` | W | S | latest | CyclingFunctionalThresholdPower | | later |
| `running_stride_length` | m | S | intensive 5 min | RunningStrideLength | | later |
| `running_vertical_oscillation` | m | S | intensive 5 min | RunningVerticalOscillation | | later |
| `running_ground_contact_time` | s | S | intensive 5 min | RunningGroundContactTime | | later |
| `physical_effort` | kcal/hr/kg (MET) | S | intensive 5 min | PhysicalEffort | | later |

## Heart and circulation

| Code | Unit | Kinds | Aggregation | Apple HK | W | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| `sleeping_heart_rate` | bpm | D | daily_summary | | | later |
| `heart_rate_recovery_1min` | bpm | S | latest | HeartRateRecoveryOneMinute | | later |
| `rr_interval` | s | S | raw series, not resolved | HKHeartbeatSeriesSample | | later |
| `afib_burden` | % | D | daily_summary | AtrialFibrillationBurden | | later |
| `perfusion_index` | % | S | intensive 5 min | PeripheralPerfusionIndex | | later |
| `ecg_qrs`, `ecg_pr`, `ecg_qt`, `ecg_qtc` | s | S (proposed group `ecg`) | latest | | 135, 136, 137, 138 | later |

## Blood pressure (group `bp_reading`)

The three cuff codes are implemented (see [metrics.md](../metrics.md)). Group context: position, arm, cuff, irregular-heartbeat flag, part of an averaged session. MAP and pulse pressure are calculated, never stored.

Cuffless estimates from calibrated optical watches get their own codes, so they can never enter a cuff average. Micro-cuff oscillometric watches use the cuff codes, with their `device_type` recorded.

| Code | Unit | Kinds | Aggregation | Apple HK | W | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| `bp_systolic_estimated`, `bp_diastolic_estimated` | mmHg | S (group `bp_estimate`) | latest | | | later |

## Respiration and oxygen

| Code | Unit | Kinds | Aggregation | Apple HK | W | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| `apnea_hypopnea_index` | events/h | D | daily_summary | | | J08.6 |
| `fev1`, `fvc` | L | S | latest | ForcedExpiratoryVolume1, ForcedVitalCapacity | | later |
| `peak_expiratory_flow` | L/min | S | latest | PeakExpiratoryFlowRate | | later |
| `inhaler_uses` | count | I | additive | InhalerUsage | | later |

## Temperature

| Code | Unit | Kinds | Aggregation | Apple HK | W | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| `sleep_temperature_deviation` | °C (delta from baseline) | D | daily_summary, selection only | | | later |

`sleep_temperature_deviation` is a provider delta, not an absolute value, so it never shares a code with absolute temperatures.

## Body composition (group `body_composition`)

| Code | Unit | Kinds | Aggregation | Apple HK | W | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| `fat_free_mass_<segment>`, `fat_mass_<segment>`, `muscle_mass_<segment>` with segment in `trunk`, `left_arm`, `right_arm`, `left_leg`, `right_leg` | kg | S | latest | | 173, 174, 175 | later |
| `hip_circumference`, `chest_circumference`, `arm_circumference`, `thigh_circumference` | m | S | latest | | | later |

`lean_body_mass` and `fat_free_mass` are separate codes (decision 2 below).

## Glucose and metabolism

| Code | Unit | Kinds | Aggregation | Apple HK | W | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| `insulin_basal`, `insulin_bolus` | IU | I | additive | InsulinDelivery (reason in metadata) | | later |
| `blood_ketones` | mmol/L | S | latest | | | later |
| `blood_lactate` | mmol/L | S | latest | | | later |
| `blood_alcohol` | % | S | latest | BloodAlcoholContent | | later |
| `alcoholic_drinks` | count | I | additive | NumberOfAlcoholicBeverages | | later |

The CGM trend arrow and meal or fasting context belong in `context`. TIR, TAR, TBR, GMI, CV and mean glucose are resolution outputs.

## Nutrition and intake (`I`, additive)

| Code | Unit | Apple HK | Phase |
| --- | --- | --- | --- |
| `diet_sodium`, `diet_potassium`, `diet_calcium`, `diet_magnesium`, `diet_iron`, `diet_zinc`, `diet_phosphorus`, `diet_chloride`, `diet_copper`, `diet_manganese` | mg | Dietary… | later |
| `diet_selenium`, `diet_chromium`, `diet_iodine`, `diet_molybdenum` | µg | Dietary… | later |
| `diet_vitamin_a`, `diet_folate`, `diet_vitamin_b12`, `diet_vitamin_d`, `diet_vitamin_k`, `diet_biotin` | µg | Dietary… | later |
| `diet_vitamin_b6`, `diet_vitamin_c`, `diet_vitamin_e`, `diet_thiamin`, `diet_riboflavin`, `diet_niacin`, `diet_pantothenic_acid` | mg | Dietary… | later |

HealthKit food correlations become a meal group (proposed group kind `meal`), with nutrient rows as members.

## Mobility

| Code | Unit | Kinds | Aggregation | Apple HK | Phase |
| --- | --- | --- | --- | --- | --- |
| `stair_ascent_speed`, `stair_descent_speed` | m/s | S | intensive 5 min | StairAscentSpeed, StairDescentSpeed | later |
| `six_minute_walk_distance` | m | S | latest | SixMinuteWalkTestDistance | later |
| `falls` | count | I | additive | NumberOfTimesFallen | later |

## Environment and hearing

| Code | Unit | Kinds | Aggregation | Apple HK | Phase |
| --- | --- | --- | --- | --- | --- |
| `environment_audio_exposure` | dBA | S | intensive 5 min | EnvironmentalAudioExposure | later |
| `headphone_audio_exposure` | dBA | S | intensive 5 min | HeadphoneAudioExposure | later |
| `environment_sound_reduction` | dB | S | intensive 5 min | EnvironmentalSoundReduction | later |
| `uv_exposure` | index | S | intensive 5 min | UVExposure | later |
| `water_temperature`, `underwater_depth` | °C, m | S | latest | WaterTemperature, UnderwaterDepth | later |
| `electrodermal_activity` | µS | S | intensive 5 min | ElectrodermalActivity | later |
| `bedroom_temperature`, `bedroom_humidity`, `bedroom_noise`, `bedroom_light` | °C, %, dBA, lx | S | intensive 5 min | | later |

## Device-measured urine

Home urine analysers such as the Withings U-Scan produce time series. These are metrics, not lab analytes. They are linked to the [urine analytes](../analytes.md#urine) only for display.

| Code | Unit | W | Phase |
| --- | --- | --- | --- |
| `urine_ph` | pH | 147 | later |
| `urine_specific_gravity` | ratio | 148 | later |
| `urine_nitrites` | µmol/L | 151 | later |
| `urine_ketones` | mmol/L | 204 | later |
| `urine_vitamin_c` | mmol/L | 205 | later |
| `urine_calcium`, `urine_creatinine` | mmol/L | 248, 249 | later |
| `urine_calcium_creatinine_ratio` | mmol/mmol | 251 | later |

## Sleep (tables `sleep_sessions`, `sleep_stages`)

Sleep is an episode with stages, not scalar rows. Derived codes use `sleep_derived` aggregation over the aligned main episode ([resolution.md](resolution.md)).

| Code | Unit | Source | Phase |
| --- | --- | --- | --- |
| `sleep_awakenings`, `sleep_rem_episodes` | count | provider summary | J08.6 |
| `sleep_snoring_time` | s | provider summary (Withings) | J08.6 |
| `sleep_snoring_episodes` | count | provider summary | J08.6 |
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

Garmin and WHOOP codes (e.g., `garmin_body_battery`, `garmin_stress`, `whoop_recovery`, `whoop_strain`) belong to [E16](migration-reference.md) and are added there.

## Events

These are typed events with a value or level, not numbers that can be resolved. **Decided in [ADR-0014](../adr/0014-healthkit-contract.md): one `health_events` table** (code, start/end, level or value, context), not one table per family. Implemented codes are in [metrics.md](../metrics.md#events) (`internal/catalog/events.go`); the rows below are not implemented yet.

| Family | Codes | Sources | Phase |
| --- | --- | --- | --- |
| ECG recording | `ecg_recording` (waveform blob, classification, average HR) | HKElectrocardiogram; Withings heart list (no job yet) | later |
| Rhythm results | `afib_ecg_result` (W 130), `afib_ppg_result` (W 139), `irregular_rhythm_alert` | Withings; HK IrregularHeartRhythmEvent | later |
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
4. **Decided (J07.1): one code per body segment.** A segment is a different quantity, not a context of one, and rules and windows see only codes. The codes are `<quantity>_<segment>` (the row above) and arrive with the first connector that reports segments.
