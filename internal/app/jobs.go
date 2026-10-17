package app

import (
	"time"

	agentapp "github.com/great-magician-01/timeline/internal/modules/agent/app"
	contentapp "github.com/great-magician-01/timeline/internal/modules/content/app"
	mediaapp "github.com/great-magician-01/timeline/internal/modules/media/app"
	platformjobs "github.com/great-magician-01/timeline/internal/platform/jobs"
)

// registerJobs 把各模块作业处理器与周期任务接入运行器。
func registerJobs(runner *platformjobs.Runner, contentApp *contentapp.Service,
	mediaApp *mediaapp.Service, agentApp *agentapp.Service) {
	if runner == nil {
		return
	}
	// 作业处理器。
	runner.Register(platformjobs.TypePostPublish, contentApp.HandlePublishJob)
	runner.Register(platformjobs.TypeMediaDerivative, mediaApp.HandleDerivativeJob)
	runner.Register(platformjobs.TypeAITask, agentApp.HandleAITaskJob)
	runner.Register(platformjobs.TypeMemoryConsolidate, agentApp.HandleMemoryJob)

	// 周期任务：AI 调度、附件失效清理、调用记录保留期清理。
	runner.RegisterPeriodic("ai_schedule", time.Minute, agentApp.RunScheduler)
	runner.RegisterPeriodic("attachment_cleanup", 6*time.Hour, mediaApp.RunCleanup)
	runner.RegisterPeriodic("ai_call_log_cleanup", 24*time.Hour, agentApp.RunCallLogCleanup)
}
