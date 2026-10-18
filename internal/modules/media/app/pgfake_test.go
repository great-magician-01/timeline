package app

// 本文件提供 media/app 单元测试使用的“内存 PostgreSQL 协议桩”：不连接真实数据库、
// 不依赖网络（DialFunc 直接把 pgxpool 接到 net.Pipe），只实现简单查询协议。
//
// 为什么需要它：media/app 的用例层直接调用具体 repo 函数（repo.Get/Insert/...），
// 没有可注入的 db.Executor 端口；要覆盖上传、鉴权读取、逻辑删除、衍生文件与清理的
// 成功/失败分支（而不是在第一次触库处 panic），测试用脚本化的查询结果驱动真实 repo
// SQL 扫描路径。桩只按顺序返回预设结果，不理解 SQL：每个用例按被测代码的确定顺序
// 调用 expect(...) 排好响应，多出/缺少响应都会以明确的测试错误暴露。
//
// 事务（BEGIN/COMMIT/ROLLBACK）属于协议层控制语句，由桩自动应答，不占用结果队列。

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/modules/media/repo"
)

// PostgreSQL 文本协议使用的类型 OID；只列测试需要的几种。
const (
	oidInt8        = 20   // BIGINT
	oidInt4        = 23   // INTEGER
	oidText        = 25   // TEXT
	oidTimestamptz = 1184 // TIMESTAMPTZ
)

// pgColumn 描述一列的名称与类型。
type pgColumn struct {
	name string
	oid  uint32
}

// pgResult 是桩为一次业务查询准备的响应：列 + 行、命令标签、或数据库错误。
type pgResult struct {
	columns []pgColumn
	rows    [][]any
	tag     string // Exec 的 CommandComplete 标签，如 "UPDATE 1"
	err     string // 非空时返回 ErrorResponse
}

// pgStub 是一个可脚本化的 PostgreSQL 协议桩；所有连接共享同一结果队列。
type pgStub struct {
	mu sync.Mutex

	queue   []pgResult
	queries []string
	conns   map[net.Conn]struct{}
	wg      sync.WaitGroup
}

// newPgStub 构造指向内存协议桩的 pgxpool.Pool；t.Cleanup 负责关闭连接池与桩。
func newPgStub(t *testing.T) (*pgxpool.Pool, *pgStub) {
	t.Helper()
	stub := &pgStub{conns: map[net.Conn]struct{}{}}
	cfg, err := pgxpool.ParseConfig("postgres://timeline:secret@pgstub:5432/timeline?sslmode=disable")
	if err != nil {
		t.Fatalf("解析测试 DSN 失败: %v", err)
	}
	// 只使用一个连接，保证脚本化结果按顺序消费。
	cfg.MaxConns = 1
	cfg.ConnConfig.LookupFunc = func(context.Context, string) ([]string, error) {
		return []string{"127.0.0.1"}, nil
	}
	cfg.ConnConfig.DialFunc = func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		stub.mu.Lock()
		stub.conns[server] = struct{}{}
		stub.mu.Unlock()
		stub.wg.Add(1)
		go stub.serve(server)
		return client, nil
	}
	// 简单查询协议：参数由客户端内联为文本，桩无需解析 Bind/Execute。
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("创建测试连接池失败: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		stub.mu.Lock()
		for conn := range stub.conns {
			_ = conn.Close()
		}
		stub.mu.Unlock()
		stub.wg.Wait()
	})
	return pool, stub
}

// expect 追加一组业务查询的响应（BEGIN/COMMIT/ROLLBACK 不占队列）。
func (s *pgStub) expect(results ...pgResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = append(s.queue, results...)
}

// pending 返回尚未被消费的响应数量，用于断言“提前 return 后不再触库”。
func (s *pgStub) pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// recorded 返回桩收到的业务查询文本（含 BEGIN/COMMIT，仅用于排查问题）。
func (s *pgStub) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.queries...)
}

func (s *pgStub) next() (pgResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return pgResult{}, false
	}
	result := s.queue[0]
	s.queue = s.queue[1:]
	return result, true
}

func (s *pgStub) serve(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	// 启动握手：pgx 在 sslmode=disable 下直接发送 StartupMessage。
	var header [8]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return
	}
	length := int(binary.BigEndian.Uint32(header[0:4]))
	if length < 8 || length > 1<<20 {
		return
	}
	if _, err := io.ReadFull(conn, make([]byte, length-8)); err != nil {
		return
	}
	writeStartup(conn)

	for {
		typ, payload, err := readMessage(conn)
		if err != nil {
			return
		}
		switch typ {
		case 'Q':
			s.handleQuery(conn, payload)
		case 'X':
			return
		default:
			writeErrorResponse(conn, fmt.Sprintf("测试协议桩不支持消息 %q，只实现简单查询协议", typ))
			writeReadyForQuery(conn)
		}
	}
}

