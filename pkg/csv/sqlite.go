package csv

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"github.com/araddon/dateparse"
	_ "github.com/mattn/go-sqlite3"
	"github.com/paveldanilin/grafana-csv-plugin/pkg/util"
	"io"
	"strconv"
	"strings"
)

func toSqlite(tableName string, reader *csv.Reader, descriptor *FileDescriptor) (*sql.DB, error) {
	// Read header
	// TODO: we should somehow handle the situation when there is no header line
	header, err := reader.Read()
	if err != nil {
		return nil, err
	}

	// Auto detect column types by the first row with data
	// Keep in mind that in case the absence of data the type will be detected incorrectly
	// In such edge situations, it would be better explicitly define column-type at the data source settings page
	firstRow, err := reader.Read()
	if err != nil {
		return nil, err
	}
	if descriptor.Columns == nil || len(descriptor.Columns) == 0 {
		descriptor.Columns = make([]Column, 0)
		for i, firstRowVal := range firstRow {
			descriptor.Columns = append(descriptor.Columns, Column{
				Type: detectDatatype(firstRowVal),
				Name: header[i],
			})
		}
	}

	// Build map: ColumnName -> CSV column Id
	csvColumns := getColumnNames(descriptor.Columns)
	columnsMap := make(map[string]int)
	for _, columnName := range csvColumns {
		for hci, headerColumn := range header {
			if headerColumn == columnName {
				columnsMap[columnName] = hci
			}
		}
	}

	// Create DB
	db, err := sql.Open("sqlite3", "file::memory:?cache=shared")
	if err != nil {
		return nil, err
	}
	db.SetMaxIdleConns(100)
	// If d <= 0, connections are reused forever.
	db.SetConnMaxLifetime(0)

	// Create table
	sqlCreateTable := createTableFor(tableName, descriptor.Columns)
	_, err = db.Exec(sqlCreateTable)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	// Prepare INSERT statement
	sqlInsert := createInsertFor(tableName, csvColumns)
	stmt, err := db.Prepare(sqlInsert)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	defer stmt.Close()

	// Insert the first row
	rowValues := valuesToRow(firstRow, descriptor.Columns, columnsMap)
	_, err = stmt.Exec(rowValues...)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	// Insert rows...
	for {
		row, err := reader.Read()
		if err != nil && err != io.EOF {
			_ = db.Close()
			return nil, err
		}

		if err == io.EOF {
			break
		}

		// CSV Row -> Insert values
		rowValues := valuesToRow(row, descriptor.Columns, columnsMap)
		_, err = stmt.Exec(rowValues...)
		if err != nil {
			_ = db.Close()
			return nil, err
		}
	}

	return db, nil
}

func createTableFor(tableName string, columns []Column) string {
	columnDefs := make([]string, 0)

	for _, column := range columns {
		columnDefs = append(columnDefs,
			// column data_type DEFAULT 0
			fmt.Sprintf("%s %s %s", column.Name, column.Type, getDefaultForColumn(column.Type)),
		)
	}

	return fmt.Sprintf("CREATE TABLE %s(%s)", tableName, strings.Join(columnDefs, ","))
}

func getDefaultForColumn(columnType ColumnType) string {
	switch columnType {
	case ColumnTypeReal:
		return "DEFAULT 0"
	case ColumnTypeInteger:
		return "DEFAULT 0"
	case ColumnTypeText:
		return "DEFAULT \"\""
	case ColumnTypeDate:
		return "DEFAULT CURRENT_TIMESTAMP"
	case ColumnTypeTimestamp:
		return "DEFAULT CURRENT_TIMESTAMP"
	}
	return "DEFAULT 0"
}

func getColumnNames(columns []Column) []string {
	columnNames := make([]string, 0)
	for _, column := range columns {
		columnNames = append(columnNames, column.Name)
	}
	return columnNames
}

func createInsertFor(tableName string, columnNames []string) string {
	binds := strings.TrimSuffix(strings.Repeat("?,", len(columnNames)), ",")
	return fmt.Sprintf("INSERT INTO %s (%s) values(%s)", tableName, strings.Join(columnNames, ","), binds)
}

func getColumnType(columns []Column, columnName string) *ColumnType {
	for _, column := range columns {
		if column.Name == columnName {
			return &column.Type
		}
	}
	return nil
}

// Caveat: function is not able to guess timestamp format, it will always be Integer
func detectDatatype(value string) ColumnType {
	if util.IsNumber(value) {
		if util.IsInt(value) {
			return ColumnTypeInteger
		}
		return ColumnTypeReal
	}
	_, err := dateparse.ParseAny(value)
	if err == nil {
		return ColumnTypeDate
	}
	return ColumnTypeText
}

func valuesToRow(values []string, columns []Column, columnsMap map[string]int) []interface{} {
	rowValues := make([]interface{}, 0)

	for _, column := range columns {
		if columnIndex, ok := columnsMap[column.Name]; ok {
			columnType := getColumnType(columns, column.Name)
			rowValues = append(rowValues, strToValue(values[columnIndex], columnType))
		}
	}

	return rowValues
}

func strToValue(value string, columnType *ColumnType) interface{} {
	if columnType == nil {
		return value
	}
	switch *columnType {
	case ColumnTypeDate:
		t, err := dateparse.ParseAny(value)
		if err != nil {
			return value
		}
		return t
	case ColumnTypeTimestamp:
		ival, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return value
		}
		return ival
	case ColumnTypeInteger:
		ival, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return value
		}
		return ival
	case ColumnTypeReal:
		fval, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return value
		}
		return fval
	}
	return value
}
