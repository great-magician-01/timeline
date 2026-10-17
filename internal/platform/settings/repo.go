package settings

// 本文件集中存放 system_settings 的 SQL。写入时间（updated_at）由应用时钟计算后
// 作为参数传入，SQL 内禁止 now()/CURRENT_DATE/interval 等数据库时间函数。

// sqlSelectSettings 批量读取设置键；只读查询，与时间无关。
const sqlSelectSettings = `SELECT key, value FROM system_settings WHERE key = ANY($1)`

// sqlUpsertSetting 写入或更新一个设置值；$4 是应用时间（updated_at）。
const sqlUpsertSetting = `
	INSERT INTO system_settings (key, value, updated_by, updated_at)
	VALUES ($1, $2::jsonb, $3, $4)
	ON CONFLICT (key) DO UPDATE
	SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = EXCLUDED.updated_at`
