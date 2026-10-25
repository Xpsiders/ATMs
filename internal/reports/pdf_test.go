package reports

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestWriteReportPDFProducesDownloadableDocument(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	page := ReportPage{
		SessionID:       "session-1",
		ScannerCoverage: ScannerCoverage{State: CoveragePartial},
		Items: []StudentAttendance{{
			MatricNo: "MAT-001", DisplayName: "Ada (A) Lovelace", DepartmentID: "dept-1",
			Level: 300, OnRoster: true, Classification: Verified, Status: "present", Reason: "Checked in",
		}},
	}
	if err := WriteReportPDF(&output, page); err != nil {
		t.Fatal(err)
	}
	pdf := output.String()
	for _, expected := range []string{
		"%PDF-1.4", "STUDENT ATTENDANCE REPORT", "Session: session-1",
		"MAT-001", "Ada \\(A\\) Lovelace", "%%EOF",
	} {
		if !strings.Contains(pdf, expected) {
			t.Errorf("PDF does not contain %q", expected)
		}
	}
	if !strings.Contains(pdf, "xref\n") || !strings.Contains(pdf, "/Count 1") {
		t.Fatalf("PDF cross-reference or page tree missing")
	}
}

func TestWriteRosterPDFPaginatesLongRosters(t *testing.T) {
	t.Parallel()
	page := RosterPage{SessionID: "session-2", Items: make([]RosterStudent, pdfRowsPerPage+1)}
	for index := range page.Items {
		page.Items[index] = RosterStudent{
			MatricNo: fmt.Sprintf("MAT-%03d", index+1), DisplayName: "Student",
			DepartmentName: "Engineering", Level: 200,
		}
	}
	var output bytes.Buffer
	if err := WriteRosterPDF(&output, page); err != nil {
		t.Fatal(err)
	}
	pdf := output.String()
	if !strings.Contains(pdf, "/Count 2") || !strings.Contains(pdf, "Page 1 of 2") ||
		!strings.Contains(pdf, "Page 2 of 2") || !strings.Contains(pdf, "MAT-041") {
		t.Fatalf("multi-page PDF is incomplete")
	}
}

func TestWritePDFReturnsWriterErrors(t *testing.T) {
	t.Parallel()
	errWrite := errors.New("disk full")
	err := WriteRosterPDF(failingWriter{err: errWrite}, RosterPage{})
	if !errors.Is(err, errWrite) {
		t.Fatalf("WriteRosterPDF error = %v, want %v", err, errWrite)
	}
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}
