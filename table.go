package pagser

import (
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// TablePathSeparator joins multi-level thead texts into one column path,
// eg: a two level header `销量` over `线上` builds the column path `销量/线上`.
const TablePathSeparator = "/"

// TableCell is one logical cell of the expanded table grid.
//
// Rowspan/colspan are expanded into the grid: every covered position holds a
// copy of the shared value, and Row/Col always point back to the source
// (origin) cell coordinates in the whole-table grid (thead rows first, then
// every tbody in document order). Positions no source cell covers are kept
// as Empty cells.
type TableCell struct {
	Text    string // trimmed cell text, nested tables excluded
	Row     int    // grid row of the source cell
	Col     int    // grid column of the source cell
	RowSpan int    // rowspan of the source cell, >= 1
	ColSpan int    // colspan of the source cell, >= 1
	Covered bool   // true when this position is filled by a span from the source cell at Row/Col
	Empty   bool   // true when no source cell covers this position (a hole)

	sel *goquery.Selection // source cell selection, nil when Empty
}

// Table is the extracted table: thead column paths plus the expanded grids.
type Table struct {
	HeaderPaths []string      // column paths composed from the thead levels
	HeaderRows  [][]TableCell // expanded thead grid
	BodyRows    [][]TableCell // expanded body rows, every tbody concatenated in document order

	colIndex map[string]int // column path => grid column
}

// TableNotFoundError no table matched the selector.
type TableNotFoundError struct {
	Selector string
}

func (e *TableNotFoundError) Error() string {
	return fmt.Sprintf("pagser: table not found by selector %q", e.Selector)
}

// TableNoHeaderError the table has no thead, column paths cannot be built.
type TableNoHeaderError struct {
	Selector string
}

func (e *TableNoHeaderError) Error() string {
	return fmt.Sprintf("pagser: table %q has no thead, cannot build column paths", e.Selector)
}

// TableSpanError rowspan/colspan is not a positive integer.
type TableSpanError struct {
	Attr  string // "rowspan" or "colspan"
	Value string // the raw attribute value
	Row   int    // grid row of the cell
	Col   int    // grid column of the cell
}

func (e *TableSpanError) Error() string {
	return fmt.Sprintf("pagser: table cell at row %d col %d has invalid %s=%q, must be a positive integer",
		e.Row, e.Col, e.Attr, e.Value)
}

// TableRowGroupSpanError a rowspan crosses its row group (thead/tbody) boundary.
type TableRowGroupSpanError struct {
	Row       int // grid row of the cell
	Col       int // grid column of the cell
	RowSpan   int // the requested rowspan
	GroupRows int // rows available in the row group
}

func (e *TableRowGroupSpanError) Error() string {
	return fmt.Sprintf("pagser: table cell at row %d col %d rowspan=%d exceeds its row group of %d rows, spans must not cross row groups",
		e.Row, e.Col, e.RowSpan, e.GroupRows)
}

// TableDuplicateColumnError two header columns compose the same column path.
type TableDuplicateColumnError struct {
	Path    string // the duplicated column path
	Columns []int  // the grid columns that clash
}

func (e *TableDuplicateColumnError) Error() string {
	return fmt.Sprintf("pagser: duplicate table column path %q at columns %v", e.Path, e.Columns)
}

// TableColumnNotFoundError a struct field binds a column path the table does not have.
type TableColumnNotFoundError struct {
	Path string
}

func (e *TableColumnNotFoundError) Error() string {
	return fmt.Sprintf("pagser: table column path %q not found in header paths", e.Path)
}

// ExtractTable expands a table selection into a Table grid.
//
// Only the table's own rows are read: rows and cell text of nested tables
// never leak into the outer table. Rowspans that cross a row group boundary,
// non positive-integer spans and duplicate column paths return the dedicated
// error types above.
func (p *Pagser) ExtractTable(tableSel *goquery.Selection) (*Table, error) {
	if tableSel == nil || tableSel.Size() == 0 {
		return nil, &TableNotFoundError{}
	}

	//collect row groups in document order, direct children only
	type rowGroup struct {
		kind string // thead, tbody, tfoot or tr (rows directly under table)
		trs  *goquery.Selection
	}
	groups := make([]rowGroup, 0)
	tableSel.Children().Each(func(_ int, s *goquery.Selection) {
		switch name := goquery.NodeName(s); name {
		case "thead", "tbody", "tfoot":
			groups = append(groups, rowGroup{kind: name, trs: s.ChildrenFiltered("tr")})
		case "tr":
			if n := len(groups); n > 0 && groups[n-1].kind == "tr" {
				groups[n-1].trs = groups[n-1].trs.AddSelection(s)
			} else {
				groups = append(groups, rowGroup{kind: "tr", trs: s})
			}
		}
	})

	var headerGrid [][]TableCell
	bodyRows := make([][]TableCell, 0)
	headerFound := false
	rowOffset := 0
	for _, g := range groups {
		switch g.kind {
		case "thead":
			if headerFound {
				continue //only the first thead builds the column paths
			}
			grid, err := expandTableRows(g.trs, 0)
			if err != nil {
				return nil, err
			}
			headerGrid = grid
			headerFound = true
			rowOffset += len(grid)
		case "tbody", "tr":
			grid, err := expandTableRows(g.trs, rowOffset)
			if err != nil {
				return nil, err
			}
			bodyRows = append(bodyRows, grid...)
			rowOffset += len(grid)
		}
	}
	if !headerFound {
		return nil, &TableNoHeaderError{}
	}

	//normalize every row to the table width, holes stay as Empty cells
	width := 0
	for _, row := range headerGrid {
		if len(row) > width {
			width = len(row)
		}
	}
	for _, row := range bodyRows {
		if len(row) > width {
			width = len(row)
		}
	}
	padTableRows(headerGrid, width, 0)
	padTableRows(bodyRows, width, len(headerGrid))

	paths := buildColumnPaths(headerGrid, width)
	colIndex := make(map[string]int, len(paths))
	for col, path := range paths {
		if first, ok := colIndex[path]; ok {
			return nil, &TableDuplicateColumnError{Path: path, Columns: []int{first, col}}
		}
		colIndex[path] = col
	}

	return &Table{
		HeaderPaths: paths,
		HeaderRows:  headerGrid,
		BodyRows:    bodyRows,
		colIndex:    colIndex,
	}, nil
}

// ParseTable parse html table to a slice of structs.
//
// Each struct field binds a column path composed from the thead levels:
//
//	type RowData struct {
//		Name  string  `pagser:"产品"`
//		Price float64 `pagser:"销量/金额->money()"`
//	}
//	var rows []RowData
//	err := pagser.New().ParseTable(&rows, html, "#price-table")
//
// The tag keeps the `[column path]->[function]` grammar, type conversion,
// custom functions and CastError semantics of the Parse series. tableSelector
// defaults to "table" (the first table in the document).
func (p *Pagser) ParseTable(v interface{}, document string, tableSelector ...string) (err error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(document))
	if err != nil {
		return err
	}
	return p.ParseTableDocument(v, doc, tableSelector...)
}

