package imports

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strconv"
	"strings"

	"attendance-api/internal/db"
)

const MaxStudentCSVBytes int64 = 5 << 20

var studentColumns = []string{"matric_no", "email", "display_name", "department_id", "level"}

var (
	ErrInvalidCSV  = errors.New("student CSV is invalid")
	ErrCSVTooLarge = errors.New("student CSV exceeds the upload size limit")
)

type StudentRow struct {
	Line         int    `json:"line"`
	MatricNo     string `json:"matric_no"`
	Email        string `json:"email"`
	DisplayName  string `json:"display_name"`
	DepartmentID string `json:"department_id"`
	Level        int    `json:"level"`
}

type RowError struct {
	Line    int    `json:"line"`
	Column  string `json:"column,omitempty"`
	Message string `json:"message"`
}

type Preview struct {
	Rows   []StudentRow `json:"rows"`
	Errors []RowError   `json:"errors"`
}

func PreviewStudents(source io.Reader, maxBytes int64) (Preview, error) {
	if maxBytes <= 0 || maxBytes > MaxStudentCSVBytes {
		maxBytes = MaxStudentCSVBytes
	}
	data, err := io.ReadAll(io.LimitReader(source, maxBytes+1))
	if err != nil {
		return Preview{}, fmt.Errorf("read student CSV: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return Preview{}, ErrCSVTooLarge
	}

	reader := csv.NewReader(strings.NewReader(string(data)))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	header, err := reader.Read()
	if err != nil {
		return Preview{}, fmt.Errorf("%w: read header: %v", ErrInvalidCSV, err)
	}
	columns := make(map[string]int, len(header))
	for index, name := range header {
		name = strings.TrimSpace(strings.ToLower(name))
		if _, duplicate := columns[name]; duplicate {
			return Preview{}, fmt.Errorf("%w: duplicate column %q", ErrInvalidCSV, name)
		}
		columns[name] = index
	}
	for _, name := range studentColumns {
		if _, ok := columns[name]; !ok {
			return Preview{}, fmt.Errorf("%w: missing required column %q", ErrInvalidCSV, name)
		}
	}
	if len(columns) == 0 {
		return Preview{}, errors.New("CSV header is empty")
	}

	result := Preview{Rows: make([]StudentRow, 0), Errors: make([]RowError, 0)}
	seenMatric := make(map[string]int)
	line := 1
	for {
		record, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		line++
		if readErr != nil {
			result.Errors = append(result.Errors, RowError{Line: line, Message: "malformed CSV row"})
			continue
		}
		if len(record) != len(header) {
			result.Errors = append(result.Errors, RowError{Line: line, Message: "row column count does not match header"})
			continue
		}
		value := func(column string) string { return strings.TrimSpace(record[columns[column]]) }
		row := StudentRow{
			Line:         line,
			MatricNo:     value("matric_no"),
			Email:        value("email"),
			DisplayName:  value("display_name"),
			DepartmentID: value("department_id"),
		}
		level, levelErr := strconv.Atoi(value("level"))
		row.Level = level
		rowErrs := 0
		check := func(column, field, message string) {
			if field == "" {
				result.Errors = append(result.Errors, RowError{Line: line, Column: column, Message: message})
				rowErrs++
			}
		}
		check("matric_no", row.MatricNo, "matric number is required")
		check("email", row.Email, "email is required")
		check("display_name", row.DisplayName, "display name is required")
		check("department_id", row.DepartmentID, "department ID is required")
		if row.DepartmentID != "" {
			departmentID, uuidErr := db.ParseUUID(row.DepartmentID)
			if uuidErr != nil {
				result.Errors = append(result.Errors, RowError{Line: line, Column: "department_id", Message: "department ID must be a UUID"})
				rowErrs++
			} else {
				row.DepartmentID = departmentID.String()
			}
		}
		if levelErr != nil || level <= 0 {
			result.Errors = append(result.Errors, RowError{Line: line, Column: "level", Message: "level must be a positive integer"})
			rowErrs++
		}
		if row.MatricNo != "" {
			matricKey := strings.ToLower(row.MatricNo)
			if previous, duplicate := seenMatric[matricKey]; duplicate {
				result.Errors = append(result.Errors, RowError{
					Line: line, Column: "matric_no",
					Message: fmt.Sprintf("matric number duplicates row %d", previous),
				})
				rowErrs++
			} else {
				seenMatric[matricKey] = line
			}
		}
		if row.Email != "" {
			address, parseErr := mail.ParseAddress(row.Email)
			if parseErr != nil || !strings.EqualFold(address.Address, row.Email) {
				result.Errors = append(result.Errors, RowError{Line: line, Column: "email", Message: "email address is invalid"})
				rowErrs++
			}
		}
		if rowErrs == 0 {
			result.Rows = append(result.Rows, row)
		}
	}
	if line == 1 {
		result.Errors = append(result.Errors, RowError{Line: 2, Message: "CSV must contain at least one student row"})
	}
	return result, nil
}
