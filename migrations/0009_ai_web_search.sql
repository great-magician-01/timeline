-- 0009_ai_web_search：AI 行为策略可单独允许调用全局网页搜索工具
-- （docs/REQUIREMENTS.md 13.3）。搜索密钥独立于模型接口预设，由管理后台
-- 系统设置统一维护；该布尔字段只控制模型工具权限，不是社区动作。

ALTER TABLE timeline.ai_behavior_settings
    ADD COLUMN allow_web_search BOOLEAN NOT NULL DEFAULT FALSE;
