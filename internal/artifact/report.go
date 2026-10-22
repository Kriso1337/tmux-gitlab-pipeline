package artifact

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"strconv"
	"strings"

	"tmux-gitlab-pipeline/internal/ci"
)

func ReportZIP(data []byte) (ci.Report, error) {
	if len(data) > 16<<20 {
		return ci.Report{}, fmt.Errorf("compressed report exceeds 16 MiB")
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return ci.Report{}, err
	}
	if len(archive.File) > 512 {
		return ci.Report{}, fmt.Errorf("report archive exceeds 512 entries")
	}
	report := ci.Report{}
	remaining := int64(8 << 20)
	count := 0
	for _, file := range archive.File {
		name := strings.ToLower(path.Base(file.Name))
		kind := ""
		switch {
		case name == "coverage-summary.json":
			kind = "json"
		case strings.HasSuffix(name, ".info") || name == "lcov":
			kind = "lcov"
		case strings.HasSuffix(name, ".xml") && (strings.Contains(name, "test") || strings.Contains(name, "junit") || strings.Contains(name, "coverage") || strings.Contains(name, "cobertura")):
			kind = "xml"
		}
		if kind == "" || file.FileInfo().IsDir() {
			continue
		}
		count++
		if count > 64 || file.UncompressedSize64 > uint64(remaining) {
			return report, fmt.Errorf("expanded reports exceed 8 MiB or 64 files")
		}
		reader, err := file.Open()
		if err != nil {
			return report, err
		}
		content, readErr := io.ReadAll(io.LimitReader(reader, remaining+1))
		closeErr := reader.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return report, err
		}
		if int64(len(content)) > remaining {
			return report, fmt.Errorf("expanded reports exceed 8 MiB")
		}
		remaining -= int64(len(content))
		var parsed ci.Report
		switch kind {
		case "xml":
			parsed, err = reportXML(content)
		case "lcov":
			parsed, err = reportLCOV(content)
		case "json":
			parsed, err = reportJSON(content)
		}
		if err != nil {
			return report, fmt.Errorf("parse report %s: %w", name, err)
		}
		report.Total += parsed.Total
		report.Passed += parsed.Passed
		report.Failed += parsed.Failed
		report.Skipped += parsed.Skipped
		report.Duration += parsed.Duration
		if parsed.Coverage != nil {
			report.Coverage = parsed.Coverage
		}
		report.Failures = append(report.Failures, parsed.Failures...)
		if len(report.Failures) > 20 {
			report.Failures = report.Failures[:20]
		}
	}
	return report, nil
}
func coverage(value float64) (*float64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
		return nil, fmt.Errorf("invalid coverage percentage")
	}
	return &value, nil
}
func reportXML(data []byte) (ci.Report, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	report := ci.Report{}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return report, err
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local == "coverage" {
			for _, attribute := range start.Attr {
				if attribute.Name.Local == "line-rate" {
					value, err := strconv.ParseFloat(attribute.Value, 64)
					if err != nil {
						return report, err
					}
					report.Coverage, err = coverage(value * 100)
					if err != nil {
						return report, err
					}
				}
			}
		}
		if start.Name.Local != "testcase" {
			continue
		}
		var test struct {
			Name    string  `xml:"name,attr"`
			Class   string  `xml:"classname,attr"`
			Time    float64 `xml:"time,attr"`
			Failure *struct {
				Message string `xml:"message,attr"`
				Text    string `xml:",chardata"`
			} `xml:"failure"`
			Error *struct {
				Message string `xml:"message,attr"`
				Text    string `xml:",chardata"`
			} `xml:"error"`
			Skipped *struct{} `xml:"skipped"`
		}
		if err := decoder.DecodeElement(&test, &start); err != nil {
			return report, err
		}
		report.Total++
		if test.Time < 0 || math.IsNaN(test.Time) || math.IsInf(test.Time, 0) {
			return report, fmt.Errorf("invalid test duration")
		}
		report.Duration += test.Time
		switch {
		case test.Skipped != nil:
			report.Skipped++
		case test.Failure != nil || test.Error != nil:
			report.Failed++
			message := ""
			if test.Failure != nil {
				message = test.Failure.Message + "\n" + test.Failure.Text
			}
			if test.Error != nil {
				message = test.Error.Message + "\n" + test.Error.Text
			}
			if len(message) > 2048 {
				message = message[:2048]
			}
			if len(report.Failures) < 20 {
				report.Failures = append(report.Failures, ci.TestFailure{Name: test.Name, Suite: test.Class, Message: message})
			}
		default:
			report.Passed++
		}
	}
	return report, nil
}
func reportJSON(data []byte) (ci.Report, error) {
	var summary struct {
		Total struct {
			Lines struct {
				Pct *float64 `json:"pct"`
			} `json:"lines"`
		} `json:"total"`
	}
	if err := json.Unmarshal(data, &summary); err != nil {
		return ci.Report{}, err
	}
	report := ci.Report{}
	if summary.Total.Lines.Pct != nil {
		value, err := coverage(*summary.Total.Lines.Pct)
		if err != nil {
			return report, err
		}
		report.Coverage = value
	}
	return report, nil
}
func reportLCOV(data []byte) (ci.Report, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var total, hit int64
	for scanner.Scan() {
		line := scanner.Text()
		kind, value, ok := strings.Cut(line, ":")
		if !ok || (kind != "LF" && kind != "LH") {
			continue
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 0 || n > 1_000_000_000 {
			return ci.Report{}, fmt.Errorf("invalid LCOV line count")
		}
		if kind == "LF" {
			total += n
		} else {
			hit += n
		}
	}
	if err := scanner.Err(); err != nil {
		return ci.Report{}, err
	}
	report := ci.Report{}
	if total > 0 {
		value, err := coverage(float64(hit) * 100 / float64(total))
		if err != nil {
			return report, err
		}
		report.Coverage = value
	}
	return report, nil
}
