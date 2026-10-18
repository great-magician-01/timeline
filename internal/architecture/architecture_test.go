// Package architecture 提供后端目标架构的静态守卫测试（G8）。
//
// 扫描范围：internal/ 下的“新树”——internal/modules/、internal/platform/、
// internal/shared/、internal/transport/、internal/app/；规则 3 显式扫描
// internal/server 与 internal/transport/router；cmd/ 目录随架构守卫一起扫描，
// 其中 cmd/server 额外断言 import 白名单；internal/testkit 是唯一显式 SQL 例外
// （测试基础设施：直接读写隔离 schema 以构造测试夹具）。
//
// 旧 internal/ 顶层业务包已在重构收尾中删除（需要对照时用 git show b6fed97:<path>），
// 本守卫只覆盖上述目标结构，不再维护旧包白名单。
//
// 规则：
//  1. 模块四层 import 限制：domain 不得 import gin/net/http/pgx/database/sql；app 不得 import
//     gin/net/http/pgx 且字符串字面量不得含 SQL 关键字；repo 不得 import gin/net/http；
//     http 不得 import pgx/database/sql。
//  2. 模块互斥：internal/modules/x/** 不得 import internal/modules/y（x≠y）。
//  3. 依赖方向：server 不得 import modules 或 transport/router；transport/router 不得 import
//     modules；shared/platform 不得 import modules/transport/server。
//  4. SQL 位置：SQL 关键字只允许出现在 modules/*/repo/**、platform/{jobs,audit,settings,
//     migrate}/** 与 internal/testkit（测试基础设施例外）。
//  5. Gin 位置：gin.Context 只允许出现在 modules/*/http/**、transport/**、server。
//  6. 时间位置：time.Now() 只允许 platform/{clock,id,logging}/** 与
//     transport/middleware/accesslog.go；SQL 时间函数（now()/CURRENT_DATE/::date/interval）
//     只允许 platform/migrate/**。
//  7. 事务原语：pool/tx/conn 的 Begin/Commit/Rollback 只允许 platform/{db,migrate,jobs}
//     （迁移器与作业运行器需要自管理事务；业务层必须走 db.TxManager）。
//  8. 遗留符号：app/domain/repo 中不得出现 c.JSON、internalError(c)、access.Querier
//     （http handler 的 c.JSON 是 DTO 输出的合法方式，不在此规则范围）。
//  9. cmd/server import 白名单：只允许 app、server、platform/{config,db,id,logging,
//     migrate,storage} 与标准库/第三方；业务服务构造必须在组合根。
//
// 所有规则都只扫描非测试 .go 文件；测试文件与 migrations/*.sql 不在本守卫范围。
package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// modulePath 是本仓库 Go module 前缀。
const modulePath = "github.com/great-magician-01/timeline/"

// 新树前缀：本守卫的主要扫描范围。
var newTreePrefixes = []string{
	"internal/modules",
	"internal/platform",
	"internal/shared",
	"internal/transport",
	"internal/app",
}

// SQL 关键字与 SQL 时间函数检测。
//
// 关键字优先按 SQL 约定的大写写法匹配（仓库内 SQL 均为大写），避免把 HTML 元素名
// 之类的小写英文单词（如 shared/richtext 的 "select"）误判为 SQL；若小写文本同时
// 含明显 SQL 结构信号（FROM/WHERE/INTO/ORDER BY/VALUES/$n），则按小写 SQL 兜底。
var (
	sqlKeywordPattern      = regexp.MustCompile(`\bSELECT\b|\bINSERT\s+INTO\b|\bUPDATE\b|\bDELETE\s+FROM\b`)
	sqlKeywordLowerPattern = regexp.MustCompile(`(?i)\bselect\b|\binsert\s+into\b|\bupdate\b|\bdelete\s+from\b`)
	sqlContextPattern      = regexp.MustCompile(`(?i)\bfrom\b|\bwhere\b|\binto\b|\border\s+by\b|\bvalues\s*\(|\$\d`)
	// interval 只匹配 SQL 用法（::interval 或 interval '...'），避免误报普通英文单词。
	sqlTimePattern = regexp.MustCompile(`(?i)\bnow\s*\(\s*\)|CURRENT_DATE|::\s*date\b|::\s*interval\b|\binterval\s+['"]`)
	modulePattern  = regexp.MustCompile(`internal/modules/([A-Za-z0-9_]+)`)
)

