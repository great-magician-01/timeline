-- 0008_ai_task_schedule：AI 手动任务支持定时发布（docs/REQUIREMENTS.md 13.1）。
-- 管理员在 /admin/ai/:id/generate 与 /admin/ai/:id/publish-direct 上可以传入 scheduled_at，
-- 任务执行时把该时间传给内容服务，由内容侧创建 status='scheduled' 的帖子并投递发布作业。

ALTER TABLE timeline.ai_tasks ADD COLUMN scheduled_at TIMESTAMPTZ;