// ParseTableReader parse html table from reader to a slice of structs
func (p *Pagser) ParseTableReader(v interface{}, reader io.Reader, tableSelector ...string) (err error) {
	doc, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		return err
	}
	return p.ParseTableDocument(v, doc, tableSelector...)
}

// ParseTableDocument parse document table to a slice of structs
func (p *Pagser) ParseTableDocument(v interface{}, document *goquery.Document, tableSelector ...string) (err error) {
	selector := "table"
	if len(tableSelector) > 0 && tableSelector[0] != "" {
		selector = tableSelector[0]
	}
	tableSel := document.Find(selector).First()
	if tableSel.Size() <= 0 {
		return &TableNotFoundError{Selector: selector}
	}
	return p.ParseTableSelection(v, tableSel)
}

// ParseTableSelection parse a table selection to a slice of structs,
// v must be a pointer to a slice of structs (or of struct pointers).
func (p *Pagser) ParseTableSelection(v interface{}, tableSel *goquery.Selection) (err error) {
	objRefType := reflect.TypeOf(v)
	objRefValue := reflect.ValueOf(v)
	if objRefValue.Kind() != reflect.Ptr {
		return fmt.Errorf("%v is non-pointer", objRefType)
	}
	if objRefValue.IsNil() {
		return fmt.Errorf("%v is nil", objRefType)
	}
	if objRefType.Elem().Kind() != reflect.Slice {
		return fmt.Errorf("%v must be a pointer to a slice of structs", objRefType)
	}

	table, err := p.ExtractTable(tableSel)
	if err != nil {
		return err
	}

	sliceType := objRefType.Elem()
	itemType := sliceType.Elem()
	itemKind := itemType.Kind()
	isPtrItem := false
	switch {
	case itemKind == reflect.Struct:
	case itemKind == reflect.Ptr && itemType.Elem().Kind() == reflect.Struct:
		isPtrItem = true
	default:
		return fmt.Errorf("%v must be a pointer to a slice of structs", objRefType)
	}

	slice := reflect.MakeSlice(sliceType, len(table.BodyRows), len(table.BodyRows))
	for i, row := range table.BodyRows {
		var itemValue reflect.Value
		if isPtrItem {
			itemValue = reflect.New(itemType.Elem())
		} else {
			itemValue = reflect.New(itemType).Elem()
		}
		err = p.bindTableRow(table, row, itemValue)
		if err != nil {
			return err
		}
		slice.Index(i).Set(itemValue)
	}
	objRefValue.Elem().Set(slice)
	return nil
}

