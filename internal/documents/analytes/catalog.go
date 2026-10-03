package analytes

// Version identifies the catalogue and conversion rules a canonical value was computed with.
// Bump it when a factor changes, so stored canonical values can be traced and recomputed.
const Version = 1

// Analyte is one catalogue entry. Code, Name, Unit and LOINC reach the database; the
// conversions are owned by code.
type Analyte struct {
	Code    string
	Name    string
	Section string // grouping in docs/analytes.md
	// Unit is the canonical unit. Empty means values are kept as printed only (qualitative
	// results, titres, or analytes whose unit depends on the lab method).
	Unit        string
	Conversions []Conversion // other units to the canonical one
	// Refused lists units of a different quantity that must never be converted to Unit,
	// e.g. Lp(a) nmol/L vs mg/dL. Converting them is an error, not an unknown unit.
	Refused []string
	Aliases []string // printed labels besides Name and Code that map to this analyte
	// LOINC is filled only from a verified loinc.org lookup, never guessed (analyte-catalog.md#rules).
	LOINC string
}

// Conversion converts a value in Unit to the canonical unit: canonical = value*Factor + Offset.
// Offset is non-zero only for an affine conversion (HbA1c % NGSP to mmol/mol IFCC).
type Conversion struct {
	Unit   string
	Factor float64
	Offset float64
}

func x(unit string, factor float64) Conversion { return Conversion{Unit: unit, Factor: factor} }

func a(code, name, unit string, conv ...Conversion) Analyte {
	return Analyte{Code: code, Name: name, Unit: unit, Conversions: conv}
}

func (an Analyte) alias(labels ...string) Analyte {
	an.Aliases = append(an.Aliases, labels...)
	return an
}

func (an Analyte) refuse(units ...string) Analyte {
	an.Refused = append(an.Refused, units...)
	return an
}

func section(name string, as ...Analyte) []Analyte {
	for i := range as {
		as[i].Section = name
	}
	return as
}

// Units without a quantity: a value printed without a unit takes them as is.
var unitless = map[string]bool{"ratio": true, "index": true, "pH": true}

// HbA1c is affine: IFCC mmol/mol = (NGSP % - 2.15) * 10.929, so the offset is -2.15 * 10.929.
var hba1c = Conversion{Factor: 10.929, Offset: -23.49735}

func affine(unit string, c Conversion) Conversion { c.Unit = unit; return c }

// enzyme units: U/L with IU/L and µkat/L.
var enzyme = []Conversion{x("IU/L", 1), x("µkat/L", 60)}

