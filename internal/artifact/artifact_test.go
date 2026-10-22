package artifact

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func reportArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(file, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
func TestReportsCombineJUnitAndCoverageWithoutExtractingFiles(t *testing.T) {
	data := reportArchive(t, map[string]string{"../../junit.xml": `<testsuite><testcase name="ok" time="1"/><testcase name="failed" classname="suite"><failure message="assertion">expected 2</failure></testcase><testcase name="skip"><skipped/></testcase></testsuite>`, "coverage-summary.json": `{"total":{"lines":{"pct":87.5}}}`, "ignored.txt": "unused"})
	report, err := ReportZIP(data)
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 3 || report.Passed != 1 || report.Failed != 1 || report.Skipped != 1 || report.Coverage == nil || *report.Coverage != 87.5 || len(report.Failures) != 1 {
		t.Fatalf("report=%+v", report)
	}
}
func TestCoverageFormatsAndMalformedReports(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		want          float64
		fails         bool
	}{{"coverage.xml", `<coverage line-rate="0.82"/>`, 82, false}, {"lcov.info", "LF:100\nLH:73\n", 73, false}, {"lcov.info", "LF:10\nLH:20\n", 0, true}, {"junit.xml", "<broken", 0, true}, {"coverage-summary.json", `{"total":{"lines":{"pct":101}}}`, 0, true}} {
		t.Run(tc.name+tc.content, func(t *testing.T) {
			report, err := ReportZIP(reportArchive(t, map[string]string{tc.name: tc.content}))
			if tc.fails {
				if err == nil {
					t.Fatal("malformed report accepted")
				}
				return
			}
			if err != nil || report.Coverage == nil || *report.Coverage != tc.want {
				t.Fatalf("report=%+v error=%v", report, err)
			}
		})
	}
	if _, err := ReportZIP([]byte("invalid")); err == nil {
		t.Fatal("invalid ZIP accepted")
	}
}
func TestReportArchiveLimitsExpandedSizeAndEntryCount(t *testing.T) {
	data := reportArchive(t, map[string]string{"junit.xml": string(bytes.Repeat([]byte("x"), (8<<20)+1))})
	if _, err := ReportZIP(data); err == nil {
		t.Fatal("expanded size accepted")
	}
	files := make(map[string]string)
	for i := range 513 {
		files[fmt.Sprintf("%d.txt", i)] = ""
	}
	if _, err := ReportZIP(reportArchive(t, files)); err == nil {
		t.Fatal("too many entries accepted")
	}
}
func TestArtifactDownloadIsAtomicAndNeverOverwrites(t *testing.T) {
	directory := t.TempDir()
	fetch := func(_ context.Context, writer io.Writer) error {
		_, err := io.WriteString(writer, "archive")
		return err
	}
	path, err := Download(t.Context(), directory, "../../test.zip", 32, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != directory {
		t.Fatal("escaped destination")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "archive" {
		t.Fatalf("data=%s error=%v", data, err)
	}
	if _, err := Download(t.Context(), directory, "../../test.zip", 32, fetch); err == nil {
		t.Fatal("overwrote existing artifact")
	}
	if _, err := Download(t.Context(), directory, "large.zip", 2, fetch); err == nil {
		t.Fatal("size limit ignored")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("partial downloads retained: %v", entries)
	}
}
