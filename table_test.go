package pagser

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

// cross merging: rowspan and colspan intersect in thead and tbody
const rawTableCrossHtml = `
<table id="cross">
	<thead>
		<tr><th rowspan="2">产品</th><th colspan="2">销量</th><th rowspan="2">金额</th></tr>
		<tr><th>线上</th><th>线下</th></tr>
	</thead>
	<tbody>
		<tr><td rowspan="2">手机</td><td>10</td><td>20</td><td>1000</td></tr>
		<tr><td colspan="2">30</td><td>2000</td></tr>
	</tbody>
</table>
`

func TestExtractTableCrossMerge(t *testing.T) {
	p := New()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(rawTableCrossHtml))
	if err != nil {
		t.Fatal(err)
	}
	table, err := p.ExtractTable(doc.Find("#cross"))
	if err != nil {
		t.Fatal(err)
	}

	//column paths composed by thead levels, rowspan headers joined once
	wantPaths := []string{"产品", "销量/线上", "销量/线下", "金额"}
	if fmt.Sprintf("%v", table.HeaderPaths) != fmt.Sprintf("%v", wantPaths) {
		t.Fatalf("header paths = %v, want %v", table.HeaderPaths, wantPaths)
	}

	if len(table.BodyRows) != 2 {
		t.Fatalf("body rows = %d, want 2", len(table.BodyRows))
	}

	//row 0: 手机(rowspan=2) | 10 | 20 | 1000
	row0 := table.BodyRows[0]
	wantRow0 := []TableCell{
		{Text: "手机", Row: 2, Col: 0, RowSpan: 2, ColSpan: 1},
		{Text: "10", Row: 2, Col: 1, RowSpan: 1, ColSpan: 1},
		{Text: "20", Row: 2, Col: 2, RowSpan: 1, ColSpan: 1},
		{Text: "1000", Row: 2, Col: 3, RowSpan: 1, ColSpan: 1},
	}
	assertTableRow(t, row0, wantRow0)

	//row 1: 手机 shared from (2,0) | 30(colspan=2) | 30 shared | 2000
	row1 := table.BodyRows[1]
	wantRow1 := []TableCell{
		{Text: "手机", Row: 2, Col: 0, RowSpan: 2, ColSpan: 1, Covered: true},
		{Text: "30", Row: 3, Col: 1, RowSpan: 1, ColSpan: 2},
		{Text: "30", Row: 3, Col: 1, RowSpan: 1, ColSpan: 2, Covered: true},
		{Text: "2000", Row: 3, Col: 3, RowSpan: 1, ColSpan: 1},
	}
	assertTableRow(t, row1, wantRow1)
}

func TestParseTableCrossMerge(t *testing.T) {
	type RowData struct {
		Name    string  `pagser:"产品"`
		Online  int     `pagser:"销量/线上"`
		Offline int     `pagser:"销量/线下"`
		Amount  float64 `pagser:"金额"`
	}
	var rows []RowData
	err := New().ParseTable(&rows, rawTableCrossHtml, "#cross")
	if err != nil {
		t.Fatal(err)
	}
	want := []RowData{
		{Name: "手机", Online: 10, Offline: 20, Amount: 1000},
		{Name: "手机", Online: 30, Offline: 30, Amount: 2000},
	}
	if fmt.Sprintf("%+v", rows) != fmt.Sprintf("%+v", want) {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}
}

// multiple tbody sections
const rawTableMultiTbodyHtml = `
<table id="multi">
	<thead><tr><th>城市</th><th>销量</th></tr></thead>
	<tbody>
		<tr><td>北京</td><td>10</td></tr>
	</tbody>
	<tbody>
		<tr><td>上海</td><td>20</td></tr>
		<tr><td>广州</td><td>30</td></tr>
	</tbody>
</table>
`

func TestParseTableMultiTbody(t *testing.T) {
	type RowData struct {
		City  string `pagser:"城市"`
		Sales int    `pagser:"销量"`
	}
	var rows []RowData
	err := New().ParseTable(&rows, rawTableMultiTbodyHtml, "#multi")
	if err != nil {
		t.Fatal(err)
	}
	want := []RowData{
		{City: "北京", Sales: 10},
		{City: "上海", Sales: 20},
		{City: "广州", Sales: 30},
	}
	if fmt.Sprintf("%+v", rows) != fmt.Sprintf("%+v", want) {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}

	//source coordinates continue across tbody sections
	p := New()
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(rawTableMultiTbodyHtml))
	table, err := p.ExtractTable(doc.Find("#multi"))
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range table.BodyRows {
		if row[0].Row != i+1 || row[0].Col != 0 {
			t.Fatalf("body row %d source = (%d,%d), want (%d,0)", i, row[0].Row, row[0].Col, i+1)
		}
	}
}

