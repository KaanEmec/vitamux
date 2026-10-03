# Analyte catalogue (draft)

Seed list for the `analytes` and `analyte_aliases` tables used by lab results ([lab-documents.md](lab-documents.md)). This is the input for [J12.5](../plan/E12-lab-documents/J12.5-analytes.md). Device and wearable metrics live in [metric-catalog.md](metric-catalog.md).

## Rules

- The catalogue only normalizes data. It never interprets values, and it stores no reference ranges: ranges come from the printed report.
- The original label, value text, unit, range and flag are always kept. A canonical value exists **only** when the analyte has a conversion for the source unit, as in the factor column below.
- An unknown analyte or unit stays confirmable with its original values only.
- Codes match `^[a-z][a-z0-9_]*$`. The specimen is part of the code only when the specimen changes the meaning, e.g. `urine_glucose` vs `glucose`.
- A computed ratio or index (HOMA-IR, non-HDL, A/G ratio) is stored only when printed on the report. Vitamux never computes and stores one.
- LOINC is an optional field. It is filled only from a verified loinc.org lookup during J12.5, never guessed.
- Factor = canonical value per source unit. An affine conversion (offset plus factor) is marked **affine**. An analyte that has **no conversion** keeps one code per unit system.

## Haematology

| Code | Analyte | Canonical | Other units → factor |
| --- | --- | --- | --- |
| `wbc` | White blood cells | 10⁹/L | 10³/µL ×1 |
| `rbc` | Red blood cells | 10¹²/L | 10⁶/µL ×1 |
| `hemoglobin` | Haemoglobin | g/L | g/dL ×10 · mmol/L ×16.11 |
| `hematocrit` | Haematocrit | % | L/L ×100 |
| `mcv` | Mean corpuscular volume | fL | — |
| `mch` | Mean corpuscular haemoglobin | pg | — |
| `mchc` | MCH concentration | g/L | g/dL ×10 |
| `rdw_cv`, `rdw_sd` | Red cell distribution width | %, fL | — |
| `platelets` | Platelets | 10⁹/L | 10³/µL ×1 |
| `mpv` | Mean platelet volume | fL | — |
| `neutrophils_abs`, `lymphocytes_abs`, `monocytes_abs`, `eosinophils_abs`, `basophils_abs` | Differential, absolute | 10⁹/L | 10³/µL ×1 |
| `neutrophils_pct`, `lymphocytes_pct`, `monocytes_pct`, `eosinophils_pct`, `basophils_pct` | Differential, percent | % | — |
| `reticulocytes_pct`, `reticulocytes_abs` | Reticulocytes | %, 10⁹/L | — |
| `nrbc` | Nucleated RBC | /100 WBC | — |
| `esr` | Erythrocyte sedimentation rate | mm/h | — |

## Electrolytes and kidney

| Code | Analyte | Canonical | Other units → factor |
| --- | --- | --- | --- |
| `sodium`, `potassium`, `chloride`, `bicarbonate` | Electrolytes | mmol/L | mEq/L ×1 |
| `anion_gap` | Anion gap (printed) | mmol/L | mEq/L ×1 |
| `urea` | Urea | mmol/L | mg/dL urea ×0.1665 |
| `bun` | Blood urea nitrogen | mmol/L | mg/dL ×0.357 |
| `creatinine` | Creatinine | µmol/L | mg/dL ×88.42 |
| `egfr` | eGFR (printed, equation kept as text) | mL/min/1.73m² | — |
| `cystatin_c` | Cystatin C | mg/L | — |
| `calcium` | Calcium, total | mmol/L | mg/dL ×0.2495 |
| `calcium_ionized` | Calcium, ionized | mmol/L | mg/dL ×0.2495 |
| `magnesium` | Magnesium, serum | mmol/L | mg/dL ×0.4114 · mEq/L ×0.5 |
| `phosphate` | Phosphate | mmol/L | mg/dL ×0.3229 |
| `uric_acid` | Uric acid | µmol/L | mg/dL ×59.48 |

## Liver and proteins

| Code | Analyte | Canonical | Other units → factor |
| --- | --- | --- | --- |
| `alt`, `ast`, `alp`, `ggt`, `ldh` | Enzymes | U/L | IU/L ×1 · µkat/L ×60 |
| `bilirubin_total`, `bilirubin_direct`, `bilirubin_indirect` | Bilirubin | µmol/L | mg/dL ×17.1 |
| `total_protein`, `albumin`, `globulin` | Proteins | g/L | g/dL ×10 |
| `ag_ratio` | Albumin/globulin ratio (printed) | ratio | — |

## Lipids

