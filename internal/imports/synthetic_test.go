package imports

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The synthetic export generator (T15.8.1). Every value, name and id is made up; the shape
// follows a Health app export: the inline DTD, ExportDate and Me, then Records, a blood-pressure
// Correlation (whose members are also top-level Records, as Apple writes them), a Workout with
// statistics, an event and a route, and an ActivitySummary the importer skips.

const (
	synthWatch = `&lt;&lt;HKDevice: 0x600002a1b2c0&gt;, name:Synthetic Watch, manufacturer:Apple Inc., model:Watch, hardware:Watch7,1, software:11.0&gt;`
	synthPhone = `&lt;&lt;HKDevice: 0x600002a1b3d0&gt;, name:Synthetic Phone, manufacturer:Apple Inc., model:iPhone, hardware:iPhone17,1, software:26.0&gt;`
	synthCuff  = `&lt;&lt;HKDevice: 0x600002a1b4e0&gt;, name:Synthetic Cuff, manufacturer:Synthetic Medical, model:BPM 1&gt;`
)

// synthRecord renders one Record element; children are inner XML.
func synthRecord(typ, source, device, unit, value, start, end, children string) string {
	attrs := fmt.Sprintf(`type=%q sourceName=%q sourceVersion="11.0"`, typ, source)
	if device != "" {
		attrs += ` device="` + device + `"`
	}
	if unit != "" {
		attrs += fmt.Sprintf(` unit=%q`, unit)
	}
	attrs += fmt.Sprintf(` creationDate=%q startDate=%q endDate=%q value=%q`, end, start, end, value)
	if children == "" {
		return "  <Record " + attrs + "/>\n"
	}
	return "  <Record " + attrs + ">\n" + children + "  </Record>\n"
}

func synthMeta(key, value string) string {
	return fmt.Sprintf("   <MetadataEntry key=%q value=%q/>\n", key, value)
}

// syntheticExportXML is a small export.xml covering every mapped record family.
func syntheticExportXML() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE HealthData [
<!-- HealthKit Export Version: 14 -->
<!ELEMENT HealthData (ExportDate,Me,(Record|Correlation|Workout|ActivitySummary|ClinicalRecord|Audiogram|VisionPrescription)*)>
<!ATTLIST HealthData
  locale CDATA #REQUIRED
>
<!ELEMENT ExportDate EMPTY>
<!ATTLIST ExportDate
  value CDATA #REQUIRED
>
<!ELEMENT Record ((MetadataEntry|HeartRateVariabilityMetadataList)*)>
<!ATTLIST Record
  type          CDATA #REQUIRED
  unit          CDATA #IMPLIED
  value         CDATA #IMPLIED
  sourceName    CDATA #REQUIRED
  sourceVersion CDATA #IMPLIED
  device        CDATA #IMPLIED
  creationDate  CDATA #IMPLIED
  startDate     CDATA #REQUIRED
  endDate       CDATA #REQUIRED
>
]>
<HealthData locale="en_US">
 <ExportDate value="2026-09-20 10:00:00 +0200"/>
 <Me HKCharacteristicTypeIdentifierDateOfBirth="" HKCharacteristicTypeIdentifierBiologicalSex="HKBiologicalSexNotSet"/>