// sqlKeywordIn 返回 value 中出现的 SQL 关键字；无则返回空字符串。
func sqlKeywordIn(value string) string {
	if keyword := sqlKeywordPattern.FindString(value); keyword != "" {
		return keyword
	}
	if sqlKeywordLowerPattern.MatchString(value) && sqlContextPattern.MatchString(value) {
		return sqlKeywordLowerPattern.FindString(value)
	}
	return ""
}

// sourceFile 是一个已解析的非测试 Go 源文件。
type sourceFile struct {
	rel  string // 仓库根相对路径，统一使用 “/” 分隔
	fset *token.FileSet
	file *ast.File
}

// violation 是一条架构违规。
type violation struct {
	rel  string
	line int
	msg  string
}

func (v violation) String() string {
	if v.line > 0 {
		return fmt.Sprintf("%s:%d: %s", v.rel, v.line, v.msg)
	}
	return fmt.Sprintf("%s: %s", v.rel, v.msg)
}

// TestGuardCoversNewTree 先做覆盖性自检：新树、cmd/ 与 testkit 必须存在且能解析，
// 避免规则因扫描不到文件而“假通过”。同时把扫描规模写入测试日志，便于排查守卫失效。
func TestGuardCoversNewTree(t *testing.T) {
	files := loadSourceFiles(t)
	newTree, cmdFiles, testkitFiles := 0, 0, 0
	modules := map[string]bool{}
	for _, f := range files {
		switch {
		case isNewTree(f.rel):
			newTree++
			if mod, _, ok := moduleLayer(f.rel); ok {
				modules[mod] = true
			}
		case under(f.rel, "cmd"):
			cmdFiles++
		case under(f.rel, "internal/testkit"):
			testkitFiles++
		}
	}
	if newTree == 0 {
		t.Fatal("没有扫描到任何新树文件：请确认 internal/{modules,platform,shared,transport,app} 存在，且本守卫未被误删")
	}
	if cmdFiles == 0 {
		t.Fatal("没有扫描到 cmd/ 源码：cmd/server import 白名单规则将假通过")
	}
	if testkitFiles == 0 {
		t.Fatal("没有扫描到 internal/testkit 源码：SQL 例外无法显式核查")
	}
	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	sort.Strings(names)
	t.Logf("扫描新树非测试 .go 文件 %d 个；模块 %d 个：%s；cmd 文件 %d 个；testkit 文件 %d 个",
		newTree, len(names), strings.Join(names, ", "), cmdFiles, testkitFiles)
}

// TestModuleLayerImports 校验模块四层（domain/app/repo/http）的 import 限制，
// 以及 app 层源码字符串不得包含 SQL 关键字。
func TestModuleLayerImports(t *testing.T) {
	files := loadSourceFiles(t)
	var violations []violation
	for _, f := range files {
		if !isNewTree(f.rel) {
			continue
		}
		_, layer, ok := moduleLayer(f.rel)
		if !ok {
			continue
		}
		for _, imp := range importsOf(f) {
			var forbidden []string
			switch layer {
			case "domain":
				forbidden = layerForbidden(layer, imp.path)
			case "app":
				forbidden = layerForbidden(layer, imp.path)
			case "repo":
				forbidden = layerForbidden(layer, imp.path)
			case "http":
				forbidden = layerForbidden(layer, imp.path)
			}
			if len(forbidden) > 0 {
				violations = append(violations, violation{
					rel: f.rel, line: imp.line,
					msg: fmt.Sprintf("%s 层不得 import %s（命中规则：%s）", layer, imp.path, strings.Join(forbidden, "、")),
				})
			}
		}
		if layer == "app" {
			for _, lit := range stringLiterals(f) {
				if keyword := sqlKeywordIn(lit.value); keyword != "" {
					violations = append(violations, violation{
						rel: f.rel, line: lit.line,
						msg: fmt.Sprintf("app 层源码字符串含 SQL 关键字 %q：%s", keyword, snippet(lit.value)),
					})
				}
			}
		}
	}
	report(t, "规则 1（模块四层 import 限制）", violations)
}

