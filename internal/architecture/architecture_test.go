// Package architecture 提供后端目标架构的静态守卫测试（G8）。
//
// 扫描范围：internal/ 下的“新树”——internal/modules/、internal/platform/、
// internal/shared/、internal/transport/（以及将来的 internal/app/）。
// 旧 internal/ 顶层包（access/admin/agent/ai/api/audit/auth/config/content/db/id/jobs/
// logging/media/migrate/notice/ratelimit/richtext/server/settings/social/storage/websearch
// 等）计划在 G5 cutover 后删除，本守卫只覆盖新树；G5 cutover 后删除旧包，本守卫只覆盖新树。
// 例外：规则 3 会显式扫描 internal/server 与 internal/transport/router 的依赖方向。
//
// 规则：
//  1. 模块四层 import 限制：domain 不得 import gin/net/http/pgx/database/sql；app 不得 import
//     gin/net/http/pgx 且字符串字面量不得含 SQL 关键字；repo 不得 import gin/net/http；
//     http 不得 import pgx/database/sql。
//  2. 模块互斥：internal/modules/x/** 不得 import internal/modules/y（x≠y）。
//  3. 依赖方向：server 不得 import modules 或 transport/router；transport/router 不得 import
//     modules；shared/platform 不得 import modules/transport/server。
//  4. SQL 位置：新树中的 SQL 关键字只允许出现在 modules/*/repo/** 与
//     platform/{jobs,audit,settings,migrate}/**。
//  5. Gin 位置：gin.Context 只允许出现在 modules/*/http/**、transport/**、server。
//  6. 时间位置：time.Now() 只允许 platform/{clock,id,logging}/** 与
//     transport/middleware/accesslog.go；SQL 时间函数（now()/CURRENT_DATE/::date/interval）
//     只允许 platform/migrate/**。
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

// TestGuardCoversNewTree 先做覆盖性自检：新树必须存在且能解析，避免规则因扫描不到
// 文件而“假通过”。同时把扫描规模写入测试日志，便于排查守卫失效。
func TestGuardCoversNewTree(t *testing.T) {
	files := loadSourceFiles(t)
	newTree := 0
	modules := map[string]bool{}
	for _, f := range files {
		if !isNewTree(f.rel) {
			continue
		}
		newTree++
		if mod, _, ok := moduleLayer(f.rel); ok {
			modules[mod] = true
		}
	}
	if newTree == 0 {
		t.Fatal("没有扫描到任何新树文件：请确认 internal/{modules,platform,shared,transport} 存在，且本守卫未被误删")
	}
	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	sort.Strings(names)
	t.Logf("扫描新树非测试 .go 文件 %d 个；模块 %d 个：%s", newTree, len(names), strings.Join(names, ", "))
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

// TestSQLLocation 校验新树中的 SQL 关键字只出现在 repo/平台 repo 文件里。
func TestSQLLocation(t *testing.T) {
	files := loadSourceFiles(t)
	var violations []violation
	for _, f := range files {
		if !isNewTree(f.rel) || sqlAllowed(f.rel) {
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

// loadSourceFiles 递归解析 internal/ 下与守卫规则相关的非测试 .go 文件。
// 只解析新树（internal/{modules,platform,shared,transport,app}）以及规则 3 显式扫描的
// internal/server、internal/transport/router；其它 legacy 目录（G5 cutover 后删除）不解析。
// 跳过隐藏目录、testdata/vendor 与编辑工具遗留的 *.tmpdir 目录。
func loadSourceFiles(t *testing.T) []sourceFile {
	t.Helper()
	root := repoRoot(t)
	internalRoot := filepath.Join(root, "internal")

	var files []sourceFile
	err := filepath.WalkDir(internalRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == internalRoot {
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
		t.Fatalf("扫描 internal/ 失败: %v", err)
	}
	return files
}

// relevantForGuard 判断某文件是否属于本守卫的扫描范围（决定是否解析）。
func relevantForGuard(rel string) bool {
	if isNewTree(rel) {
		return true
	}
	return under(rel, "internal/server") || under(rel, "internal/transport/router")
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

// sqlAllowed 判断新树中哪些目录允许出现 SQL 关键字。
func sqlAllowed(rel string) bool {
	if _, layer, ok := moduleLayer(rel); ok && layer == "repo" {
		return true
	}
	for _, dir := range []string{
		"internal/platform/jobs",
		"internal/platform/audit",
		"internal/platform/settings",
		"internal/platform/migrate",
	} {
		if under(rel, dir) {
			return true
		}
	}
	return false
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