// empty cells: real empty td and holes left by ragged rows
const rawTableEmptyHtml = `
<table id="empty">
	<thead><tr><th>A</th><th>B</th><th>C</th></tr></thead>
	<tbody>
		<tr><td>1</td><td></td><td>3</td></tr>
		<tr><td>4</td></tr>
	</tbody>
</table>
`

func TestParseTableEmptyCells(t *testing.T) {
	type RowData struct {
		A string `pagser:"A"`
		B string `pagser:"B"`
		C string `pagser:"C"`
	}
	var rows []RowData
	err := New().ParseTable(&rows, rawTableEmptyHtml, "#empty")
	if err != nil {
		t.Fatal(err)
	}
	want := []RowData{
		{A: "1", B: "", C: "3"},
		{A: "4", B: "", C: ""},
	}
	if fmt.Sprintf("%+v", rows) != fmt.Sprintf("%+v", want) {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}

	p := New()
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(rawTableEmptyHtml))
	table, err := p.ExtractTable(doc.Find("#empty"))
	if err != nil {
		t.Fatal(err)
	}
	//row 0 col 1 is a real empty cell: empty text but not a hole
	c := table.BodyRows[0][1]
	if c.Empty || c.Text != "" || c.Row != 1 || c.Col != 1 {
		t.Fatalf("real empty cell = %+v, want Empty=false Text=\"\" at (1,1)", c)
	}
	//row 1 col 1 and col 2 are holes and stay as Empty cells
	for col := 1; col <= 2; col++ {
		c := table.BodyRows[1][col]
		if !c.Empty || c.Text != "" || c.Row != 2 || c.Col != col {
			t.Fatalf("hole cell = %+v, want Empty=true at (2,%d)", c, col)
		}
	}
}

// duplicate header path must return a recognizable error
const rawTableDupHeaderHtml = `
<table id="dup">
	<thead><tr><th>名称</th><th>数值</th><th>名称</th></tr></thead>
	<tbody><tr><td>a</td><td>1</td><td>b</td></tr></tbody>
</table>
`

func TestParseTableDuplicateHeader(t *testing.T) {
	type RowData struct {
		Name string `pagser:"名称"`
	}
	var rows []RowData
	err := New().ParseTable(&rows, rawTableDupHeaderHtml, "#dup")
	var dupErr *TableDuplicateColumnError
	if !errors.As(err, &dupErr) {
		t.Fatalf("err = %v, want TableDuplicateColumnError", err)
	}
	if dupErr.Path != "名称" || fmt.Sprintf("%v", dupErr.Columns) != "[0 2]" {
		t.Fatalf("dupErr = %+v, want path 名称 columns [0 2]", dupErr)
	}
}

// custom number conversion through a registered function
const rawTableMoneyHtml = `
<table id="money">
	<thead><tr><th>产品</th><th>单价</th></tr></thead>
	<tbody>
		<tr><td>手机</td><td>¥1,999.50</td></tr>
		<tr><td>平板</td><td>¥2,999</td></tr>
	</tbody>
</table>
`

func TestParseTableCustomFunc(t *testing.T) {
	p := New()
	p.RegisterFunc("money", func(node *goquery.Selection, args ...string) (out interface{}, err error) {
		text := strings.TrimSpace(node.Text())
		text = strings.ReplaceAll(text, "¥", "")
		text = strings.ReplaceAll(text, ",", "")
		return text, nil
	})

	type RowData struct {
		Name  string  `pagser:"产品"`
		Price float64 `pagser:"单价->money()"`
	}
	var rows []RowData
	err := p.ParseTable(&rows, rawTableMoneyHtml, "#money")
	if err != nil {
		t.Fatal(err)
	}
	want := []RowData{
		{Name: "手机", Price: 1999.5},
		{Name: "平板", Price: 2999},
	}
	if fmt.Sprintf("%+v", rows) != fmt.Sprintf("%+v", want) {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}
}