// TestModuleIsolation 校验模块互斥：internal/modules/x 不得 import internal/modules/y。
func TestModuleIsolation(t *testing.T) {
	files := loadSourceFiles(t)
	var violations []violation
	for _, f := range files {
		if !isNewTree(f.rel) {
			continue
		}
		mod, _, ok := moduleLayer(f.rel)
		if !ok {
			continue
		}
		for _, imp := range importsOf(f) {
			m := modulePattern.FindStringSubmatch(imp.path)
			if len(m) != 2 {
				continue
			}
			if target := m[1]; target != mod {
				violations = append(violations, violation{
					rel: f.rel, line: imp.line,
					msg: fmt.Sprintf("模块 %s 不得 import 其它模块 %s（%s）", mod, target, imp.path),
				})
			}
		}
	}
	report(t, "规则 2（模块互斥）", violations)
}

// TestDependencyDirection 校验组合根/传输层/共享层的依赖方向。
func TestDependencyDirection(t *testing.T) {
	files := loadSourceFiles(t)
	var violations []violation
	for _, f := range files {
		var forbidden []string
		switch {
		case under(f.rel, "internal/server"):
			forbidden = []string{modulePath + "internal/modules", modulePath + "internal/transport/router"}
		case under(f.rel, "internal/transport/router"):
			forbidden = []string{modulePath + "internal/modules"}
		case under(f.rel, "internal/shared"), under(f.rel, "internal/platform"):
			forbidden = []string{
				modulePath + "internal/modules",
				modulePath + "internal/transport",
				modulePath + "internal/server",
			}
		default:
			continue
		}
		for _, imp := range importsOf(f) {
			for _, prefix := range forbidden {
				if strings.HasPrefix(imp.path, prefix) {
					violations = append(violations, violation{
						rel: f.rel, line: imp.line,
						msg: fmt.Sprintf("%s 不得 import %s", dirOf(f.rel), imp.path),
					})
					break
				}
			}
		}
	}
	report(t, "规则 3（依赖方向）", violations)
}

// TestSQLLocation 校验 SQL 关键字只出现在 repo/平台 repo 与显式例外（testkit）。
func TestSQLLocation(t *testing.T) {
	files := loadSourceFiles(t)
	var violations []violation
	for _, f := range files {
		if !sqlScope(f.rel) || sqlAllowed(f.rel) {
			continue
		}
		for _, lit := range stringLiterals(f) {
			if keyword := sqlKeywordIn(lit.value); keyword != "" {
				violations = append(violations, violation{
					rel: f.rel, line: lit.line,
					msg: fmt.Sprintf("SQL 关键字 %q 出现在非 repo 层：%s", keyword, snippet(lit.value)),
				})
			}
		}
	}
	report(t, "规则 4（SQL 位置）", violations)
}

// TestGinLocation 校验 gin.Context 只出现在 HTTP/传输层。
func TestGinLocation(t *testing.T) {
	files := loadSourceFiles(t)
	var violations []violation
	for _, f := range files {
		if !isNewTree(f.rel) || ginAllowed(f.rel) {
			continue
		}
		for _, line := range ginContextLines(f) {
			violations = append(violations, violation{
				rel: f.rel, line: line,
				msg: "gin.Context 只允许出现在 modules/*/http、transport/**、server",
			})
		}
	}
	report(t, "规则 5（Gin 位置）", violations)
}