func (s *pgStub) handleQuery(conn net.Conn, payload []byte) {
	query := strings.TrimSpace(strings.TrimRight(string(payload), "\x00"))
	s.mu.Lock()
	s.queries = append(s.queries, query)
	s.mu.Unlock()

	// BEGIN/COMMIT/ROLLBACK 是协议层的确定应答，不消耗脚本化结果。
	switch strings.ToLower(query) {
	case "begin", "commit", "rollback":
		writeCommandComplete(conn, strings.ToUpper(strings.ToLower(query)))
		writeReadyForQuery(conn)
		return
	}

	result, ok := s.next()
	if !ok {
		writeErrorResponse(conn, "测试协议桩没有为该查询准备结果: "+query)
		writeReadyForQuery(conn)
		return
	}
	if result.err != "" {
		writeErrorResponse(conn, result.err)
		writeReadyForQuery(conn)
		return
	}
	if len(result.columns) > 0 {
		writeRowDescription(conn, result.columns)
		for _, row := range result.rows {
			writeDataRow(conn, row)
		}
		writeCommandComplete(conn, "SELECT "+strconv.Itoa(len(result.rows)))
	} else {
		tag := result.tag
		if tag == "" {
			tag = "OK"
		}
		writeCommandComplete(conn, tag)
	}
	writeReadyForQuery(conn)
}

func writeStartup(w io.Writer) {
	writeInt32Payload(w, 'R', 0) // AuthenticationOk
	writeParameterStatus(w, "server_version", "16.0")
	writeParameterStatus(w, "client_encoding", "UTF8")
	writeParameterStatus(w, "DateStyle", "ISO, MDY")
	writeParameterStatus(w, "standard_conforming_strings", "on")
	writeInt32Payload(w, 'K', 1, 2) // BackendKeyData
	writeReadyForQuery(w)
}

func writeParameterStatus(w io.Writer, key, value string) {
	payload := append([]byte(key), 0)
	payload = append(payload, []byte(value)...)
	payload = append(payload, 0)
	writePayload(w, 'S', payload)
}

// writeInt32Payload 写入只含若干 int32 字段的协议消息。
func writeInt32Payload(w io.Writer, typ byte, values ...int32) {
	payload := make([]byte, len(values)*4)
	for i, value := range values {
		binary.BigEndian.PutUint32(payload[i*4:], uint32(value))
	}
	writePayload(w, typ, payload)
}

func writeReadyForQuery(w io.Writer) { writePayload(w, 'Z', []byte{'I'}) }

func writePayload(w io.Writer, typ byte, payload []byte) {
	message := make([]byte, 5+len(payload))
	message[0] = typ
	binary.BigEndian.PutUint32(message[1:5], uint32(4+len(payload)))
	copy(message[5:], payload)
	_, _ = w.Write(message)
}

func readMessage(r io.Reader) (byte, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}
	length := int(binary.BigEndian.Uint32(header[1:5])) - 4
	if length < 0 || length > 1<<20 {
		return 0, nil, errors.New("非法的协议消息长度")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return header[0], payload, nil
}

func writeRowDescription(w io.Writer, columns []pgColumn) {
	payload := make([]byte, 2)
	binary.BigEndian.PutUint16(payload, uint16(len(columns)))
	for _, column := range columns {
		payload = append(payload, []byte(column.name)...)
		payload = append(payload, 0)
		field := make([]byte, 18)
		// tableOID/attnum 置零；typeSize 与 typeModifier 用 -1 表示变长。
		binary.BigEndian.PutUint32(field[6:10], column.oid)
		binary.BigEndian.PutUint16(field[10:12], 0xFFFF)
		binary.BigEndian.PutUint32(field[12:16], 0xFFFFFFFF)
		payload = append(payload, field...)
	}
	writePayload(w, 'T', payload)
}

func writeDataRow(w io.Writer, row []any) {
	payload := make([]byte, 2)
	binary.BigEndian.PutUint16(payload, uint16(len(row)))
	for _, value := range row {
		if value == nil {
			payload = append(payload, 0xFF, 0xFF, 0xFF, 0xFF) // NULL
			continue
		}
		text := encodeTextValue(value)
		size := make([]byte, 4)
		binary.BigEndian.PutUint32(size, uint32(len(text)))
		payload = append(payload, size...)
		payload = append(payload, text...)
	}
	writePayload(w, 'D', payload)
}

func writeCommandComplete(w io.Writer, tag string) {
	writePayload(w, 'C', append([]byte(tag), 0))
}

func writeErrorResponse(w io.Writer, message string) {
	payload := []byte{'S'}
	payload = append(payload, "ERROR"...)
	payload = append(payload, 0, 'V')
	payload = append(payload, "ERROR"...)
	payload = append(payload, 0, 'C')
	payload = append(payload, "XX000"...)
	payload = append(payload, 0, 'M')
	payload = append(payload, []byte(message)...)
	payload = append(payload, 0, 0)
	writePayload(w, 'E', payload)
}