// bindTableRow binds one expanded body row to one struct item by column path.
func (p *Pagser) bindTableRow(table *Table, row []TableCell, itemValue reflect.Value) (err error) {
	itemRefValue := itemValue
	if itemRefValue.Kind() != reflect.Ptr {
		itemRefValue = itemValue.Addr()
	}
	itemType := itemRefValue.Elem().Type()
	itemRefValueElem := itemRefValue.Elem()

	for i := 0; i < itemRefValueElem.NumField(); i++ {
		fieldType := itemType.Field(i)
		fieldValue := itemRefValueElem.Field(i)
		kind := fieldType.Type.Kind()

		tagValue, tagOk := fieldType.Tag.Lookup(p.Config.TagName)
		if !tagOk {
			continue
		}
		if tagValue == ignoreSymbol {
			continue
		}

		cacheTag, ok := p.mapTags.Load(tagValue)
		var tag *tagTokenizer
		if !ok || cacheTag == nil {
			tag, err = p.newTag(tagValue)
			if err != nil {
				return err
			}
			p.mapTags.Store(tagValue, tag)
		} else {
			tag = cacheTag.(*tagTokenizer)
		}

		//the selector part of the tag is the column path in table mode
		path := tag.Selector
		col, ok := table.colIndex[path]
		if !ok {
			return &TableColumnNotFoundError{Path: path}
		}
		cell := row[col]

		switch kind {
		case reflect.Struct, reflect.Ptr, reflect.Map, reflect.Chan, reflect.Func:
			return fmt.Errorf("tag=`%v` table field %v not support kind %v, only scalar and slice kinds",
				tagValue, fieldType.Name, kind)
		}

		var value interface{}
		if tag.FuncName != "" {
			node := cell.sel
			if node == nil {
				node = &goquery.Selection{}
			}
			callOutValue, callErr := p.findAndExecFunc(itemRefValue, nil, tag, node)
			if callErr != nil {
				return fmt.Errorf("tag=`%v` table row %d column %q parse func error: %v",
					tagValue, cell.Row, path, callErr)
			}
			if subNode, ok := callOutValue.(*goquery.Selection); ok {
				value = strings.TrimSpace(subNode.Text())
			} else {
				value = callOutValue
			}
		} else {
			value = cell.Text
		}

		if svErr := p.setRefectValue(kind, fieldValue, value); svErr != nil {
			return fmt.Errorf("tag=`%v` table row %d column %q set value error: %v",
				tagValue, cell.Row, path, svErr)
		}
	}
	return nil
}

