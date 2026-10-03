# Metric catalogue (draft)

Default metrics and events for wearable, device and app data. This is the input for [J07.1](../plan/E07-normalization/J07.1-catalogue-units.md). Lab analytes live in [analyte-catalog.md](analyte-catalog.md), and the table shapes in [data-model.md](data-model.md).

Once `internal/catalog` exists, the generated `docs/metrics.md` is the source of truth for implemented codes. This file then keeps only the rules and the codes not yet implemented.

## Rules

- **One code per quantity and method.** Sources combine only when they share a code. Measurements made with different methods get different codes and never share a rule, e.g. `hrv_sdnn` vs `hrv_rmssd`.
- **Device, origin and context are selectors, not codes.** Fingerstick vs CGM glucose, or Watch vs iPhone steps, stay one code each. The difference lives in `device`, `origin` and `context`.
- **Proprietary scores are provider-namespaced** (`<provider>_<name>`) and never pooled across providers. They land in the catalogue together with their connector.
- **Calculated values are never stored as measurements.** BMI, MAP, pulse pressure, time-in-range, GMI and sleep debt come from resolution or views. A value the *provider* reports (e.g., BMI from a scale) is stored as-is with its origin.
- **Canonical units** use SI-style units, kept readable: s for durations, m for distances, kg, kcal, °C, mmol/L, % (0–100). The source value and unit are kept whenever conversion changed the value ([data-model.md](data-model.md#measurements)).
- **Phase:** `v1` = the J07.1 seed (MVP). It covers the Withings measures stream ([J08.3](../plan/E08-withings/J08.3-measures.md)), the fixturegen scenarios ([J04.1](../plan/E04-fixtures-harness/J04.1-fixturegen.md)), and push or file imports. `J08.6` = Withings activity and sleep (post-MVP) · `E15` = needed by the Apple bridge · `later` = backlog, added with the first connector that needs it. A connector that needs a code not yet in the seed adds it in its own job.
- **Withings `meastype` codes** are verified against the official `getmeas` reference ([J08.1](../plan/E08-withings/J08.1-verify-api.md) re-checks them). Measures outside `bp_reading` and `body_composition` (SpO2, temperature, VO2max) are stored as plain samples.

Kinds: `S` sample · `I` interval · `C` cumulative · `D` daily_value. Aggregation values are defined in [resolution.md](resolution.md#within-source-aggregation). HK ids omit the `HKQuantityTypeIdentifier` / `HKCategoryTypeIdentifier` prefix. W = Withings `meastype`.

## Activity

| Code | Unit | Kinds | Aggregation | Apple HK | W | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| `steps` | count | I D | additive | StepCount | | v1 |
| `distance_walk_run` | m | I D | additive | DistanceWalkingRunning | | v1 |
| `distance_cycling` | m | I D | additive | DistanceCycling | | E15 |
| `distance_swimming` | m | I | additive | DistanceSwimming | | E15 |
| `distance_wheelchair` | m | I | additive | DistanceWheelchair | | E15 |
| `distance_rowing`, `distance_paddle`, `distance_skating`, `distance_xc_ski`, `distance_downhill_snow` | m | I | additive | DistanceRowing, DistancePaddleSports, DistanceSkatingSports, DistanceCrossCountrySkiing, DistanceDownhillSnowSports | | later |
| `floors_climbed` | count | I D | additive | FlightsClimbed | | E15 |
| `elevation_gain` | m | I D | additive | | | J08.6 |
| `active_energy` | kcal | I D | additive | ActiveEnergyBurned | | v1 |
| `basal_energy` | kcal | I D | additive | BasalEnergyBurned | | E15 |
| `total_energy` | kcal | D | daily_summary | | | J08.6 |
| `exercise_time` | s | I D | additive | AppleExerciseTime | | E15 |
| `intensity_moderate_time`, `intensity_vigorous_time` | s | I D | additive | | | J08.6 |
| `sedentary_time` | s | D | daily_summary | | | later |
| `move_time` | s | I | additive | AppleMoveTime | | later |
| `stand_time` | s | I | additive | AppleStandTime | | E15 |
| `stand_hours` | count | D | daily_summary | AppleStandHour (category) | | E15 |
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
| `heart_rate` | bpm | S | intensive 5 min | HeartRate | 11 (outside BP) | v1 |
| `resting_heart_rate` | bpm | S D | daily_summary | RestingHeartRate | | v1 |
| `walking_heart_rate` | bpm | D | daily_summary | WalkingHeartRateAverage | | E15 |
| `sleeping_heart_rate` | bpm | D | daily_summary | | | later |
| `heart_rate_recovery_1min` | bpm | S | latest | HeartRateRecoveryOneMinute | | later |
| `hrv_sdnn` | ms | S | intensive 5 min | HeartRateVariabilitySDNN | | v1 |
| `hrv_rmssd` | ms | S D | daily_summary | | | v1 |
| `rr_interval` | s | S | raw series, not resolved | HKHeartbeatSeriesSample | | later |
| `vo2max` | mL/kg/min | S | latest | VO2Max | 123 | v1 |
| `afib_burden` | % | D | daily_summary | AtrialFibrillationBurden | | later |
| `pulse_wave_velocity` | m/s | S | latest | | 91 | v1 |
| `vascular_age` | years | S | latest | | 155 | v1 |
| `perfusion_index` | % | S | intensive 5 min | PeripheralPerfusionIndex | | later |
| `ecg_qrs`, `ecg_pr`, `ecg_qt`, `ecg_qtc` | s | S (proposed group `ecg`) | latest | | 135, 136, 137, 138 | later |

## Blood pressure (group `bp_reading`)

| Code | Unit | Kinds | Aggregation | Apple HK | W | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| `bp_systolic` | mmHg | S | latest | BloodPressureSystolic | 10 | v1 |
| `bp_diastolic` | mmHg | S | latest | BloodPressureDiastolic | 9 | v1 |
| `bp_pulse` | bpm | S | latest | HeartRate in the correlation | 11 | v1 |

Group context: position, arm, cuff, irregular-heartbeat flag, part of an averaged session. MAP and pulse pressure are calculated, never stored.

## Respiration and oxygen

| Code | Unit | Kinds | Aggregation | Apple HK | W | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| `spo2` | % | S | intensive 5 min | OxygenSaturation | 54 | v1 |
| `respiratory_rate` | breaths/min | S | intensive 5 min | RespiratoryRate | | v1 |
| `breathing_disturbances` | events/h | D | daily_summary | AppleSleepingBreathingDisturbances | | E15 |
| `apnea_hypopnea_index` | events/h | D | daily_summary | | | J08.6 |
| `fev1`, `fvc` | L | S | latest | ForcedExpiratoryVolume1, ForcedVitalCapacity | | later |
| `peak_expiratory_flow` | L/min | S | latest | PeakExpiratoryFlowRate | | later |
| `inhaler_uses` | count | I | additive | InhalerUsage | | later |

## Temperature

| Code | Unit | Kinds | Aggregation | Apple HK | W | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| `body_temperature` | °C | S | latest | BodyTemperature | 71, 12 | v1 |
| `skin_temperature` | °C | S | intensive 5 min | | 73 | v1 |
| `sleep_temperature_deviation` | °C (delta from baseline) | D | daily_summary | | | later |
| `wrist_temperature_sleeping` | °C | D | daily_summary | AppleSleepingWristTemperature | | E15 |
| `basal_body_temperature` | °C | S | latest | BasalBodyTemperature | | E15 |

`sleep_temperature_deviation` is a provider delta, not an absolute value, so it never shares a code with absolute temperatures.

## Body composition (group `body_composition`)

| Code | Unit | Kinds | Aggregation | Apple HK | W | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| `weight` | kg | S | latest | BodyMass | 1 | v1 |
| `height` | m | S | latest | Height | 4 | v1 |
| `bmi` | kg/m² | S | latest (provider-reported only) | BodyMassIndex | | E15 |
| `body_fat_ratio` | % | S | latest | BodyFatPercentage | 6 | v1 |
| `fat_mass` | kg | S | latest | | 8 | v1 |
| `fat_free_mass` | kg | S | latest | | 5 | v1 |
| `lean_body_mass` | kg | S | latest | LeanBodyMass | | E15 |
| `muscle_mass` | kg | S | latest | | 76 | v1 |
| `bone_mass` | kg | S | latest | | 88 | v1 |
| `hydration` | kg | S | latest | | 77 | v1 |
| `extracellular_water`, `intracellular_water` | kg | S | latest | | 168, 169 | v1 |
| `visceral_fat_index` | index | S | latest | | 170 | v1 |
| `segment_fat_free_mass`, `segment_fat_mass`, `segment_muscle_mass` | kg (per segment in context) | S | latest | | 173, 174, 175 | later |
| `basal_metabolic_rate` | kcal/day | S | latest | | 226 | v1 |
| `waist_circumference` | m | S | latest | WaistCircumference | | E15 |
| `hip_circumference`, `chest_circumference`, `arm_circumference`, `thigh_circumference` | m | S | latest | | | later |

`lean_body_mass` and `fat_free_mass` stay separate codes until we confirm the providers define them the same way.

## Glucose and metabolism

| Code | Unit | Kinds | Aggregation | Apple HK | W | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| `blood_glucose` | mmol/L | S | intensive 5 min | BloodGlucose | | E15 |
| `insulin_basal`, `insulin_bolus` | IU | I | additive | InsulinDelivery (reason in metadata) | | later |
| `blood_ketones` | mmol/L | S | latest | | | later |
| `blood_lactate` | mmol/L | S | latest | | | later |
| `blood_alcohol` | % | S | latest | BloodAlcoholContent | | later |
| `alcoholic_drinks` | count | I | additive | NumberOfAlcoholicBeverages | | later |

The CGM trend arrow and meal or fasting context belong in `context`. TIR, TAR, TBR, GMI, CV and mean glucose are resolution outputs.

## Nutrition and intake (`I`, additive)

| Code | Unit | Apple HK | Phase |
| --- | --- | --- | --- |
| `diet_energy` | kcal | DietaryEnergyConsumed | E15 |
| `diet_protein`, `diet_carbohydrate`, `diet_fat_total`, `diet_fat_saturated`, `diet_fat_monounsaturated`, `diet_fat_polyunsaturated`, `diet_fiber`, `diet_sugar` | g | Dietary… (same names) | E15 |
| `diet_cholesterol` | mg | DietaryCholesterol | E15 |
| `diet_water` | mL | DietaryWater | E15 |
| `diet_caffeine` | mg | DietaryCaffeine | E15 |
| `diet_sodium`, `diet_potassium`, `diet_calcium`, `diet_magnesium`, `diet_iron`, `diet_zinc`, `diet_phosphorus`, `diet_chloride`, `diet_copper`, `diet_manganese` | mg | Dietary… | later |
| `diet_selenium`, `diet_chromium`, `diet_iodine`, `diet_molybdenum` | µg | Dietary… | later |
| `diet_vitamin_a`, `diet_folate`, `diet_vitamin_b12`, `diet_vitamin_d`, `diet_vitamin_k`, `diet_biotin` | µg | Dietary… | later |
| `diet_vitamin_b6`, `diet_vitamin_c`, `diet_vitamin_e`, `diet_thiamin`, `diet_riboflavin`, `diet_niacin`, `diet_pantothenic_acid` | mg | Dietary… | later |

HealthKit food correlations become a meal group (proposed group kind `meal`), with nutrient rows as members.

## Mobility

| Code | Unit | Kinds | Aggregation | Apple HK | Phase |
| --- | --- | --- | --- | --- | --- |
| `walking_steadiness` | % | S | latest | AppleWalkingSteadiness | E15 |
| `walking_asymmetry` | % | S | intensive 5 min | WalkingAsymmetryPercentage | E15 |
| `walking_double_support` | % | S | intensive 5 min | WalkingDoubleSupportPercentage | E15 |
| `walking_step_length` | m | S | intensive 5 min | WalkingStepLength | E15 |
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

Home urine analysers such as the Withings U-Scan produce time series. These are metrics, not lab analytes. They are linked to [analyte-catalog.md](analyte-catalog.md#urine) only for display.

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
| `sleep_total`, `sleep_in_bed`, `sleep_awake`, `sleep_light`, `sleep_deep`, `sleep_rem`, `sleep_unspecified` | s | stage sums (HK SleepAnalysis values; Withings sleep summary from J08.6) | v1 |
| `sleep_latency`, `sleep_waso` | s | provider summary or stage timeline | v1 |
| `sleep_efficiency` | % | provider summary | v1 |
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

Each score is added together with its connector. They are never pooled across providers, and `mean` is rejected for them.

| Pattern | Examples | Aggregation |
| --- | --- | --- |
| Readiness / recovery | `oura_readiness`, `polar_nightly_recharge`, `fitbit_daily_readiness`, `ultrahuman_recovery` | daily_summary |
| Sleep score | `withings_sleep_score`, `oura_sleep_score`, `eight_sleep_sleep_score` | daily_summary |
| Activity score | `oura_activity_score`, `ultrahuman_movement_index` | daily_summary |
| Stress / energy | `oura_daytime_stress`, `fitbit_stress_management` | daily_summary or intensive |
| Fitness estimates | `fitbit_cardio_fitness`, `withings_nerve_health_score` (W 167), `withings_nerve_response_score` (W 196), `withings_esc` (W 229), `withings_metabolic_age` (W 227) | latest |
| Breathing quality | `withings_breathing_quality` | daily_summary |

Garmin and WHOOP codes (e.g., `garmin_body_battery`, `garmin_stress`, `whoop_recovery`, `whoop_strain`) belong to [E16](migration-reference.md) and are added there.

## Events (proposed storage)

These are typed events with a value or level, not numbers that can be resolved. Today no table holds them, and the MVP needs none. **Open question, decided in [J15.1](../plan/E15-apple-health/J15.1-platform-contract.md):** one `health_events` table (code, start/end, level or value text, context) vs one table per family.

| Family | Codes | Sources | Phase |
| --- | --- | --- | --- |
| ECG recording | `ecg_recording` (waveform blob, classification, average HR) | HKElectrocardiogram; Withings heart list (no job yet) | later |
| Rhythm results | `afib_ecg_result` (W 130), `afib_ppg_result` (W 139), `irregular_rhythm_alert` | Withings; HK IrregularHeartRhythmEvent | later |
| HR alerts | `high_heart_rate_alert`, `low_heart_rate_alert`, `low_cardio_fitness_alert` | HK HighHeartRateEvent, LowHeartRateEvent, LowCardioFitnessEvent | E15 |
| Other alerts | `hypertension_alert`, `sleep_apnea_alert`, `walking_steadiness_alert`, `environment_audio_alert`, `headphone_audio_alert` | HK HypertensionEvent, SleepApneaEvent, AppleWalkingSteadinessEvent, EnvironmentalAudioExposureEvent, HeadphoneAudioExposureEvent | E15 |
| Cycle tracking | `menstrual_flow`, `intermenstrual_bleeding`, `ovulation_test`, `pregnancy_test`, `progesterone_test`, `cervical_mucus`, `sexual_activity`, `contraceptive`, `pregnancy`, `lactation`, cycle-deviation alerts | HK categories with the same names | later |
| Mind | `mindful_session`, `state_of_mind` (valence, labels) | HK MindfulSession, HKStateOfMind | later |
| Hygiene | `handwashing`, `toothbrushing` | HK HandwashingEvent, ToothbrushingEvent | later |
| Symptoms (`symptom_<name>`, severity level) | abdominal_cramps, acne, appetite_changes, bladder_incontinence, bloating, breast_pain, chest_tightness_or_pain, chills, constipation, coughing, diarrhea, dizziness, dry_skin, fainting, fatigue, fever, generalized_body_ache, hair_loss, headache, heartburn, hot_flashes, loss_of_smell, loss_of_taste, lower_back_pain, memory_lapse, mood_changes, nausea, night_sweats, pelvic_pain, rapid_pounding_or_fluttering_heartbeat, runny_nose, shortness_of_breath, sinus_congestion, skipped_heartbeat, sleep_changes, sore_throat, vaginal_dryness, vomiting, wheezing | HK symptom categories | later |
| Medication | `medication_dose` | HKMedicationDoseEvent; manual entry | later |
| Clinical records | allergies, conditions, immunizations, procedures (FHIR resources kept raw) | HK clinical records | later |

## Open questions

Questions 2–4 are decided in [J07.1](../plan/E07-normalization/J07.1-catalogue-units.md); question 1 in [J15.1](../plan/E15-apple-health/J15.1-platform-contract.md).

1. Event storage shape (above).
2. Do Withings `fat_free_mass` (5) and HK `LeanBodyMass` mean the same thing? This decides whether the two can share a code.
3. Should `hrv_rmssd` stay `daily_summary`? Some sources send nightly averages, others 5-min samples. The alternative is splitting it into `hrv_rmssd` (samples) and `hrv_rmssd_nightly`.
4. Segmental body composition: one code per segment, or segment in `context`?
