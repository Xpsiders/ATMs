package reports

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
)

const (
	pdfPageWidth   = 842
	pdfPageHeight  = 595
	pdfRowsPerPage = 40
)

func WriteReportPDF(writer io.Writer, page ReportPage) error {
	lines := []string{
		"STUDENT ATTENDANCE REPORT",
		"Session: " + page.SessionID + "    Scanner coverage: " + string(page.ScannerCoverage.State),
		"Matric No.       Student Name             Department ID                    Level  Roster  Classification       Status    Reason",
		strings.Repeat("-", 136),
	}
	for _, item := range page.Items {
		lines = append(lines, fmt.Sprintf("%-16s %-24s %-32s %-6s %-8s %-21s %-10s %s",
			pdfCell(item.MatricNo, 16), pdfCell(item.DisplayName, 24), pdfCell(item.DepartmentID, 32),
			strconv.Itoa(item.Level), strconv.FormatBool(item.OnRoster), pdfCell(string(item.Classification), 21),
			pdfCell(item.Status, 10), pdfCell(item.Reason, 24)))
	}
	return writePDF(writer, lines)
}

func WriteRosterPDF(writer io.Writer, page RosterPage) error {
	lines := []string{
		"SESSION ATTENDANCE ROSTER",
		"Session: " + page.SessionID,
		"Matric No.       Student Name                       Department                 Level",
		strings.Repeat("-", 88),
	}
	for _, item := range page.Items {
		lines = append(lines, fmt.Sprintf("%-16s %-34s %-28s %s",
			pdfCell(item.MatricNo, 16), pdfCell(item.DisplayName, 34),
			pdfCell(item.DepartmentName, 28), strconv.Itoa(item.Level)))
	}
	return writePDF(writer, lines)
}

func writePDF(writer io.Writer, lines []string) error {
	if len(lines) < 4 {
		return fmt.Errorf("PDF document is missing its header")
	}
	if len(lines) == 4 {
		lines = append(lines, "No records")
	}
	dataRows := lines[4:]
	pageCount := (len(dataRows) + pdfRowsPerPage - 1) / pdfRowsPerPage
	if pageCount == 0 {
		pageCount = 1
	}
	pages := make([][]string, 0, pageCount)
	for index := 0; index < pageCount; index++ {
		start := index * pdfRowsPerPage
		end := start + pdfRowsPerPage
		if end > len(dataRows) {
			end = len(dataRows)
		}
		pageLines := append([]string(nil), lines[:4]...)
		pageLines[1] += fmt.Sprintf("    Page %d of %d", index+1, pageCount)
		pageLines = append(pageLines, dataRows[start:end]...)
		pages = append(pages, pageLines)
	}
	return encodePDF(writer, pages)
}

func encodePDF(writer io.Writer, pages [][]string) error {
	const catalogObject = 1
	const pagesObject = 2
	const fontObject = 3
	firstPageObject := 4
	objectCount := 3 + len(pages)*2
	objects := make([][]byte, objectCount+1)
	pageRefs := make([]string, 0, len(pages))
	for index, lines := range pages {
		pageObject := firstPageObject + index*2
		contentObject := pageObject + 1
		pageRefs = append(pageRefs, fmt.Sprintf("%d 0 R", pageObject))
		content := pdfPageContent(lines)
		objects[pageObject] = []byte(fmt.Sprintf(
			"<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %d %d] /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>",
			pagesObject, pdfPageWidth, pdfPageHeight, fontObject, contentObject))
		objects[contentObject] = []byte(fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream",
			len(content), content))
	}
	objects[catalogObject] = []byte(fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pagesObject))
	objects[pagesObject] = []byte(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>",
		strings.Join(pageRefs, " "), len(pages)))
	objects[fontObject] = []byte("<< /Type /Font /Subtype /Type1 /BaseFont /Courier /Encoding /WinAnsiEncoding >>")

	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, objectCount+1)
	for objectID := 1; objectID <= objectCount; objectID++ {
		offsets[objectID] = output.Len()
		fmt.Fprintf(&output, "%d 0 obj\n", objectID)
		output.Write(objects[objectID])
		output.WriteString("\nendobj\n")
	}
	xrefOffset := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n0000000000 65535 f \n", objectCount+1)
	for objectID := 1; objectID <= objectCount; objectID++ {
		fmt.Fprintf(&output, "%010d 00000 n \n", offsets[objectID])
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		objectCount+1, catalogObject, xrefOffset)
	if _, err := writer.Write(output.Bytes()); err != nil {
		return fmt.Errorf("write PDF document: %w", err)
	}
	return nil
}

func pdfPageContent(lines []string) []byte {
	var content strings.Builder
	content.WriteString("BT\n/F1 8 Tf\n35 558 Td\n")
	for index, line := range lines {
		if index > 0 {
			content.WriteString("0 -12 Td\n")
		}
		content.WriteByte('(')
		content.WriteString(escapePDFString(winAnsi(line)))
		content.WriteString(") Tj\n")
	}
	content.WriteString("ET\n")
	return []byte(content.String())
}

func pdfCell(value string, width int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	runes := []rune(value)
	if len(runes) > width {
		if width > 1 {
			runes = append(runes[:width-1], '.')
		} else {
			runes = runes[:width]
		}
	}
	return string(runes)
}

func winAnsi(value string) string {
	var encoded strings.Builder
	for _, character := range value {
		if character >= 0x20 && character <= 0x7e || character >= 0xa0 && character <= 0xff {
			encoded.WriteByte(byte(character))
			continue
		}
		if replacement, ok := winAnsiSpecial[character]; ok {
			encoded.WriteByte(replacement)
			continue
		}
		encoded.WriteByte('?')
	}
	return encoded.String()
}

func escapePDFString(value string) string {
	var escaped strings.Builder
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == '\\' || character == '(' || character == ')' {
			escaped.WriteByte('\\')
		}
		escaped.WriteByte(character)
	}
	return escaped.String()
}

var winAnsiSpecial = map[rune]byte{
	'\u20ac': 0x80, '\u201a': 0x82, '\u0192': 0x83, '\u201e': 0x84,
	'\u2026': 0x85, '\u2020': 0x86, '\u2021': 0x87, '\u02c6': 0x88,
	'\u2030': 0x89, '\u0160': 0x8a, '\u2039': 0x8b, '\u0152': 0x8c,
	'\u017d': 0x8e, '\u2018': 0x91, '\u2019': 0x92, '\u201c': 0x93,
	'\u201d': 0x94, '\u2022': 0x95, '\u2013': 0x96, '\u2014': 0x97,
	'\u02dc': 0x98, '\u2122': 0x99, '\u0161': 0x9a, '\u203a': 0x9b,
	'\u0153': 0x9c, '\u017e': 0x9e, '\u0178': 0x9f,
}