| Code | Analyte | Canonical | Other units → factor |
| --- | --- | --- | --- |
| `cholesterol_total`, `ldl_c`, `hdl_c`, `non_hdl_c`, `vldl_c` | Cholesterol fractions | mmol/L | mg/dL ×0.02586 |
| `ldl_c_direct` | LDL, directly measured (calculated LDL stays `ldl_c`, with the method in context) | mmol/L | mg/dL ×0.02586 |
| `triglycerides` | Triglycerides | mmol/L | mg/dL ×0.01129 |
| `apob`, `apoa1` | Apolipoproteins | g/L | mg/dL ×0.01 |
| `lpa_molar` | Lipoprotein(a), molar | nmol/L | **no conversion** |
| `lpa_mass` | Lipoprotein(a), mass | mg/dL | mg/L ×0.1; **no conversion** to nmol/L |
| `ldl_p` | LDL particle number | nmol/L | — |

## Glucose metabolism

| Code | Analyte | Canonical | Other units → factor |
| --- | --- | --- | --- |
| `glucose` | Glucose, serum/plasma (fasting state kept in context) | mmol/L | mg/dL ×0.05551 |
| `hba1c` | HbA1c | mmol/mol | % (NGSP): **affine** (%−2.15)×10.929 |
| `insulin` | Insulin | pmol/L | µIU/mL ×6.00 (the factor is a convention; record which one was used) |
| `c_peptide` | C-peptide | nmol/L | ng/mL ×0.331 |
| `fructosamine` | Fructosamine | µmol/L | — |
| `homa_ir` | HOMA-IR (printed only) | index | — |

## Thyroid

| Code | Analyte | Canonical | Other units → factor |
| --- | --- | --- | --- |
| `tsh` | TSH | mIU/L | µIU/mL ×1 |
| `free_t4` | Free T4 | pmol/L | ng/dL ×12.87 |
| `total_t4` | Total T4 | nmol/L | µg/dL ×12.87 |
| `free_t3` | Free T3 | pmol/L | pg/mL ×1.536 |
| `total_t3` | Total T3 | nmol/L | ng/dL ×0.01536 |
| `reverse_t3` | Reverse T3 | pmol/L | ng/dL ×15.36 |
| `anti_tpo`, `anti_tg` | Thyroid antibodies | IU/mL | kIU/L ×1 |
| `thyroglobulin` | Thyroglobulin | µg/L | ng/mL ×1 |

## Iron, vitamins, minerals

| Code | Analyte | Canonical | Other units → factor |
| --- | --- | --- | --- |
| `iron` | Iron, serum | µmol/L | µg/dL ×0.1791 |
| `ferritin` | Ferritin | µg/L | ng/mL ×1 |
| `transferrin` | Transferrin | g/L | mg/dL ×0.01 |
| `tibc`, `uibc` | Iron binding capacity | µmol/L | µg/dL ×0.1791 |
| `transferrin_saturation` | Transferrin saturation | % | — |
| `vitamin_d_25oh` | 25-OH vitamin D | nmol/L | ng/mL ×2.496 |
| `vitamin_b12` | Vitamin B12 | pmol/L | pg/mL ×0.7378 |
| `holotranscobalamin` | Active B12 | pmol/L | — |
| `folate_serum`, `folate_rbc` | Folate | nmol/L | ng/mL ×2.266 |
| `mma` | Methylmalonic acid | nmol/L | — |
| `vitamin_b1`, `vitamin_b6`, `vitamin_a`, `vitamin_e`, `vitamin_k1` | Other vitamins | as printed per lab method | added when seen |
| `zinc`, `copper`, `selenium` | Trace elements | µmol/L | µg/dL ×0.153 (Zn), ×0.157 (Cu); µg/L ×0.01266 (Se) |
| `magnesium_rbc` | RBC magnesium | mmol/L | mg/dL ×0.4114 |
| `omega3_index` | Omega-3 index | % | — |
| `epa_pct`, `dha_pct`, `aa_epa_ratio` | Fatty acids | %, ratio | — |

## Hormones

