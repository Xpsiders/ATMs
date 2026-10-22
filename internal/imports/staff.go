package imports

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strings"

	"attendance-api/internal/db"
)

var staffColumns = []string{"staff_no", "email", "display_name", "department_id"}

type StaffRow struct {
	Line         int    `json:"line"`
	StaffNo      string `json:"staff_no"`
	Email        string `json:"email"`
	DisplayName  string `json:"display_name"`
	DepartmentID string `json:"department_id"`
}

type StaffPreview struct {
	Rows   []StaffRow `json:"rows"`
	Errors []RowError `json:"errors"`
}

type StaffStore interface {
	ValidateStaff(context.Context, string, string, []StaffRow) ([]RowError, error)
	CommitStaff(context.Context, string, string, []StaffRow) error
}

func PreviewStaff(source io.Reader, maxBytes int64) (StaffPreview, error) {
	if maxBytes <= 0 || maxBytes > MaxStudentCSVBytes {
		maxBytes = MaxStudentCSVBytes
	}
	data, err := io.ReadAll(io.LimitReader(source, maxBytes+1))
	if err != nil {
		return StaffPreview{}, fmt.Errorf("read staff CSV: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return StaffPreview{}, ErrCSVTooLarge
	}
	reader := csv.NewReader(strings.NewReader(string(data)))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	header, err := reader.Read()
	if err != nil {
		return StaffPreview{}, fmt.Errorf("%w: read header: %v", ErrInvalidCSV, err)
	}
	columns := make(map[string]int, len(header))
	for index, name := range header {
		name = strings.ToLower(strings.TrimSpace(name))
		if _, duplicate := columns[name]; duplicate {
			return StaffPreview{}, fmt.Errorf("%w: duplicate column %q", ErrInvalidCSV, name)
		}
		columns[name] = index
	}
	for _, name := range staffColumns {
		if _, ok := columns[name]; !ok {
			return StaffPreview{}, fmt.Errorf("%w: missing required column %q", ErrInvalidCSV, name)
		}
	}
	result := StaffPreview{Rows: []StaffRow{}, Errors: []RowError{}}
	seenStaff := make(map[string]int)
	seenEmails := make(map[string]int)
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
		row := StaffRow{
			Line: line, StaffNo: value("staff_no"), Email: strings.ToLower(value("email")),
			DisplayName: value("display_name"), DepartmentID: value("department_id"),
		}
		rowErrors := 0
		check := func(column, field, message string) {
			if field == "" {
				result.Errors = append(result.Errors, RowError{Line: line, Column: column, Message: message})
				rowErrors++
			}
		}
		check("staff_no", row.StaffNo, "staff number is required")
		check("email", row.Email, "email is required")
		check("display_name", row.DisplayName, "display name is required")
		check("department_id", row.DepartmentID, "department ID is required")
		if row.DepartmentID != "" {
			departmentID, uuidErr := db.ParseUUID(row.DepartmentID)
			if uuidErr != nil {
				result.Errors = append(result.Errors, RowError{Line: line, Column: "department_id", Message: "department ID must be a UUID"})
				rowErrors++
			} else {
				row.DepartmentID = departmentID.String()
			}
		}
		if row.Email != "" {
			address, parseErr := mail.ParseAddress(row.Email)
			if parseErr != nil || !strings.EqualFold(address.Address, row.Email) || len(row.Email) > 254 {
				result.Errors = append(result.Errors, RowError{Line: line, Column: "email", Message: "email address is invalid"})
				rowErrors++
			}
		}
		if row.StaffNo != "" {
			key := strings.ToLower(row.StaffNo)
			if previous, ok := seenStaff[key]; ok {
				result.Errors = append(result.Errors, RowError{Line: line, Column: "staff_no", Message: fmt.Sprintf("staff number duplicates row %d", previous)})
				rowErrors++
			} else {
				seenStaff[key] = line
			}
		}
		if row.Email != "" {
			if previous, ok := seenEmails[row.Email]; ok {
				result.Errors = append(result.Errors, RowError{Line: line, Column: "email", Message: fmt.Sprintf("email duplicates row %d", previous)})
				rowErrors++
			} else {
				seenEmails[row.Email] = line
			}
		}
		if rowErrors == 0 {
			result.Rows = append(result.Rows, row)
		}
	}
	if line == 1 {
		result.Errors = append(result.Errors, RowError{Line: 2, Message: "CSV must contain at least one staff row"})
	}
	return result, nil
}