// expandTableRows expands one row group's rows into a rectangular grid.
// rowOffset is the grid row of the group's first row in the whole table.
func expandTableRows(trs *goquery.Selection, rowOffset int) ([][]TableCell, error) {
	rowCount := trs.Size()
	slots := make([][]*TableCell, rowCount)
	var gerr error
	trs.EachWithBreak(func(r int, tr *goquery.Selection) bool {
		col := 0
		//direct cell children only, nested table cells never leak in
		tr.ChildrenFiltered("th,td").EachWithBreak(func(_ int, cs *goquery.Selection) bool {
			//first free column, slots above may be covered by rowspans
			for col < len(slots[r]) && slots[r][col] != nil {
				col++
			}
			rowspan, err := parseTableSpan(cs, "rowspan", rowOffset+r, col)
			if err != nil {
				gerr = err
				return false
			}
			colspan, err := parseTableSpan(cs, "colspan", rowOffset+r, col)
			if err != nil {
				gerr = err
				return false
			}
			if r+rowspan > rowCount {
				gerr = &TableRowGroupSpanError{
					Row:       rowOffset + r,
					Col:       col,
					RowSpan:   rowspan,
					GroupRows: rowCount,
				}
				return false
			}
			cell := &TableCell{
				Text:    tableCellText(cs),
				Row:     rowOffset + r,
				Col:     col,
				RowSpan: rowspan,
				ColSpan: colspan,
				sel:     cs,
			}
			for dr := 0; dr < rowspan; dr++ {
				for len(slots[r+dr]) < col+colspan {
					slots[r+dr] = append(slots[r+dr], nil)
				}
				for dc := 0; dc < colspan; dc++ {
					slots[r+dr][col+dc] = cell
				}
			}
			col += colspan
			return true
		})
		return gerr == nil
	})
	if gerr != nil {
		return nil, gerr
	}

	width := 0
	for _, row := range slots {
		if len(row) > width {
			width = len(row)
		}
	}
	grid := make([][]TableCell, rowCount)
	for r := 0; r < rowCount; r++ {
		grid[r] = make([]TableCell, width)
		for c := 0; c < width; c++ {
			if c < len(slots[r]) && slots[r][c] != nil {
				cell := *slots[r][c]
				//shared positions point back to the source cell coordinates
				cell.Covered = cell.Row != rowOffset+r || cell.Col != c
				grid[r][c] = cell
			} else {
				//keep the hole as an Empty cell on its own position
				grid[r][c] = TableCell{Empty: true, Row: rowOffset + r, Col: c}
			}
		}
	}
	return grid, nil
}

// parseTableSpan reads rowspan/colspan, default 1, must be a positive integer.
func parseTableSpan(cs *goquery.Selection, attr string, row, col int) (int, error) {
	raw, ok := cs.Attr(attr)
	if !ok {
		return 1, nil
	}
	raw = strings.TrimSpace(raw)
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, &TableSpanError{Attr: attr, Value: raw, Row: row, Col: col}
	}
	return n, nil
}

// tableCellText the trimmed cell text, nested tables excluded.
func tableCellText(cs *goquery.Selection) string {
	clone := cs.Clone()
	clone.Find("table").Remove()
	return strings.TrimSpace(clone.Text())
}

// padTableRows pads rows to width with Empty cells on their own positions.
func padTableRows(rows [][]TableCell, width int, rowOffset int) {
	for r := range rows {
		for len(rows[r]) < width {
			rows[r] = append(rows[r], TableCell{Empty: true, Row: rowOffset + r, Col: len(rows[r])})
		}
	}
}

// buildColumnPaths composes one column path per grid column from the thead
// levels, eg `销量/线上`. Levels filled by the same rowspan header cell are
// joined only once.
func buildColumnPaths(headerGrid [][]TableCell, width int) []string {
	paths := make([]string, width)
	for c := 0; c < width; c++ {
		parts := make([]string, 0, len(headerGrid))
		prevRow, prevCol := -1, -1
		for r := 0; r < len(headerGrid); r++ {
			if c >= len(headerGrid[r]) {
				continue
			}
			cell := headerGrid[r][c]
			if cell.Empty {
				continue
			}
			//same source cell as the previous level (rowspan), skip the repeat
			if cell.Row == prevRow && cell.Col == prevCol {
				continue
			}
			prevRow, prevCol = cell.Row, cell.Col
			if cell.Text == "" {
				continue
			}
			parts = append(parts, cell.Text)
		}
		paths[c] = strings.Join(parts, TablePathSeparator)
	}
	return paths
}