// struct method as custom function, same lookup rules as the Parse series
type tableMethodRow struct {
	Name  string `pagser:"产品"`
	Price string `pagser:"单价->Prefix()"`
}

func (r tableMethodRow) Prefix(node *goquery.Selection, args ...string) (out interface{}, err error) {
	return "price:" + strings.TrimSpace(node.Text()), nil
}

func TestParseTableStructMethod(t *testing.T) {
	var rows []tableMethodRow
	err := New().ParseTable(&rows, rawTableMoneyHtml, "#money")
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Price != "price:¥1,999.50" || rows[1].Price != "price:¥2,999" {
		t.Fatalf("rows = %+v", rows)
	}
}

// invalid spans must return a recognizable error with row and col
func TestParseTableInvalidSpan(t *testing.T) {
	cases := []struct {
		name    string
		html    string
		attr    string
		value   string
		wantRow int
		wantCol int
	}{
		{"rowspan not number", `<table><thead><tr><th>A</th></tr></thead><tbody><tr><td rowspan="x">1</td></tr></tbody></table>`, "rowspan", "x", 1, 0},
		{"rowspan zero", `<table><thead><tr><th>A</th></tr></thead><tbody><tr><td rowspan="0">1</td></tr></tbody></table>`, "rowspan", "0", 1, 0},
		{"colspan negative", `<table><thead><tr><th>A</th></tr></thead><tbody><tr><td colspan="-2">1</td></tr></tbody></table>`, "colspan", "-2", 1, 0},
		{"colspan in second cell", `<table><thead><tr><th>A</th><th>B</th></tr></thead><tbody><tr><td>1</td><td colspan="2.5">2</td></tr></tbody></table>`, "colspan", "2.5", 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			type RowData struct {
				A string `pagser:"A"`
			}
			var rows []RowData
			err := New().ParseTable(&rows, c.html)
			var spanErr *TableSpanError
			if !errors.As(err, &spanErr) {
				t.Fatalf("err = %v, want TableSpanError", err)
			}
			if spanErr.Attr != c.attr || spanErr.Value != c.value || spanErr.Row != c.wantRow || spanErr.Col != c.wantCol {
				t.Fatalf("spanErr = %+v, want attr=%v value=%v at (%d,%d)",
					spanErr, c.attr, c.value, c.wantRow, c.wantCol)
			}
		})
	}
}

// rowspan crossing its row group boundary must return a recognizable error
const rawTableCrossGroupHtml = `
<table id="crossgroup">
	<thead><tr><th>A</th><th>B</th></tr></thead>
	<tbody>
		<tr><td rowspan="3">1</td><td>2</td></tr>
		<tr><td>3</td></tr>
	</tbody>
</table>
`

func TestParseTableCrossRowGroupSpan(t *testing.T) {
	type RowData struct {
		A string `pagser:"A"`
	}
	var rows []RowData
	err := New().ParseTable(&rows, rawTableCrossGroupHtml, "#crossgroup")
	var groupErr *TableRowGroupSpanError
	if !errors.As(err, &groupErr) {
		t.Fatalf("err = %v, want TableRowGroupSpanError", err)
	}
	if groupErr.Row != 1 || groupErr.Col != 0 || groupErr.RowSpan != 3 || groupErr.GroupRows != 2 {
		t.Fatalf("groupErr = %+v, want rowspan=3 at (1,0) with 2 group rows", groupErr)
	}
}

// nested table cells and rows must not leak into the outer table
const rawTableNestedHtml = `
<table id="outer">
	<thead><tr><th>名称</th><th>说明</th></tr></thead>
	<tbody>
		<tr>
			<td>item</td>
			<td>text<table id="inner"><thead><tr><th>innerH</th></tr></thead><tbody><tr><td>inner</td></tr></tbody></table></td>
		</tr>
	</tbody>
</table>
`

func TestParseTableNested(t *testing.T) {
	type RowData struct {
		Name string `pagser:"名称"`
		Desc string `pagser:"说明"`
	}
	var rows []RowData
	err := New().ParseTable(&rows, rawTableNestedHtml, "#outer")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want exactly 1 row, nested rows must not leak", rows)
	}
	if rows[0].Name != "item" || rows[0].Desc != "text" {
		t.Fatalf("rows = %+v, want desc %q without nested table text", rows, "text")
	}
}

