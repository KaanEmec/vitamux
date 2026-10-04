# Resolution defaults (suggestions)

Suggested built-in rules for the most common metrics and the ten most common tracker brands. This is the input for [J09.2](../plan/E09-resolution/J09.2-rules-storage-defaults.md). The rule language is in [resolution.md](resolution.md), and the codes in [metrics.md](../metrics.md) and [metric-catalog.md](metric-catalog.md).

**These are suggestions, not policy.** A built-in default is only the starting point when the owner has not configured a metric:

- the owner can reorder, replace, or delete any group; switch the strategy; or use `single_source` for their preferred device;
- the first edit copies the built-in into a user rule version ([resolution.md](resolution.md#selectors-and-validation)), and per-window manual overrides still apply on top;
- the owner's own comparison beats this table. Fit and placement change optical accuracy a lot: a strap worn snugly on the upper arm can beat a loose wrist watch of a "higher" tier.

The generated [`docs/resolution-defaults.md`](../resolution-defaults.md) lists the defaults that actually ship; [differences](#differences-in-the-shipped-built-ins) from this table are recorded below. This file keeps the reasoning and evidence behind them. The figures here are for contributors. The UI shows each built-in's one-line `Why` (the `reason` field of the rules API) and that the owner can reorder or replace it, never a judgement about the owner's device.

## Principles

1. **Validated evidence beats market share.** Ladders follow independent tests against reference instruments: PSG, ECG or chest strap, lab CPET, validated BP protocols, DEXA.
2. **Vendor-funded evidence is marked (V)** and never ranks a brand above one with comparable independent evidence.
3. **Rank by device type where brand evidence is missing.** For steps and energy, the device type is supported by evidence and the brand order is not.
4. **Tiers are per validated generation.** A newer, unvalidated model inherits its predecessor's tier, marked "(inherited)". Rules work at brand level, so this is information for contributors, not a selector.
5. **`first_available` ladders by default.** They are reproducible and easy to explain. `mean` is used only where sources share one definition and similar accuracy, and the owner can switch it on.
6. **Never pool different definitions.** Resting HR, nightly HRV windows, temperature baselines, BMR models and provider scores differ by vendor. These metrics are selection-only ([metric-catalog.md](metric-catalog.md#rules)).
7. **Consistency beats point accuracy** for metrics where every device is inaccurate, such as energy. One source per day is better than a blend.
8. **One event comes from one source.** A sleep night (all sleep codes together) or a workout is never spliced across sources.
9. **Devices that aren't connected are skipped.** A ladder naming ten brands works for an owner who has one.

## How brands are selected

- **Direct connector or Apple Health.** In the MVP, most brands arrive through the Apple Health bridge (E15). A brand group therefore matches either path: `[{provider: <brand>}, {provider: apple_health, origin_key: <brand bundle id>}]`. [J15.1](../plan/E15-apple-health/J15.1-platform-contract.md) records the real bundle ids as named origin sets. Defaults reference those sets, not hard-coded ids.
- **Named groups ([J09.11](../plan/E09-resolution/J09.11-brand-device-selectors.md)).** The built-ins use the same choices as the rule builder:
  - `apple_watch` and `iphone` are Apple's own measurements (`origin_key_prefix: com.apple.health`) on a device with `device_manufacturer: "Apple Inc."` and `device_model` `Watch` or `iPhone`, whatever type the owner gave the device;
  - `apple` is all of Apple's own apps and devices;
  - `<brand>` is the direct connector;
  - `<brand>_apple` is the brand's app relaying into Apple Health (Garmin, Oura, Withings, WHOOP, Polar, Fitbit, from the `known_relay_origins` seed). For Garmin and Withings, whose HealthKit manufacturer string is verified, it also matches `{provider: apple_health, device_manufacturer: <Brand>}`.
- **What each brand actually writes to Apple Health limits the ladders.**
  - Oura and WHOOP don't write HRV there, and WHOOP writes HR only inside sleep and workouts ([Apple forum](https://developer.apple.com/forums/thread/784797), [Terra](https://tryterra.co/blog/whoop-syncs-health-data-to-apple-health-ee298d328f41)). Nightly RMSSD therefore needs their direct connectors (E19 or later).
  - Fitbit/Pixel write to Apple Health since Google Health 5.05, August 2026 ([TechRepublic](https://techrepublic.com/article/news-fitbit-apple-health-sync)). HRV is not confirmed.
  - Samsung has no path in the MVP: Samsung Health syncs to Android Health Connect only. It stays in the ladders for a future connector.
- **Chest straps and arm bands** only show up as their own device type once the E15 normalizer maps `HKDevice` to `device_type`, or when a direct Polar or Garmin connector exists. When a Watch workout records HR from a paired strap, HealthKit may attribute that HR to the Watch. Until then these groups are inert, and the ladder simply starts at the next group.

## Evidence tiers by brand

Tiers: **A** = validated, good · **B** = acceptable or mixed · **C** = weak or poor · **?** = no independent evidence (treated as C) · **–** = not offered · **(V)** = vendor-funded evidence only.

| Brand | HR exercise | HR daytime | RHR | HRV RMSSD | Sleep stages | Sleep total | Steps | Energy | VO2max | SpO2 | Resp. rate | Temp dev. | BP | Weight / comp | ECG/AFib |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| Apple | A | A | B | C (SDNN) | B | B | A | C | B | A | ? | ? | – | – | A |
| Samsung | A | ? | ? | ? | C (V) | C (V) | B | B | ? | B | B (V) | ? | C (cuffless) | – | A |
| Garmin | B | A | ? | B | C | C | ? | C | B | C | ? | ? | – | B / C | ? |
| Google Pixel / Fitbit | A | A | ? | ? | B | B | C | C | – | ? | ? | ? | – | – | C |
| Xiaomi | C | C | ? | ? | C | C | ? | ? | ? | ? | ? | ? | – | – | ? |
| Amazfit | ? | ? | ? | ? | ? | ? | ? | ? | ? | ? | ? | ? | – | – | ? |
| Oura | – | C | A | A | A (V) | A | B | C | ? | C | B (V) | B (V) | – | – | – |
| WHOOP | B | B | A | A | C | C | ? | ? | – | C | ? | ? | – | – | – |
| Polar | A+ (H10) / B wrist | C | B | C | ? | ? | ? | ? | C | – | – | – | – | – | – |
| Withings | – | B | ? | – | C watch / B mattress | C watch / B mattress | ? | C | – | B | ? | ? | A (cuffs) | A / C | C |

Validated generations: Apple Watch up to Series 9 / Ultra 2 · Pixel Watch 2, Fitbit Charge 6, Sense 2 · Galaxy Watch 5/6 · Garmin Vivoactive 5, Fenix 6, Forerunner 245/945 · Oura Gen 3/4 · WHOOP 4.0 · Polar Grit X Pro, Ignite 3, H10 · ScanWatch 1. Newer models are "(inherited)". Forum reports describe WHOOP 5.0 HR as worse than 4.0 ([WHOOP community](https://www.community.whoop.com/t/whoop-5-0-hr-inaccurate-much-worse-than-4-0/2381), anecdotal), so inheritance is a working assumption, not a fact.

Brand set: global and US/EU shipment share 2025–26 (IDC, Counterpoint), limited to brands widely sold in the US/EU. Huawei ranks high globally but sells mostly in China; add it with its own evidence.

## Suggested defaults

Strategy is `first_available` over the listed ladder unless stated otherwise. Relayed origins are excluded whenever the relaying provider is connected directly. **[E#]** marks an [extension](resolution.md#extensions): E1, E2, E3, E5 and E9 are planned for the MVP ([J09.10](../plan/E09-resolution/J09.10-rule-extensions.md)). E4 and E6–E8 are proposed, and until one exists, the plain rule applies. **Phase** says when a built-in can ship: `v1` codes are implemented; for the others, the default lands with the code.

| Metric | Window | Strategy and gates | Suggested ladder | Why | Phase |
|---|---|---|---|---|---|
| `heart_rate` | 5-min bucket | first_available · `plausible_range: [25, 230]` · `exclude_flags: [manual_entry]` | chest_strap › arm_band › Apple Watch › Garmin › Pixel/Fitbit › Samsung › WHOOP › Polar wrist › Xiaomi/Amazfit › Oura | Chest straps are ECG-class. Wrist MAPE ≈ 5.5–8% for Fitbit Charge 6, Pixel Watch 2, Garmin Vivoactive 5 and Apple Watch SE; 11–15% for Polar wrist, Xiaomi and Oura Gen 3, over a protocol that included exercise. Samsung and WHOOP were not in that study. Fuller 2020 names Apple and Garmin most accurate. WHOOP sits mid-ladder for lack of independent data; worn on the upper arm, many owners find it more reliable than wrist watches during workouts, and moving it up is a one-step edit | v1 |
| Workout HR | inside aligned workouts | **[E1]** `contexts.workout` | chest_strap › arm_band › the device that recorded the workout › rest of the HR ladder | Avoids blending other devices' motion-affected PPG into a workout | v1 |
| `resting_heart_rate` | local_day · `max_staleness: 36h` | first_available (selection only) · warning `definition_changed` when the selected group changes | Oura › WHOOP › Polar › Apple › Garmin › Pixel/Fitbit › Samsung | Nightly RHR MAPE: Oura 1.7–1.9%, Polar 2.7%, WHOOP 3.0% (Dial 2025); Apple 5.9% (O'Grady 2024). Garmin and Fitbit have no independent RHR figure. Definitions differ, so `resting_heart_rate_nocturnal` is the comparable headline value | v1 |
| `resting_heart_rate_nocturnal` (derived) | local_night | **[E2]** lowest 30-min mean of `heart_rate` in the main episode, from the HR ladder with its sleep context · `min_coverage: 0.7` of 5-min buckets in the episode | — | One definition across brands. The coverage gate stops sparse night HR from producing a fake minimum | J09.10 |
| `hrv_rmssd_nightly` | local_day · `max_staleness: 36h` | first_available (selection only) | Oura › WHOOP › Garmin › Polar › others | CCC vs ECG: Oura 0.97–0.99, WHOOP 0.94, Garmin 0.87, Polar 0.82 (Dial 2025). No Fitbit or Samsung evidence. Arrives only from direct connectors | v1 |
| `hrv_rmssd` (samples) | local_night | first_available | same ladder | For sources that send 5-min samples. Never combined with `hrv_rmssd_nightly` | v1 |
| `hrv_sdnn` | local_day | single_source | Apple | A separate method (spot SDNN, MAPE ≈ 29%). Never mixed with RMSSD | v1 |
| `steps` | local_day · **[E9]** `compose: {from: hour, op: first_available}` | **[E3]** `require_wear: heart_rate` · `min_coverage: 0.6` | watch › ring › band › phone | Free-living MAPE vs research accelerometers: Apple 2–6%, Oura 6%, Samsung 10.5%, Fitbit up to +18%, phones ≈ 30% (Hong 2024, Miwa 2026). Several watch brands share the `watch` group, where the existing per-bucket max applies, so they are never added together. Hourly `max` instead of `first_available` is an owner option | v1 |
| `distance_walk_run` | as steps | as steps; **[E1]** inside workouts, the device that recorded the workout | watch › phone › ring | GPS workouts are 3–6% off. Outside workouts, distance follows the step source | v1 |
| `active_energy` | local_day | first_available · `min_coverage: 0.8` (wear-based) | watch › band › ring › phone | Every brand is 19–100% off (Murakami 2019, Fuller 2020). One source per day for consistency; there is no evidence for a brand order. **[E4]** later | v1 |
| `basal_energy`, `total_energy` | local_day | **[E5]** `follow: active_energy` | — | BMR models differ, and active energy is never added to a total | E15 / J08.6 |
| Exercise / intensity minutes | local_day | single_source per provider code | — | Apple exercise time, Garmin intensity minutes and Fitbit Active Zone Minutes have different definitions | later |
| `vo2max` | latest · `max_staleness: 30d` | first_available | Garmin › Apple › Polar › Samsung | Garmin studies mostly 5–10% MAPE, one 15.8%. Apple 13.3%, about 6 mL/kg/min low. Polar overestimated by about 10 in one study. Fitbit's value is a provider score, not `vo2max` | v1 |
| Workouts | workout cluster | event_priority | the watch that recorded the session › other watch › phone | Overlapping workouts cluster, with alternates listed. "Has GPS" is not a selector | v1 |
| Sleep (all sleep codes, one rule) | local_night · `match_overlap: 0.5` · `min_episode_coverage: 0.7` · `max_staleness: 36h` | event_priority on the sleep family | Oura › Apple › Pixel/Fitbit › Withings under-mattress › Samsung › WHOOP › Garmin › Polar › Xiaomi/Amazfit | Independent four-stage kappa: Apple S8 0.53, Fitbit 0.41–0.42, WHOOP 4.0 0.37, ScanWatch 0.22, Vivosmart 4 0.21 (Schyvens 2025). Mattress 0.49 with good totals. Oura 0.65 and Fitbit 0.55 come from an Oura-funded study (V); Oura's meta-analysis TST bias is about 0. A watch on the charger fails the coverage gate | v1 |
| Naps | sleep_episode | event_priority | as sleep | Only naps that don't overlap the main sleep count | v1 |
| `spo2` | local_night | first_available, nightly mean · derived `spo2_night_min` **[E2]** | Apple › Samsung › Withings › Garmin › Pixel/Fitbit › Oura/WHOOP | Apple RMSE 2.9%, Garmin Venu 2s 6.7% (Jiang 2023). Apple, Samsung and Withings RMSD ≤ 4% (Walzel 2023). Ring values are a trend only. A failed reading is not 0% | v1 |
| `respiratory_rate` | local_night | first_available | Samsung › Oura › WHOOP › Apple › Pixel/Fitbit › Garmin | All published evidence is vendor-funded (Samsung PSG study, Oura); low confidence across the board | v1 |
| `sleep_temperature_deviation` | — | no built-in; onboarding asks the owner to pick one source | — | Each provider has its own baseline, and providers agree weakly | later |
| `sleeping_heart_rate` | — | no built-in until the code exists | — | `resting_heart_rate_nocturnal` covers the MVP | later |
| `bp_systolic` / `bp_diastolic` / `bp_pulse` | reading; local_day = mean of that day's readings | first_available · readings stay coherent | `bp_monitor` › watch with micro-cuff › `entry: manual` | Validated cuffs first ([validatebp.org](https://www.validatebp.org)). The 2025 AHA/ACC guideline does not recommend cuffless devices for diagnosis or management (COR 3); their values use separate `bp_*_estimated` codes | v1 |
| `weight` | local_day | first_available, latest reading (**[E8]** first morning reading later) | scale › scale apps via Apple Health › `entry: manual` | Scales agree within 0–0.3 kg vs DEXA. The brand order is the owner's choice | v1 |
| Body fat, muscle, water, bone | reading / local_day | **[E5]** `follow: weight` | — | Same scale as that day's weight. Fat mass is 2–4 kg off vs DEXA, and each vendor's model differs. Labelled as an estimate | v1 |
| `bmi` | — | reported by the provider only | — | Vitamux computes BMI on read and never stores it | v1 |
| ECG and AFib results | events | none: all results listed | — | Inconclusive is its own category. BASEL sensitivity: Apple ≈ Samsung 85% › Fitbit 66% › Withings 58%, with 17–26% inconclusive. Garmin was not tested | later |
| `blood_glucose` | 5-min bucket | first_available | `cgm` › `glucose_meter` | CGM for the series; fingersticks stay selectable | E15 |
| Provider scores | local_day | stay with their own provider | — | Never pooled ([metric-catalog.md](metric-catalog.md#provider-namespaced-scores)) | with connector |

## Differences in the shipped built-ins

J09.2 encodes the table above in `internal/resolve/builtin.go`, with these deliberate differences:

- **Relays.** A brand whose app relays into Apple Health is two adjacent groups, `<brand>` then `<brand>_apple`, instead of one group with two selectors. The direct path wins whenever it has a valid value, so the relayed copy is used only when the direct connector is absent or silent; this is the static form of "relayed origins are excluded when the relaying provider is connected". Until J15.1, the only relay ids are the `known_relay_origins` seed (Garmin Connect, Oura, Withings, WHOOP, Polar, Fitbit). `hrv_rmssd` and `hrv_rmssd_nightly` keep WHOOP, Polar and Fitbit direct only, since they don't write HRV to Apple Health.
- **Relayed wearables in device-type ladders** (`builtin:<metric>:2` of `steps`, `distance_walk_run`, `active_energy`). The `watch`, `ring` and `band` groups take only direct rows (`relayed: false`), each followed by `<type>_relayed`. In v1 a Garmin watch relayed through Apple Health shared the `watch` group with the direct Garmin watch, and the per-bucket max of the two copies inflated the day (23,948 instead of 18,676 steps on the 2025-03-30 fixture). In v3 `iphone` comes before the generic `phone`.
- **Brands without a connector** (`oura`, `fitbit`, `samsung`, `polar`, `xiaomi`, `amazfit`) use placeholder provider codes; their direct groups stay empty until the connector lands, while their `_apple` relay groups already take Apple Health data. Ties such as Xiaomi/Amazfit or Oura/WHOOP are adjacent groups in the listed order. "Others" for `hrv_rmssd_nightly` means Fitbit, then Samsung.
- **Apple** is matched by the native origin prefix `com.apple.health` (`apple`), with Apple's manufacturer and model for `apple_watch` and `iphone`, so relayed or third-party HealthKit data never lands there. "Scale apps via Apple Health" is any measured (non-manual) Apple Health weight.
- **Blood pressure** uses `window: local_day` with `statistic: mean`; reading requests reuse the same ladder.
- **Under-mattress** also matches `device_type: sleep_monitor`, which the Withings normalizer emits.
- **Codes the table does not list:** `pulse_wave_velocity` and `vascular_age` take Withings first; `body_temperature` and `height` take the newest reading (`latest`), measured before manual. Every `body_composition` code follows `weight` (E5), including visceral fat, fat-free mass, cellular water and BMR. `skin_temperature` has no built-in: the value depends on where the device is worn, so the owner picks a source.
- **Derived codes (E2)** read the `heart_rate` and `spo2` ladders unchanged; the HR built-in has no sleep context, so nothing is reordered at night.
- **Not yet encoded:** codes not in the catalogue, workouts (no catalogue metric yet) and naps (a `sleep_episode` request on the sleep rule) get no separate built-in.

## Review policy

- Device algorithms change, and accuracy changes with them. Review the ladders once a year and when a major algorithm update ships. Record the date and sources here.
- A change to a built-in creates a new `builtin:<metric>:<n>`. Owners who copied the old version keep their own rule.
- When a cell moves from `?` or "(inherited)" to evidence, link the source.

## Evidence

Gathered 2026-10 and checked by an adversarial review (abstracts verified through Europe PMC). (V) = vendor-funded or vendor-authored.

- Cross-brand review, HR/steps/energy: Fuller 2020 — https://pmc.ncbi.nlm.nih.gov/articles/PMC7509623/
- Apple Watch living meta-analysis (82 studies): Lambe 2026 — https://www.nature.com/articles/s41746-025-02238-1
- HR in ten wearables: Gielen 2026 — https://pmc.ncbi.nlm.nih.gov/articles/PMC12912460/
- Nightly RHR and HRV vs Polar H10 (Oura, WHOOP, Polar; Garmin excluded from RHR): Dial 2025 — https://pmc.ncbi.nlm.nih.gov/articles/PMC12367097
- Apple RHR and HRV vs H10: O'Grady 2024 — https://pmc.ncbi.nlm.nih.gov/articles/PMC11478500/
- Sleep vs PSG, independent: Schyvens 2025 — https://academic.oup.com/sleepadvances/article/6/2/zpaf021/8090472 · Chinoy 2021 — https://europepmc.org/article/PMC/PMC8120339
- Sleep vs PSG, Oura-funded (V): Robbins 2024 — https://pmc.ncbi.nlm.nih.gov/articles/PMC11511193/ · Oura meta-analysis, Khan 2025 — https://pmc.ncbi.nlm.nih.gov/articles/PMC12602993/
- Sleep, Galaxy Watch 3 (V): https://www.e-jsm.org/journal/view.php?doi=10.13078/jsm.230004 · Google algorithm update (V): https://research.google/pubs/performance-analysis-of-updated-sleep-tracking-algorithms-across-google-and-fitbit-wearable-devices/
- Under-mattress sleep: https://humanfactors.jmir.org/2026/1/e77033
- Steps: Hong 2024 — https://pmc.ncbi.nlm.nih.gov/articles/PMC11281039/ · Miwa 2026 — https://pmc.ncbi.nlm.nih.gov/articles/PMC12928483/ · Oura, Kristiansson 2023 — https://pmc.ncbi.nlm.nih.gov/articles/PMC9950693
- Energy vs doubly labelled water and metabolic chamber: Murakami 2019 — https://pmc.ncbi.nlm.nih.gov/articles/PMC6696858/
- GPS distance, eight sport watches: https://pmc.ncbi.nlm.nih.gov/articles/PMC7381051/
- VO2max: Apple — https://pmc.ncbi.nlm.nih.gov/articles/PMC12080799/ · Garmin review, Železnik Mežan 2025 — https://pmc.ncbi.nlm.nih.gov/articles/PMC12748164/
- SpO2: Jiang 2023 — https://pmc.ncbi.nlm.nih.gov/articles/PMC10337940/ · Walzel 2023 — https://pmc.ncbi.nlm.nih.gov/articles/PMC10674783 · skin-tone meta-analysis — https://jmir.org/article/export/bib/jmir_v26i1e62769
- Respiratory rate, Galaxy Watch (V): Jung 2023 — https://pmc.ncbi.nlm.nih.gov/articles/PMC10536355/
- Temperature and cycle (V): Maijala 2019 — https://pubmed.ncbi.nlm.nih.gov/31783840/ · https://jmir.org/2025/1/e60667
- ECG/AFib, BASEL: Mannhart 2023 — https://pubmed.ncbi.nlm.nih.gov/36858690/
- Smart scales vs DEXA: Frija-Masson 2021 — https://pubmed.ncbi.nlm.nih.gov/33929337/
- BP validation registry: https://www.validatebp.org