// encodeTextValue 把 Go 值编码为 PostgreSQL 文本格式。
func encodeTextValue(value any) []byte {
	switch typed := value.(type) {
	case string:
		return []byte(typed)
	case int64:
		return []byte(strconv.FormatInt(typed, 10))
	case int:
		return []byte(strconv.Itoa(typed))
	case bool:
		if typed {
			return []byte("t")
		}
		return []byte("f")
	case time.Time:
		return []byte(typed.UTC().Format("2006-01-02 15:04:05.999999-07"))
	default:
		return []byte(fmt.Sprint(typed))
	}
}

// ===== 结果构造辅助（与 repo 扫描列顺序严格一致） =====

var attachmentRowColumns = []pgColumn{
	{"id", oidInt8}, {"uploader_id", oidInt8}, {"post_id", oidInt8},
	{"kind", oidText}, {"purpose", oidText}, {"original_filename", oidText},
	{"media_type", oidText}, {"size_bytes", oidInt8}, {"storage_key", oidText},
	{"status", oidText}, {"width", oidInt4}, {"height", oidInt4},
	{"duration_ms", oidInt8}, {"created_at", oidTimestamptz}, {"updated_at", oidTimestamptz},
}

var derivativeRowColumns = []pgColumn{
	{"id", oidInt8}, {"attachment_id", oidInt8}, {"derivative_type", oidText},
	{"status", oidText}, {"storage_key", oidText}, {"last_error", oidText},
	{"attempts", oidInt4}, {"created_at", oidTimestamptz}, {"updated_at", oidTimestamptz},
}

var idColumn = []pgColumn{{"id", oidInt8}}

var usageColumn = []pgColumn{{"used_bytes", oidInt8}}

var derivativeStatusColumns = []pgColumn{
	{"attachment_id", oidInt8}, {"derivative_type", oidText}, {"status", oidText},
}

// attachmentResult 返回一条附件行（模拟 repo.Get）。
func attachmentResult(row repo.AttachmentRow) pgResult {
	return pgResult{
		columns: attachmentRowColumns,
		rows:    [][]any{attachmentRowValues(row)},
	}
}

// attachmentRowsResult 返回多条附件行（模拟 repo.ListByIDs）。
func attachmentRowsResult(rows ...repo.AttachmentRow) pgResult {
	values := make([][]any, 0, len(rows))
	for _, row := range rows {
		values = append(values, attachmentRowValues(row))
	}
	return pgResult{columns: attachmentRowColumns, rows: values}
}

// noAttachmentResult 返回“查询到零行”的结果（QueryRow 扫描为 pgx.ErrNoRows）。
func noAttachmentResult() pgResult { return pgResult{columns: attachmentRowColumns} }

// derivativeResult 返回一条衍生文件行（模拟 repo.GetDerivative）。
func derivativeResult(row repo.Derivative) pgResult {
	return pgResult{
		columns: derivativeRowColumns,
		rows: [][]any{{
			row.ID, row.AttachmentID, row.DerivativeType, row.Status, row.StorageKey,
			row.LastError, int(row.Attempts), row.CreatedAt, row.UpdatedAt,
		}},
	}
}

// noDerivativeResult 返回“衍生文件记录不存在”。
func noDerivativeResult() pgResult { return pgResult{columns: derivativeRowColumns} }

// idsResult 返回若干候选附件 ID（模拟清理候选查询）。
func idsResult(ids ...int64) pgResult {
	rows := make([][]any, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, []any{id})
	}
	return pgResult{columns: idColumn, rows: rows}
}

// usageResult 返回上传者已用存储空间（模拟 repo.Usage 的 attachment_usage 点查）。
func usageResult(total int64) pgResult {
	return pgResult{columns: usageColumn, rows: [][]any{{total}}}
}

// derivativeStatusesResult 返回衍生文件状态行（模拟 repo.StatusesByAttachmentIDs）。
func derivativeStatusesResult(entries map[int64]map[string]string) pgResult {
	rows := make([][]any, 0, len(entries))
	for attachmentID, statuses := range entries {
		for derivativeType, status := range statuses {
			rows = append(rows, []any{attachmentID, derivativeType, status})
		}
	}
	return pgResult{columns: derivativeStatusColumns, rows: rows}
}

// commandResult 返回一条命令完成标签（模拟 UPDATE/INSERT 的影响行数）。
func commandResult(tag string) pgResult { return pgResult{tag: tag} }

// dbErrorResult 返回数据库错误响应（模拟 repo 层报错）。
func dbErrorResult(message string) pgResult { return pgResult{err: message} }

// nullableInt64 把可空指针转成数据行值（nil 指针必须显式产生 NULL，而不是指向 nil 的接口）。
func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func attachmentRowValues(row repo.AttachmentRow) []any {
	return []any{
		row.ID, row.UploaderID, nullableInt64(row.PostID), row.Kind, row.Purpose,
		row.OriginalFilename, row.MediaType, row.SizeBytes, row.StorageKey,
		row.Status, nullableInt(row.Width), nullableInt(row.Height),
		nullableInt64(row.DurationMS), row.CreatedAt, row.UpdatedAt,
	}
}
