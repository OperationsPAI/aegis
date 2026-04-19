package router

import (
	auth "aegis/module/auth"
	container "aegis/module/container"
	dataset "aegis/module/dataset"
	evaluation "aegis/module/evaluation"
	execution "aegis/module/execution"
	group "aegis/module/group"
	injection "aegis/module/injection"
	label "aegis/module/label"
	metric "aegis/module/metric"
<<<<<<< HEAD
=======
	notification "aegis/module/notification"
>>>>>>> 309b299 (phase-4: migrate pedestal module for #38)
	project "aegis/module/project"
	ratelimiter "aegis/module/ratelimiter"
	sdk "aegis/module/sdk"
	system "aegis/module/system"
	task "aegis/module/task"
)

type Handlers struct {
<<<<<<< HEAD
	Auth        *auth.Handler
	Project     *project.Handler
	Task        *task.Handler
	Injection   *injection.Handler
	Execution   *execution.Handler
	Container   *container.Handler
	Dataset     *dataset.Handler
	Evaluation  *evaluation.Handler
	Group       *group.Handler
	Metric      *metric.Handler
	SDK         *sdk.Handler
	System      *system.Handler
	RateLimiter *ratelimiter.Handler
	Label       *label.Handler
=======
	Auth         *auth.Handler
	Project      *project.Handler
	Task         *task.Handler
	Injection    *injection.Handler
	Execution    *execution.Handler
	Container    *container.Handler
	Dataset      *dataset.Handler
	Evaluation   *evaluation.Handler
	Trace        *trace.Handler
	Group        *group.Handler
	Metric       *metric.Handler
	User         *user.Handler
	SDK          *sdk.Handler
	System       *system.Handler
	Notification *notification.Handler
	RateLimiter  *ratelimiter.Handler
	ChaosSystem  *chaossystem.Handler
	Team         *team.Handler
	Label        *label.Handler
	SystemMetric *systemmetric.Handler
>>>>>>> 309b299 (phase-4: migrate pedestal module for #38)
}

func NewHandlers(
	auth *auth.Handler,
	project *project.Handler,
	task *task.Handler,
	injection *injection.Handler,
	execution *execution.Handler,
	container *container.Handler,
	dataset *dataset.Handler,
	evaluation *evaluation.Handler,
	group *group.Handler,
	metric *metric.Handler,
	sdk *sdk.Handler,
	system *system.Handler,
<<<<<<< HEAD
=======
	notification *notification.Handler,
>>>>>>> 309b299 (phase-4: migrate pedestal module for #38)
	rateLimiter *ratelimiter.Handler,
	label *label.Handler,
) *Handlers {
	return &Handlers{
<<<<<<< HEAD
		Auth:        auth,
		Project:     project,
		Task:        task,
		Injection:   injection,
		Execution:   execution,
		Container:   container,
		Dataset:     dataset,
		Evaluation:  evaluation,
		Group:       group,
		Metric:      metric,
		SDK:         sdk,
		System:      system,
		RateLimiter: rateLimiter,
		Label:       label,
=======
		Auth:         auth,
		Project:      project,
		Task:         task,
		Injection:    injection,
		Execution:    execution,
		Container:    container,
		Dataset:      dataset,
		Evaluation:   evaluation,
		Trace:        trace,
		Group:        group,
		Metric:       metric,
		User:         user,
		SDK:          sdk,
		System:       system,
		Notification: notification,
		RateLimiter:  rateLimiter,
		ChaosSystem:  chaosSystem,
		Team:         team,
		Label:        label,
		SystemMetric: systemMetric,
>>>>>>> 309b299 (phase-4: migrate pedestal module for #38)
	}
}