// TestTimeLocation 校验应用时钟与 SQL 时间函数的位置白名单。
func TestTimeLocation(t *testing.T) {
	files := loadSourceFiles(t)
	var violations []violation
	for _, f := range files {
		if !isNewTree(f.rel) {
			continue
		}
		if !timeNowAllowed(f.rel) {
			for _, line := range timeNowLines(f) {
				violations = append(violations, violation{
					rel: f.rel, line: line,
					msg: "time.Now() 只允许出现在 platform/{clock,id,logging} 与 transport/middleware/accesslog.go",
				})
			}
		}
		if !sqlTimeAllowed(f.rel) {
			for _, lit := range stringLiterals(f) {
				if tok := sqlTimePattern.FindString(lit.value); tok != "" {
					violations = append(violations, violation{
						rel: f.rel, line: lit.line,
						msg: fmt.Sprintf("SQL 时间函数 %q 只允许 platform/migrate 白名单：%s", tok, snippet(lit.value)),
					})
				}
			}
		}
	}
	report(t, "规则 6（时间位置）", violations)
}

// TestDBPrimitiveLocation 校验事务原语只出现在平台数据库层、迁移器与作业运行器。
// 业务层（modules/app、repo、http、组合根）必须通过 db.TxManager.WithTx 编排事务，
// 不允许自行 Begin/Commit/Rollback。
func TestDBPrimitiveLocation(t *testing.T) {
	files := loadSourceFiles(t)
	var violations []violation
	allowedHits := 0
	for _, f := range files {
		if txPrimitiveAllowed(f.rel) {
			allowedHits += len(txPrimitiveLines(f))
			continue
		}
		for _, line := range txPrimitiveLines(f) {
			violations = append(violations, violation{
				rel: f.rel, line: line,
				msg: "Begin/Commit/Rollback 只允许出现在 platform/{db,migrate,jobs}；业务编排请使用 db.TxManager.WithTx",
			})
		}
	}
	if allowedHits == 0 {
		t.Fatal("未在允许位置扫描到任何事务原语，规则检测可能失效（AST 检测器回归）")
	}
	report(t, "规则 7（事务原语位置）", violations)
}

// TestLegacySymbols 校验旧架构遗留符号不会回归：app/domain/repo 中不得出现
// c.JSON（DTO 输出只属于 http handler）、internalError(c)（旧 handler 错误辅助）、
// access.Querier（旧带 SQL 的授权包接口）。
func TestLegacySymbols(t *testing.T) {
	files := loadSourceFiles(t)
	var violations []violation
	for _, f := range files {
		_, layer, ok := moduleLayer(f.rel)
		if !ok || (layer != "app" && layer != "domain" && layer != "repo") {
			continue
		}
		for _, line := range legacySymbolLines(f) {
			violations = append(violations, violation{
				rel: f.rel, line: line,
				msg: "app/domain/repo 不得出现 c.JSON、internalError(c) 或 access.Querier 等旧架构符号",
			})
		}
	}
	report(t, "规则 8（遗留符号）", violations)
}

// TestCmdServerImports 校验 cmd/server 只依赖组合根、slim server 与白名单内的平台包，
// 不得直接依赖 modules/transport/router（业务装配必须在 internal/app）。
func TestCmdServerImports(t *testing.T) {
	files := loadSourceFiles(t)
	var violations []violation
	checked := 0
	for _, f := range files {
		if !under(f.rel, "cmd/server") {
			continue
		}
		checked++
		for _, imp := range importsOf(f) {
			if !strings.HasPrefix(imp.path, modulePath) {
				continue // 标准库与第三方依赖不在白名单规则范围
			}
			if cmdServerAllowedImports[imp.path] {
				continue
			}
			violations = append(violations, violation{
				rel: f.rel, line: imp.line,
				msg: fmt.Sprintf("cmd/server 不得 import %s；业务装配应在 internal/app", imp.path),
			})
		}
	}
	if checked == 0 {
		t.Fatal("没有扫描到 cmd/server 源码，import 白名单规则未生效")
	}
	report(t, "规则 9（cmd/server import 白名单）", violations)
}