| Code | Analyte | Canonical | Other units → factor |
| --- | --- | --- | --- |
| `testosterone_total` | Testosterone, total | nmol/L | ng/dL ×0.03467 |
| `testosterone_free` | Testosterone, free | pmol/L | pg/mL ×3.467 |
| `shbg` | SHBG | nmol/L | — |
| `estradiol` | Estradiol | pmol/L | pg/mL ×3.671 |
| `progesterone` | Progesterone | nmol/L | ng/mL ×3.180 |
| `lh`, `fsh` | Gonadotropins | IU/L | mIU/mL ×1 |
| `prolactin` | Prolactin | µg/L | ng/mL ×1 · mIU/L ×0.0472 |
| `dhea_s` | DHEA-S | µmol/L | µg/dL ×0.02714 |
| `amh` | Anti-Müllerian hormone | pmol/L | ng/mL ×7.14 |
| `cortisol` | Cortisol, serum (time of collection kept) | nmol/L | µg/dL ×27.59 |
| `cortisol_saliva` | Cortisol, saliva | nmol/L | — |
| `acth` | ACTH | pmol/L | pg/mL ×0.2202 |
| `igf1` | IGF-1 | nmol/L | ng/mL ×0.1307 |
| `pth` | Parathyroid hormone | pmol/L | pg/mL ×0.106 |
| `androstenedione`, `dht`, `ohp17` | Other androgens | nmol/L | added when seen |

## Inflammation, immunity, coagulation

| Code | Analyte | Canonical | Other units → factor |
| --- | --- | --- | --- |
| `crp`, `hs_crp` | C-reactive protein | mg/L | mg/dL ×10 |
| `il6` | Interleukin-6 | ng/L | pg/mL ×1 |
| `fibrinogen` | Fibrinogen | g/L | mg/dL ×0.01 |
| `iga`, `igg`, `igm` | Immunoglobulins | g/L | mg/dL ×0.01 |
| `ige_total` | IgE, total | kU/L | IU/mL ×1 |
| `complement_c3`, `complement_c4` | Complement | g/L | mg/dL ×0.01 |
| `pt`, `aptt` | Clotting times | s | — |
| `inr` | INR | ratio | — |
| `d_dimer` | D-dimer (unit type kept: FEU or DDU) | mg/L FEU | µg/mL FEU ×1; **no conversion** between FEU and DDU |

## Cardiac and muscle

| Code | Analyte | Canonical | Other units → factor |
| --- | --- | --- | --- |
| `nt_probnp` | NT-proBNP | ng/L | pg/mL ×1 |
| `bnp` | BNP | ng/L | pg/mL ×1 |
| `troponin_i_hs`, `troponin_t_hs` | hs-troponin | ng/L | pg/mL ×1 |
| `homocysteine` | Homocysteine | µmol/L | — |
| `ck`, `ck_mb` | Creatine kinase | U/L | µkat/L ×60 |

## Autoimmune, serology, tumour markers, other (mostly qualitative or titres)

Usually `value_text` plus a comparator. A numeric value is stored only when printed.

| Code | Analyte | Canonical |
| --- | --- | --- |
| `ana` | Antinuclear antibodies (titre and pattern kept as text) | titre |
| `rf` | Rheumatoid factor | IU/mL |
| `anti_ccp` | Anti-CCP | U/mL |
| `ttg_iga` | Tissue transglutaminase IgA | U/mL |
| `anti_dsdna` | Anti-dsDNA | IU/mL |
| `psa_total`, `psa_free` | PSA | µg/L (= ng/mL) |
| `psa_free_ratio` | Free/total PSA (printed) | % |
| `cea`, `afp` | Tumour markers | µg/L |
| `ca125`, `ca199` | Tumour markers | kU/L (= U/mL) |
| `hiv_ag_ab`, `hbsag`, `anti_hbs`, `anti_hcv`, `syphilis_ab`, `rubella_igg`, `varicella_igg` | Serology | qualitative / IU/mL |
| `amylase`, `lipase` | Pancreatic enzymes | U/L |
| `calprotectin_fecal` | Faecal calprotectin | µg/g |
| `lead_blood`, `mercury_blood` | Heavy metals | µg/L |
| `blood_group` | ABO and Rh | text |
| `apoe_genotype` | ApoE genotype | text |

## Urine

| Code | Analyte | Canonical |
| --- | --- | --- |
| `urine_color`, `urine_clarity` | Appearance | text |
| `urine_specific_gravity` | Specific gravity | ratio |
| `urine_ph` | pH | pH |
| `urine_protein`, `urine_glucose`, `urine_ketones`, `urine_bilirubin`, `urine_urobilinogen`, `urine_blood`, `urine_nitrite`, `urine_leukocyte_esterase` | Dipstick (semi-quantitative text kept) | text / mg/dL as printed |
| `urine_rbc`, `urine_wbc`, `urine_epithelial`, `urine_casts`, `urine_crystals` | Microscopy | /hpf, /lpf, text |
| `urine_albumin`, `urine_creatinine` | Spot urine | mg/L, mmol/L |
| `urine_acr` | Albumin/creatinine ratio | mg/mmol (mg/g ×0.113) |