`)
	hr := "HKQuantityTypeIdentifierHeartRate"
	b.WriteString(synthRecord(hr, "Synthetic Watch", synthWatch, "count/min", "61", "2026-09-14 07:31:05 +0200", "2026-09-14 07:31:05 +0200",
		synthMeta("HKMetadataKeyHeartRateMotionContext", "1")))
	b.WriteString(synthRecord(hr, "Synthetic Watch", synthWatch, "count/min", "142", "2026-09-14 18:02:00 +0200", "2026-09-14 18:02:00 +0200",
		synthMeta("HKMetadataKeyHeartRateMotionContext", "2")))
	b.WriteString(synthRecord(hr, "Synthetic Watch", synthWatch, "count/min", "55", "2026-09-14 23:40:00 +0200", "2026-09-14 23:40:00 +0200", ""))
	b.WriteString(synthRecord("HKQuantityTypeIdentifierStepCount", "Synthetic Phone", synthPhone, "count", "420",
		"2026-09-14 08:00:00 +0200", "2026-09-14 08:10:00 +0200", ""))
	b.WriteString(synthRecord("HKQuantityTypeIdentifierDistanceWalkingRunning", "Synthetic Phone", synthPhone, "km", "0.31",
		"2026-09-14 08:00:00 +0200", "2026-09-14 08:10:00 +0200", ""))
	b.WriteString(synthRecord("HKQuantityTypeIdentifierBodyMass", "Health", "", "lb", "160.5",
		"2026-09-14 07:00:00 +0200", "2026-09-14 07:00:00 +0200", synthMeta("HKWasUserEntered", "1")))
	b.WriteString(synthRecord("HKQuantityTypeIdentifierHeartRateVariabilitySDNN", "Synthetic Watch", synthWatch, "ms", "48.2",
		"2026-09-14 03:10:00 +0200", "2026-09-14 03:11:00 +0200",
		"   <HeartRateVariabilityMetadataList>\n    <InstantaneousBeatsPerMinute bpm=\"61\" time=\"3:10:01.12 AM\"/>\n   </HeartRateVariabilityMetadataList>\n"))
	sleep := "HKCategoryTypeIdentifierSleepAnalysis"
	for _, s := range [][3]string{
		{"HKCategoryValueSleepAnalysisInBed", "2026-09-14 23:20:00 +0200", "2026-09-15 06:50:00 +0200"},
		{"HKCategoryValueSleepAnalysisAsleepCore", "2026-09-14 23:35:00 +0200", "2026-09-15 01:00:00 +0200"},
		{"HKCategoryValueSleepAnalysisAsleepDeep", "2026-09-15 01:00:00 +0200", "2026-09-15 01:45:00 +0200"},
		{"HKCategoryValueSleepAnalysisAsleepREM", "2026-09-15 01:45:00 +0200", "2026-09-15 02:30:00 +0200"},
		{"HKCategoryValueSleepAnalysisAwake", "2026-09-15 02:30:00 +0200", "2026-09-15 02:35:00 +0200"},
	} {
		b.WriteString(synthRecord(sleep, "Synthetic Watch", synthWatch, "", s[0], s[1], s[2], synthMeta("HKTimeZone", "Europe/Amsterdam")))
	}
	b.WriteString(synthRecord("HKCategoryTypeIdentifierHighHeartRateEvent", "Synthetic Watch", synthWatch, "", "HKCategoryValueNotApplicable",
		"2026-09-14 15:00:00 +0200", "2026-09-14 15:10:00 +0200", synthMeta("HKMetadataKeyHeartRateEventThreshold", "120 count/min")))
	b.WriteString(synthRecord("HKCategoryTypeIdentifierAppleStandHour", "Synthetic Watch", synthWatch, "", "HKCategoryValueAppleStandHourStood",
		"2026-09-14 09:00:00 +0200", "2026-09-14 10:00:00 +0200", ""))
	// A type the mapping does not know is stored raw and reported by the normalizer.
	b.WriteString(synthRecord("HKQuantityTypeIdentifierEnvironmentalAudioExposure", "Synthetic Watch", synthWatch, "dBASPL", "71",
		"2026-09-14 12:00:00 +0200", "2026-09-14 12:30:00 +0200", ""))

	sys, dia := "HKQuantityTypeIdentifierBloodPressureSystolic", "HKQuantityTypeIdentifierBloodPressureDiastolic"
	at := "2026-09-14 07:05:00 +0200"
	b.WriteString(synthRecord(dia, "Synthetic Cuff App", synthCuff, "mmHg", "79", at, at, ""))
	b.WriteString(synthRecord(sys, "Synthetic Cuff App", synthCuff, "mmHg", "121", at, at, ""))
	b.WriteString(`  <Correlation type="HKCorrelationTypeIdentifierBloodPressure" sourceName="Synthetic Cuff App" sourceVersion="3.1" device="` + synthCuff +
		`" creationDate="` + at + `" startDate="` + at + `" endDate="` + at + `">` + "\n" +
		synthMeta("HKTimeZone", "Europe/Amsterdam") +
		strings.ReplaceAll(synthRecord(dia, "Synthetic Cuff App", synthCuff, "mmHg", "79", at, at, ""), "  <Record", "   <Record") +
		strings.ReplaceAll(synthRecord(sys, "Synthetic Cuff App", synthCuff, "mmHg", "121", at, at, ""), "  <Record", "   <Record") +
		"  </Correlation>\n")

	w0, w1 := "2026-09-14 18:00:00 +0200", "2026-09-14 18:30:30 +0200"
	b.WriteString(`  <Workout workoutActivityType="HKWorkoutActivityTypeRunning" duration="30.5" durationUnit="min" sourceName="Synthetic Watch" sourceVersion="11.0" device="` +
		synthWatch + `" creationDate="` + w1 + `" startDate="` + w0 + `" endDate="` + w1 + `">` + "\n" +
		synthMeta("HKIndoorWorkout", "0") + synthMeta("HKTimeZone", "Europe/Amsterdam") +
		`   <WorkoutEvent type="HKWorkoutEventTypeSegment" date="` + w0 + `" duration="5" durationUnit="min"/>` + "\n" +
		`   <WorkoutStatistics type="HKQuantityTypeIdentifierActiveEnergyBurned" startDate="` + w0 + `" endDate="` + w1 + `" sum="310.5" unit="kcal"/>` + "\n" +
		`   <WorkoutStatistics type="HKQuantityTypeIdentifierDistanceWalkingRunning" startDate="` + w0 + `" endDate="` + w1 + `" sum="5.2" unit="km"/>` + "\n" +
		`   <WorkoutStatistics type="HKQuantityTypeIdentifierHeartRate" startDate="` + w0 + `" endDate="` + w1 + `" average="141" minimum="98" maximum="171" unit="count/min"/>` + "\n" +
		`   <WorkoutRoute sourceName="Synthetic Watch" sourceVersion="11.0" creationDate="` + w1 + `" startDate="` + w0 + `" endDate="` + w1 + `">` + "\n" +
		`    <MetadataEntry key="HKMetadataKeySyncVersion" value="2"/>` + "\n" +
		`    <FileReference path="/workout-routes/route_2026-09-14_6.30pm.gpx"/>` + "\n" +
		"   </WorkoutRoute>\n  </Workout>\n")
	b.WriteString(` <ActivitySummary dateComponents="2026-09-14" activeEnergyBurned="512" activeEnergyBurnedGoal="500" activeEnergyBurnedUnit="kcal"/>
</HealthData>
`)
	return b.String()
}

// syntheticRecords is the number of records syntheticExportXML holds (the correlation counts
// once; its members are inside it).
const syntheticRecords = 3 + 4 + 5 + 2 + 1 + 2 + 1 + 1

// writeSyntheticExport writes export.zip (apple_health_export/export.xml plus a route file, as
// the Health app does) into dir and returns its path.
func writeSyntheticExport(t testing.TB, dir string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"apple_health_export/export.xml":                                 syntheticExportXML(),
		"apple_health_export/workout-routes/route_2026-09-14_6.30pm.gpx": `<?xml version="1.0"?><gpx version="1.1"/>`,
	} {
		w, err := zw.Create(name)
		if err == nil {
			_, err = w.Write([]byte(body))
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "export.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