// TestRuleDetectors 用内存中的夹具源码验证 AST 检测器本身有效，避免规则因检测器
// 回归而"永远通过"。
func TestRuleDetectors(t *testing.T) {
	const src = `package fixture
func f(c any, pool any) {
	c.JSON(200, nil)
	internalError(c)
	var _ access.Querier
	pool.Begin(nil)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, 0)
	if err != nil {
		t.Fatalf("解析夹具源码失败: %v", err)
	}
	f := sourceFile{rel: "internal/modules/x/app/fixture.go", fset: fset, file: file}
	if got := len(legacySymbolLines(f)); got != 3 {
		t.Fatalf("遗留符号检测应命中 3 处（c.JSON/internalError(c)/access.Querier），实际 %d", got)
	}
	if got := len(txPrimitiveLines(f)); got != 1 {
		t.Fatalf("事务原语检测应命中 1 处（pool.Begin），实际 %d", got)
	}
	if !cmdServerAllowedImports[modulePath+"internal/app"] {
		t.Fatal("cmd/server 白名单必须包含组合根 internal/app")
	}
	if cmdServerAllowedImports[modulePath+"internal/modules"] {
		t.Fatal("cmd/server 白名单不应包含 internal/modules")
	}
}

// ---------- 扫描与解析 ----------

// repoRoot 从测试工作目录（internal/architecture）向上查找 go.mod。
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("获取当前工作目录失败: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("无法从 %s 向上找到包含 go.mod 的仓库根目录", dir)
		}
		dir = parent
	}
}

// loadSourceFiles 递归解析 internal/ 与 cmd/ 下与守卫规则相关的非测试 .go 文件。
// 解析范围：新树（internal/{modules,platform,shared,transport,app}）、规则 3 显式扫描的
// internal/server 与 internal/transport/router、白名单例外的 internal/testkit，以及 cmd/。
// 跳过隐藏目录、testdata/vendor 与编辑工具遗留的 *.tmpdir 目录。
func loadSourceFiles(t *testing.T) []sourceFile {
	t.Helper()
	root := repoRoot(t)
	scanRoots := []string{
		filepath.Join(root, "internal"),
		filepath.Join(root, "cmd"),
	}

	var files []sourceFile
	for _, scanRoot := range scanRoots {
		err := filepath.WalkDir(scanRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path == scanRoot {
					return nil
				}
				name := d.Name()
				if strings.HasPrefix(name, ".") || strings.HasSuffix(name, "tmpdir") ||
					name == "testdata" || name == "vendor" || name == "node_modules" {
					return fs.SkipDir
				}
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if !relevantForGuard(rel) {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("读取 %s 失败: %w", path, err)
			}
			fset := token.NewFileSet()
			file, parseErr := parser.ParseFile(fset, path, src, parser.ParseComments)
			if parseErr != nil {
				return fmt.Errorf("解析 %s 失败（架构守卫要求源码可解析）: %w", rel, parseErr)
			}
			files = append(files, sourceFile{rel: rel, fset: fset, file: file})
			return nil
		})
		if err != nil {
			t.Fatalf("扫描 %s 失败: %v", scanRoot, err)
		}
	}
	return files
}

// relevantForGuard 判断某文件是否属于本守卫的扫描范围（决定是否解析）。
func relevantForGuard(rel string) bool {
	if isNewTree(rel) {
		return true
	}
	return under(rel, "internal/server") || under(rel, "internal/transport/router") ||
		under(rel, "internal/testkit") || under(rel, "cmd")
}

type importRef struct {
	path string
	line int
}

func importsOf(f sourceFile) []importRef {
	refs := make([]importRef, 0, len(f.file.Imports))
	for _, imp := range f.file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			path = imp.Path.Value
		}
		refs = append(refs, importRef{path: path, line: f.fset.Position(imp.Pos()).Line})
	}
	return refs
}

type literalRef struct {
	value string
	line  int
}

func stringLiterals(f sourceFile) []literalRef {
	var literals []literalRef
	ast.Inspect(f.file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			value = lit.Value
		}
		literals = append(literals, literalRef{value: value, line: f.fset.Position(lit.Pos()).Line})
		return true
	})
	return literals
}

// timeNowLines 返回 file 中所有 time.Now() 调用的行号。
func timeNowLines(f sourceFile) []int {
	var lines []int
	ast.Inspect(f.file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if ok && ident.Name == "time" && sel.Sel.Name == "Now" {
			lines = append(lines, f.fset.Position(call.Pos()).Line)
		}
		return true
	})
	return lines
}

// ginContextLines 返回 file 中所有 gin.Context（含 *gin.Context）引用的行号。
func ginContextLines(f sourceFile) []int {
	var lines []int
	ast.Inspect(f.file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if ok && ident.Name == "gin" && sel.Sel.Name == "Context" {
			lines = append(lines, f.fset.Position(sel.Pos()).Line)
		}
		return true
	})
	return lines
}

// ---------- 路径与白名单判断 ----------

func under(rel, dir string) bool {
	return rel == dir || strings.HasPrefix(rel, dir+"/")
}

func isNewTree(rel string) bool {
	for _, prefix := range newTreePrefixes {
		if under(rel, prefix) {
			return true
		}
	}
	return false
}

// moduleLayer 从 internal/modules/<module>/<layer>/<file> 中解析模块名与层名。
func moduleLayer(rel string) (module, layer string, ok bool) {
	parts := strings.Split(rel, "/")
	if len(parts) < 5 || parts[0] != "internal" || parts[1] != "modules" {
		return "", "", false
	}
	return parts[2], parts[3], true
}

// layerForbidden 返回某层被禁止 import 的说明（空切片表示允许）。
func layerForbidden(layer, importPath string) []string {
	var forbidden []string
	switch layer {
	case "domain":
		if strings.Contains(importPath, "gin-gonic/gin") {
			forbidden = append(forbidden, "gin")
		}
		if importPath == "net/http" || strings.HasPrefix(importPath, "net/http/") {
			forbidden = append(forbidden, "net/http")
		}
		if strings.Contains(importPath, "jackc/pgx") {
			forbidden = append(forbidden, "pgx")
		}
		if importPath == "database/sql" || strings.HasPrefix(importPath, "database/sql/") {
			forbidden = append(forbidden, "database/sql")
		}
	case "app":
		if strings.Contains(importPath, "gin-gonic/gin") {
			forbidden = append(forbidden, "gin")
		}
		if importPath == "net/http" || strings.HasPrefix(importPath, "net/http/") {
			forbidden = append(forbidden, "net/http")
		}
		if strings.Contains(importPath, "jackc/pgx") {
			forbidden = append(forbidden, "pgx")
		}
	case "repo":
		if strings.Contains(importPath, "gin-gonic/gin") {
			forbidden = append(forbidden, "gin")
		}
		if importPath == "net/http" || strings.HasPrefix(importPath, "net/http/") {
			forbidden = append(forbidden, "net/http")
		}
	case "http":
		if strings.Contains(importPath, "jackc/pgx") {
			forbidden = append(forbidden, "pgx")
		}
		if importPath == "database/sql" || strings.HasPrefix(importPath, "database/sql/") {
			forbidden = append(forbidden, "database/sql")
		}
	}
	return forbidden
}

// sqlScope 返回 SQL 位置规则覆盖的文件范围：新树 + internal/testkit 显式例外。
func sqlScope(rel string) bool {
	return isNewTree(rel) || under(rel, "internal/testkit")
}

// sqlAllowed 判断哪些目录允许出现 SQL 关键字。
// internal/testkit 是唯一的测试基础设施例外（直接读写隔离测试 schema 构造夹具），
// 不属于运行期业务代码；新增例外必须在此显式登记并说明理由。
func sqlAllowed(rel string) bool {
	if _, layer, ok := moduleLayer(rel); ok && layer == "repo" {
		return true
	}
	for _, dir := range []string{
		"internal/platform/jobs",
		"internal/platform/audit",
		"internal/platform/settings",
		"internal/platform/migrate",
		"internal/testkit",
	} {
		if under(rel, dir) {
			return true
		}
	}
	return false
}

// cmdServerAllowedImports 是 cmd/server 允许 import 的本仓库包白名单：
// 组合根（app）、slim server 与进程生命周期所需的平台包（配置/连接池/ID/日志/迁移/存储）。
// modules、transport（除 server 内部使用）、shared 都不允许直接出现在 main 中。
var cmdServerAllowedImports = map[string]bool{
	modulePath + "internal/app":              true,
	modulePath + "internal/server":           true,
	modulePath + "internal/platform/config":  true,
	modulePath + "internal/platform/db":      true,
	modulePath + "internal/platform/id":      true,
	modulePath + "internal/platform/logging": true,
	modulePath + "internal/platform/migrate": true,
	modulePath + "internal/platform/storage": true,
}

// txPrimitiveMethods 是事务原语方法名：只允许平台数据库层、迁移器与作业运行器使用。
var txPrimitiveMethods = map[string]bool{"Begin": true, "Commit": true, "Rollback": true}

// txPrimitiveAllowed 判断某文件是否允许直接调用事务原语。
//   - platform/db：TxManager/Readiness 的实现本体；
//   - platform/migrate：迁移器需要自管理迁移事务与 no-transaction 路径；
//   - platform/jobs：Runner 内部例外（设计文档 §11.4 的既有例外）。
func txPrimitiveAllowed(rel string) bool {
	for _, dir := range []string{
		"internal/platform/db",
		"internal/platform/migrate",
		"internal/platform/jobs",
	} {
		if under(rel, dir) {
			return true
		}
	}
	return false
}

// txPrimitiveLines 返回文件内 Begin/Commit/Rollback 调用的行号。
func txPrimitiveLines(f sourceFile) []int {
	var lines []int
	ast.Inspect(f.file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !txPrimitiveMethods[sel.Sel.Name] {
			return true
		}
		lines = append(lines, f.fset.Position(call.Pos()).Line)
		return true
	})
	return lines
}

// legacySymbolLines 返回 app/domain/repo 中旧架构遗留符号的行号：
// c.JSON、internalError(c) 与 access.Querier。
func legacySymbolLines(f sourceFile) []int {
	var lines []int
	isIdent := func(expr ast.Expr, name string) bool {
		ident, ok := expr.(*ast.Ident)
		return ok && ident.Name == name
	}
	ast.Inspect(f.file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			// c.JSON 与 access.Querier
			if isIdent(node.X, "c") && node.Sel.Name == "JSON" ||
				isIdent(node.X, "access") && node.Sel.Name == "Querier" {
				lines = append(lines, f.fset.Position(node.Pos()).Line)
			}
		case *ast.CallExpr:
			if isIdent(node.Fun, "internalError") && len(node.Args) > 0 && isIdent(node.Args[0], "c") {
				lines = append(lines, f.fset.Position(node.Pos()).Line)
			}
		}
		return true
	})
	return lines
}

func ginAllowed(rel string) bool {
	if _, layer, ok := moduleLayer(rel); ok && layer == "http" {
		return true
	}
	return under(rel, "internal/transport") || under(rel, "internal/server")
}

func timeNowAllowed(rel string) bool {
	for _, dir := range []string{"internal/platform/clock", "internal/platform/id", "internal/platform/logging"} {
		if under(rel, dir) {
			return true
		}
	}
	return rel == "internal/transport/middleware/accesslog.go"
}

func sqlTimeAllowed(rel string) bool {
	return under(rel, "internal/platform/migrate")
}

// ---------- 报告 ----------

func report(t *testing.T, ruleName string, violations []violation) {
	t.Helper()
	if len(violations) == 0 {
		return
	}
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].rel != violations[j].rel {
			return violations[i].rel < violations[j].rel
		}
		return violations[i].line < violations[j].line
	})
	var b strings.Builder
	fmt.Fprintf(&b, "%s：发现 %d 处违规", ruleName, len(violations))
	for i, v := range violations {
		if i >= 50 {
			b.WriteString("\n  ...（其余违规已截断）")
			break
		}
		b.WriteString("\n  ")
		b.WriteString(v.String())
	}
	t.Error(b.String())
}

func dirOf(rel string) string {
	if idx := strings.LastIndex(rel, "/"); idx >= 0 {
		return rel[:idx]
	}
	return rel
}

func snippet(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	if len([]rune(value)) > 80 {
		return string([]rune(value)[:80]) + "…"
	}
	return value
}