// column path not found in the table header
func TestParseTableColumnNotFound(t *testing.T) {
	type RowData struct {
		Name string `pagser:"不存在"`
	}
	var rows []RowData
	err := New().ParseTable(&rows, rawTableMoneyHtml, "#money")
	var notFoundErr *TableColumnNotFoundError
	if !errors.As(err, &notFoundErr) {
		t.Fatalf("err = %v, want TableColumnNotFoundError", err)
	}
	if notFoundErr.Path != "不存在" {
		t.Fatalf("notFoundErr = %+v, want path 不存在", notFoundErr)
	}
}

// CastError semantics stay the same as the Parse series
func TestParseTableCastError(t *testing.T) {
	const html = `
	<table id="num">
		<thead><tr><th>名称</th><th>数量</th></tr></thead>
		<tbody><tr><td>a</td><td>abc</td></tr></tbody>
	</table>
	`
	type RowData struct {
		Name string `pagser:"名称"`
		Num  int    `pagser:"数量"`
	}

	//CastError=false: bad number converts to zero value
	var rows []RowData
	err := New().ParseTable(&rows, html, "#num")
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Num != 0 {
		t.Fatalf("rows = %+v, want Num=0", rows)
	}

	//CastError=true: bad number returns an error
	cfg := DefaultConfig()
	cfg.CastError = true
	p, err := NewWithConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var rows2 []RowData
	err = p.ParseTable(&rows2, html, "#num")
	if err == nil || !strings.Contains(err.Error(), "数量") {
		t.Fatalf("err = %v, want a cast error mentioning the column path", err)
	}
}

// table not found by selector
func TestParseTableNotFound(t *testing.T) {
	type RowData struct {
		Name string `pagser:"名称"`
	}
	var rows []RowData
	err := New().ParseTable(&rows, `<div>no table here</div>`, "#none")
	var notFoundErr *TableNotFoundError
	if !errors.As(err, &notFoundErr) {
		t.Fatalf("err = %v, want TableNotFoundError", err)
	}
}

// slice of struct pointers also works
func TestParseTablePtrSlice(t *testing.T) {
	type RowData struct {
		City  string `pagser:"城市"`
		Sales int    `pagser:"销量"`
	}
	var rows []*RowData
	err := New().ParseTable(&rows, rawTableMultiTbodyHtml, "#multi")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].City != "北京" || rows[2].Sales != 30 {
		t.Fatalf("rows = %+v", rows)
	}
}

// the Parse series keeps working on the same Pagser instance
func TestParseTableKeepsParseBehavior(t *testing.T) {
	type PageData struct {
		Title string `pagser:"h1"`
	}
	var page PageData
	err := New().Parse(&page, `<h1>Hello</h1>`)
	if err != nil {
		t.Fatal(err)
	}
	if page.Title != "Hello" {
		t.Fatalf("page = %+v", page)
	}
}

func assertTableRow(t *testing.T, got []TableCell, want []TableCell) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("row has %d cells, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Text != w.Text || g.Row != w.Row || g.Col != w.Col ||
			g.RowSpan != w.RowSpan || g.ColSpan != w.ColSpan ||
			g.Covered != w.Covered || g.Empty != w.Empty {
			t.Fatalf("cell %d = {Text:%q Row:%d Col:%d RowSpan:%d ColSpan:%d Covered:%v Empty:%v}, want %+v",
				i, g.Text, g.Row, g.Col, g.RowSpan, g.ColSpan, g.Covered, g.Empty, w)
		}
	}
}

func ExamplePagser_ParseTable() {
	const html = `
	<table>
		<thead>
			<tr><th rowspan="2">产品</th><th colspan="2">销量</th></tr>
			<tr><th>线上</th><th>线下</th></tr>
		</thead>
		<tbody>
			<tr><td>手机</td><td>10</td><td>20</td></tr>
			<tr><td>平板</td><td>5</td><td>8</td></tr>
		</tbody>
	</table>
	`
	type RowData struct {
		Name    string `pagser:"产品"`
		Online  int    `pagser:"销量/线上"`
		Offline int    `pagser:"销量/线下"`
	}
	var rows []RowData
	err := New().ParseTable(&rows, html)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	for _, row := range rows {
		fmt.Printf("%v online=%v offline=%v\n", row.Name, row.Online, row.Offline)
	}
	//Output:
	//手机 online=10 offline=20
	//平板 online=5 offline=8
}