// analytes is the catalogue in seed order; ids are positions + 1, so append only.
var analytes = flatten(
	section("Haematology",
		a("wbc", "White blood cells", "10⁹/L", x("10³/µL", 1)).alias("WBC", "Leukocytes", "Leucocytes"),
		a("rbc", "Red blood cells", "10¹²/L", x("10⁶/µL", 1)).alias("RBC", "Erythrocytes"),
		a("hemoglobin", "Haemoglobin", "g/L", x("g/dL", 10), x("mmol/L", 16.11)).alias("Hemoglobin", "Hb", "HGB"),
		a("hematocrit", "Haematocrit", "%", x("L/L", 100)).alias("Hematocrit", "Hct"),
		a("mcv", "Mean corpuscular volume", "fL").alias("MCV"),
		a("mch", "Mean corpuscular haemoglobin", "pg").alias("MCH", "Mean corpuscular hemoglobin"),
		a("mchc", "Mean corpuscular haemoglobin concentration", "g/L", x("g/dL", 10)).alias("MCHC", "Mean corpuscular hemoglobin concentration"),
		a("rdw_cv", "Red cell distribution width, CV", "%").alias("RDW-CV"),
		a("rdw_sd", "Red cell distribution width, SD", "fL").alias("RDW-SD"),
		a("platelets", "Platelets", "10⁹/L", x("10³/µL", 1)).alias("PLT", "Thrombocytes", "Platelet count"),
		a("mpv", "Mean platelet volume", "fL").alias("MPV"),
		a("neutrophils_abs", "Neutrophils, absolute", "10⁹/L", x("10³/µL", 1)),
		a("lymphocytes_abs", "Lymphocytes, absolute", "10⁹/L", x("10³/µL", 1)),
		a("monocytes_abs", "Monocytes, absolute", "10⁹/L", x("10³/µL", 1)),
		a("eosinophils_abs", "Eosinophils, absolute", "10⁹/L", x("10³/µL", 1)),
		a("basophils_abs", "Basophils, absolute", "10⁹/L", x("10³/µL", 1)),
		a("neutrophils_pct", "Neutrophils, percent", "%"),
		a("lymphocytes_pct", "Lymphocytes, percent", "%"),
		a("monocytes_pct", "Monocytes, percent", "%"),
		a("eosinophils_pct", "Eosinophils, percent", "%"),
		a("basophils_pct", "Basophils, percent", "%"),
		a("reticulocytes_pct", "Reticulocytes, percent", "%"),
		a("reticulocytes_abs", "Reticulocytes, absolute", "10⁹/L"),
		a("nrbc", "Nucleated red blood cells", "/100 WBC").alias("NRBC"),
		a("esr", "Erythrocyte sedimentation rate", "mm/h").alias("ESR"),
	),
	section("Electrolytes and kidney",
		a("sodium", "Sodium", "mmol/L", x("mEq/L", 1)).alias("Na"),
		a("potassium", "Potassium", "mmol/L", x("mEq/L", 1)).alias("K"),
		a("chloride", "Chloride", "mmol/L", x("mEq/L", 1)).alias("Cl"),
		a("bicarbonate", "Bicarbonate", "mmol/L", x("mEq/L", 1)).alias("HCO3"),
		a("anion_gap", "Anion gap", "mmol/L", x("mEq/L", 1)),
		a("urea", "Urea", "mmol/L", x("mg/dL", 0.1665)),
		a("bun", "Blood urea nitrogen", "mmol/L", x("mg/dL", 0.357)).alias("BUN"),
		a("creatinine", "Creatinine", "µmol/L", x("mg/dL", 88.42)),
		a("egfr", "eGFR", "mL/min/1.73m²").alias("Estimated glomerular filtration rate"),
		a("cystatin_c", "Cystatin C", "mg/L"),
		a("calcium", "Calcium, total", "mmol/L", x("mg/dL", 0.2495)).alias("Calcium", "Total calcium"),
		a("calcium_ionized", "Calcium, ionized", "mmol/L", x("mg/dL", 0.2495)).alias("Ionized calcium", "Ionised calcium"),
		a("magnesium", "Magnesium, serum", "mmol/L", x("mg/dL", 0.4114), x("mEq/L", 0.5)).alias("Magnesium"),
		a("phosphate", "Phosphate", "mmol/L", x("mg/dL", 0.3229)).alias("Phosphorus"),
		a("uric_acid", "Uric acid", "µmol/L", x("mg/dL", 59.48)).alias("Urate"),
	),
	section("Liver and proteins",
		a("alt", "Alanine aminotransferase", "U/L", enzyme...).alias("ALT", "ALAT", "SGPT"),
		a("ast", "Aspartate aminotransferase", "U/L", enzyme...).alias("AST", "ASAT", "SGOT"),
		a("alp", "Alkaline phosphatase", "U/L", enzyme...).alias("ALP"),
		a("ggt", "Gamma-glutamyl transferase", "U/L", enzyme...).alias("GGT", "Gamma GT"),
		a("ldh", "Lactate dehydrogenase", "U/L", enzyme...).alias("LDH"),
		a("bilirubin_total", "Bilirubin, total", "µmol/L", x("mg/dL", 17.1)).alias("Total bilirubin"),
		a("bilirubin_direct", "Bilirubin, direct", "µmol/L", x("mg/dL", 17.1)).alias("Direct bilirubin", "Conjugated bilirubin"),
		a("bilirubin_indirect", "Bilirubin, indirect", "µmol/L", x("mg/dL", 17.1)).alias("Indirect bilirubin"),
		a("total_protein", "Total protein", "g/L", x("g/dL", 10)).alias("Protein, total"),
		a("albumin", "Albumin", "g/L", x("g/dL", 10)),
		a("globulin", "Globulin", "g/L", x("g/dL", 10)),
		a("ag_ratio", "Albumin/globulin ratio", "ratio").alias("A/G ratio"),
	),
	section("Lipids",
		a("cholesterol_total", "Cholesterol, total", "mmol/L", x("mg/dL", 0.02586)).alias("Total cholesterol", "Cholesterol"),
		a("ldl_c", "LDL cholesterol", "mmol/L", x("mg/dL", 0.02586)).alias("LDL-C", "LDL"),
		a("hdl_c", "HDL cholesterol", "mmol/L", x("mg/dL", 0.02586)).alias("HDL-C", "HDL"),
		a("non_hdl_c", "Non-HDL cholesterol", "mmol/L", x("mg/dL", 0.02586)).alias("Non-HDL-C"),
		a("vldl_c", "VLDL cholesterol", "mmol/L", x("mg/dL", 0.02586)).alias("VLDL-C", "VLDL"),
		a("ldl_c_direct", "LDL cholesterol, direct", "mmol/L", x("mg/dL", 0.02586)).alias("Direct LDL"),
		a("triglycerides", "Triglycerides", "mmol/L", x("mg/dL", 0.01129)).alias("Triglyceride", "TG"),
		a("apob", "Apolipoprotein B", "g/L", x("mg/dL", 0.01)).alias("ApoB", "Apo B"),
		a("apoa1", "Apolipoprotein A1", "g/L", x("mg/dL", 0.01)).alias("ApoA1", "Apo A1", "ApoA-I"),
		a("lpa_molar", "Lipoprotein(a), molar", "nmol/L").refuse("mg/dL", "mg/L"),
		a("lpa_mass", "Lipoprotein(a), mass", "mg/dL", x("mg/L", 0.1)).refuse("nmol/L"),
		a("ldl_p", "LDL particle number", "nmol/L").alias("LDL-P"),
	),
	section("Glucose metabolism",
		a("glucose", "Glucose", "mmol/L", x("mg/dL", 0.05551)).alias("Glucose, serum", "Glucose, plasma"),
		a("hba1c", "HbA1c", "mmol/mol", affine("%", hba1c), affine("% NGSP", hba1c)).
			alias("Haemoglobin A1c", "Hemoglobin A1c", "Glycated haemoglobin", "Glycated hemoglobin", "A1c"),
		a("insulin", "Insulin", "pmol/L", x("µIU/mL", 6.00)),
		a("c_peptide", "C-peptide", "nmol/L", x("ng/mL", 0.331)),
		a("fructosamine", "Fructosamine", "µmol/L"),
		a("homa_ir", "HOMA-IR", "index"),
	),
	section("Thyroid",
		a("tsh", "TSH", "mIU/L", x("µIU/mL", 1)).alias("Thyroid-stimulating hormone", "Thyrotropin"),
		a("free_t4", "Free T4", "pmol/L", x("ng/dL", 12.87)).alias("FT4", "Free thyroxine"),
		a("total_t4", "Total T4", "nmol/L", x("µg/dL", 12.87)).alias("T4, total"),
		a("free_t3", "Free T3", "pmol/L", x("pg/mL", 1.536)).alias("FT3", "Free triiodothyronine"),
		a("total_t3", "Total T3", "nmol/L", x("ng/dL", 0.01536)).alias("T3, total"),
		a("reverse_t3", "Reverse T3", "pmol/L", x("ng/dL", 15.36)).alias("rT3"),
		a("anti_tpo", "Anti-TPO antibodies", "IU/mL", x("kIU/L", 1)).alias("Anti-TPO", "TPO antibodies", "TPOAb"),
		a("anti_tg", "Anti-thyroglobulin antibodies", "IU/mL", x("kIU/L", 1)).alias("Anti-Tg", "TgAb"),
		a("thyroglobulin", "Thyroglobulin", "µg/L", x("ng/mL", 1)),
	),
	section("Iron, vitamins, minerals",
		a("iron", "Iron, serum", "µmol/L", x("µg/dL", 0.1791)).alias("Iron", "Serum iron"),
		a("ferritin", "Ferritin", "µg/L", x("ng/mL", 1)),
		a("transferrin", "Transferrin", "g/L", x("mg/dL", 0.01)),
		a("tibc", "Total iron binding capacity", "µmol/L", x("µg/dL", 0.1791)).alias("TIBC"),
		a("uibc", "Unsaturated iron binding capacity", "µmol/L", x("µg/dL", 0.1791)).alias("UIBC"),
		a("transferrin_saturation", "Transferrin saturation", "%").alias("TSAT"),
		a("vitamin_d_25oh", "25-OH vitamin D", "nmol/L", x("ng/mL", 2.496)).alias("25-hydroxyvitamin D", "Vitamin D, 25-OH", "25(OH)D"),
		a("vitamin_b12", "Vitamin B12", "pmol/L", x("pg/mL", 0.7378)).alias("Cobalamin"),
		a("holotranscobalamin", "Holotranscobalamin", "pmol/L").alias("Active B12"),
		a("folate_serum", "Folate, serum", "nmol/L", x("ng/mL", 2.266)).alias("Serum folate"),
		a("folate_rbc", "Folate, RBC", "nmol/L", x("ng/mL", 2.266)).alias("RBC folate"),
		a("mma", "Methylmalonic acid", "nmol/L").alias("MMA"),
		a("vitamin_b1", "Vitamin B1", ""),
		a("vitamin_b6", "Vitamin B6", ""),
		a("vitamin_a", "Vitamin A", ""),
		a("vitamin_e", "Vitamin E", ""),
		a("vitamin_k1", "Vitamin K1", ""),
		a("zinc", "Zinc", "µmol/L", x("µg/dL", 0.153)),
		a("copper", "Copper", "µmol/L", x("µg/dL", 0.157)),
		a("selenium", "Selenium", "µmol/L", x("µg/L", 0.01266)),
		a("magnesium_rbc", "Magnesium, RBC", "mmol/L", x("mg/dL", 0.4114)).alias("RBC magnesium"),
		a("omega3_index", "Omega-3 index", "%"),
		a("epa_pct", "EPA", "%"),
		a("dha_pct", "DHA", "%"),
		a("aa_epa_ratio", "AA/EPA ratio", "ratio"),
	),
	section("Hormones",
		a("testosterone_total", "Testosterone, total", "nmol/L", x("ng/dL", 0.03467)).alias("Total testosterone", "Testosterone"),
		a("testosterone_free", "Testosterone, free", "pmol/L", x("pg/mL", 3.467)).alias("Free testosterone"),
		a("shbg", "SHBG", "nmol/L").alias("Sex hormone-binding globulin"),
		a("estradiol", "Estradiol", "pmol/L", x("pg/mL", 3.671)).alias("Oestradiol"),
		a("progesterone", "Progesterone", "nmol/L", x("ng/mL", 3.180)),
		a("lh", "Luteinizing hormone", "IU/L", x("mIU/mL", 1)).alias("LH"),
		a("fsh", "Follicle-stimulating hormone", "IU/L", x("mIU/mL", 1)).alias("FSH"),
		a("prolactin", "Prolactin", "µg/L", x("ng/mL", 1), x("mIU/L", 0.0472)),
		a("dhea_s", "DHEA-S", "µmol/L", x("µg/dL", 0.02714)).alias("DHEA sulfate", "DHEA sulphate"),
		a("amh", "Anti-Müllerian hormone", "pmol/L", x("ng/mL", 7.14)).alias("AMH"),
		a("cortisol", "Cortisol, serum", "nmol/L", x("µg/dL", 27.59)).alias("Cortisol"),
		a("cortisol_saliva", "Cortisol, saliva", "nmol/L").alias("Salivary cortisol"),
		a("acth", "ACTH", "pmol/L", x("pg/mL", 0.2202)).alias("Adrenocorticotropic hormone"),
		a("igf1", "IGF-1", "nmol/L", x("ng/mL", 0.1307)),
		a("pth", "Parathyroid hormone", "pmol/L", x("pg/mL", 0.106)).alias("PTH"),
		a("androstenedione", "Androstenedione", "nmol/L"),
		a("dht", "Dihydrotestosterone", "nmol/L").alias("DHT"),
		a("ohp17", "17-OH progesterone", "nmol/L").alias("17-hydroxyprogesterone"),
	),
	section("Inflammation, immunity, coagulation",
		a("crp", "C-reactive protein", "mg/L", x("mg/dL", 10)).alias("CRP"),
		a("hs_crp", "C-reactive protein, high sensitivity", "mg/L", x("mg/dL", 10)).alias("hs-CRP", "hsCRP"),
		a("il6", "Interleukin-6", "ng/L", x("pg/mL", 1)).alias("IL-6"),
		a("fibrinogen", "Fibrinogen", "g/L", x("mg/dL", 0.01)),
		a("iga", "Immunoglobulin A", "g/L", x("mg/dL", 0.01)).alias("IgA"),
		a("igg", "Immunoglobulin G", "g/L", x("mg/dL", 0.01)).alias("IgG"),
		a("igm", "Immunoglobulin M", "g/L", x("mg/dL", 0.01)).alias("IgM"),
		a("ige_total", "IgE, total", "kU/L", x("IU/mL", 1)).alias("Total IgE"),
		a("complement_c3", "Complement C3", "g/L", x("mg/dL", 0.01)).alias("C3"),
		a("complement_c4", "Complement C4", "g/L", x("mg/dL", 0.01)).alias("C4"),
		a("pt", "Prothrombin time", "s").alias("PT"),
		a("aptt", "Activated partial thromboplastin time", "s").alias("aPTT"),
		a("inr", "INR", "ratio"),
		a("d_dimer", "D-dimer", "mg/L FEU", x("µg/mL FEU", 1)).refuse("mg/L DDU", "µg/mL DDU", "µg/L DDU", "ng/mL DDU"),
	),
	section("Cardiac and muscle",
		a("nt_probnp", "NT-proBNP", "ng/L", x("pg/mL", 1)),
		a("bnp", "BNP", "ng/L", x("pg/mL", 1)),
		a("troponin_i_hs", "Troponin I, high sensitivity", "ng/L", x("pg/mL", 1)).alias("hs-Troponin I", "hs-TnI"),
		a("troponin_t_hs", "Troponin T, high sensitivity", "ng/L", x("pg/mL", 1)).alias("hs-Troponin T", "hs-TnT"),
		a("homocysteine", "Homocysteine", "µmol/L"),
		a("ck", "Creatine kinase", "U/L", x("µkat/L", 60)).alias("CK", "CPK"),
		a("ck_mb", "Creatine kinase MB", "U/L", x("µkat/L", 60)).alias("CK-MB"),
	),
	section("Autoimmune, serology, tumour markers, other",
		a("ana", "Antinuclear antibodies", "").alias("ANA"),
		a("rf", "Rheumatoid factor", "IU/mL").alias("RF"),
		a("anti_ccp", "Anti-CCP", "U/mL"),
		a("ttg_iga", "Tissue transglutaminase IgA", "U/mL").alias("tTG-IgA"),
		a("anti_dsdna", "Anti-dsDNA", "IU/mL"),
		a("psa_total", "PSA, total", "µg/L", x("ng/mL", 1)).alias("Total PSA", "PSA"),
		a("psa_free", "PSA, free", "µg/L", x("ng/mL", 1)).alias("Free PSA"),
		a("psa_free_ratio", "Free/total PSA ratio", "%"),
		a("cea", "CEA", "µg/L"),
		a("afp", "AFP", "µg/L"),
		a("ca125", "CA 125", "kU/L", x("U/mL", 1)),
		a("ca199", "CA 19-9", "kU/L", x("U/mL", 1)),
		a("hiv_ag_ab", "HIV antigen/antibody", ""),
		a("hbsag", "HBsAg", ""),
		a("anti_hbs", "Anti-HBs", ""),
		a("anti_hcv", "Anti-HCV", ""),
		a("syphilis_ab", "Syphilis antibodies", ""),
		a("rubella_igg", "Rubella IgG", ""),
		a("varicella_igg", "Varicella IgG", ""),
		a("amylase", "Amylase", "U/L"),
		a("lipase", "Lipase", "U/L"),
		a("calprotectin_fecal", "Calprotectin, faecal", "µg/g").alias("Faecal calprotectin", "Fecal calprotectin"),
		a("lead_blood", "Lead, blood", "µg/L"),
		a("mercury_blood", "Mercury, blood", "µg/L"),
		a("blood_group", "Blood group", "").alias("ABO and Rh"),
		a("apoe_genotype", "ApoE genotype", ""),
	),
	section("Urine",
		a("urine_color", "Urine colour", "").alias("Urine color"),
		a("urine_clarity", "Urine clarity", ""),
		a("urine_specific_gravity", "Urine specific gravity", "ratio"),
		a("urine_ph", "Urine pH", "pH"),
		a("urine_protein", "Urine protein", ""),
		a("urine_glucose", "Urine glucose", ""),
		a("urine_ketones", "Urine ketones", ""),
		a("urine_bilirubin", "Urine bilirubin", ""),
		a("urine_urobilinogen", "Urine urobilinogen", ""),
		a("urine_blood", "Urine blood", ""),
		a("urine_nitrite", "Urine nitrite", ""),
		a("urine_leukocyte_esterase", "Urine leukocyte esterase", ""),
		a("urine_rbc", "Urine red blood cells", "/hpf"),
		a("urine_wbc", "Urine white blood cells", "/hpf"),
		a("urine_epithelial", "Urine epithelial cells", "/hpf"),
		a("urine_casts", "Urine casts", "/lpf"),
		a("urine_crystals", "Urine crystals", ""),
		a("urine_albumin", "Urine albumin", "mg/L"),
		a("urine_creatinine", "Urine creatinine", "mmol/L"),
		a("urine_acr", "Albumin/creatinine ratio, urine", "mg/mmol", x("mg/g", 0.113)).alias("Urine ACR", "UACR"),
	),
)

func flatten(groups ...[]Analyte) []Analyte {
	var out []Analyte
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

var byCode = func() map[string]Analyte {
	m := make(map[string]Analyte, len(analytes))
	for _, an := range analytes {
		m[an.Code] = an
	}
	return m
}()

// Lookup finds an analyte by code.
func Lookup(code string) (Analyte, bool) {
	an, ok := byCode[code]
	return an, ok
}

// All returns the catalogue in seed order.
func All() []Analyte { return append([]Analyte(nil), analytes...) }

// SeedAliases returns the printed labels seeded for an: its name, its code and its aliases,
// without duplicates by LabelKey.
func (an Analyte) SeedAliases() []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range append([]string{an.Name, an.Code}, an.Aliases...) {
		if k := LabelKey(l); !seen[k] {
			seen[k] = true
			out = append(out, l)
		}
	}
	return out
}
