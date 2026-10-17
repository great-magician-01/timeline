package audit

// sqlInsertAuditLog 写入一条审计记录。created_at 使用 $7（Entry.At，应用时间），
// 不依赖数据库默认值 now()。
const sqlInsertAuditLog = `
	INSERT INTO admin_audit_logs (
		id, admin_id, action_type, target_type, target_id, detail, created_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7)`
