package background

import (
	"go.opentelemetry.io/otel/metric"
)

// tasksMetrics is a struct that contains metrics for background tasks.
type tasksMetrics struct {
	// BackgroundTasksRunning is a counter that tracks the number of background tasks currently running.
	BackgroundTasksRunning metric.Int64UpDownCounter
	// OnceTasksRunning is a counter that tracks the number of once tasks currently running.
	OnceTasksRunning metric.Int64UpDownCounter
	// BackgroundTasksTotal is a counter that tracks the total number of background tasks run.
	BackgroundTasksTotal metric.Int64Counter
	// OnceTasksTotal is a counter that tracks the total number of once tasks run.
	OnceTasksTotal metric.Int64Counter

	// BackgroundTasks is a map that contains metrics for tasks run via Run().
	BackgroundTasks map[string]*backgroundTaskMetrics
	// OnceTasks is a map that contains metrics for tasks run via Once().
	OnceTasks map[string]*onceTaskMetrics
}

func newTaskMetrics(m metric.Meter) *tasksMetrics {
	btr, err := m.Int64UpDownCounter(
		"background_tasks_running",
		metric.WithDescription("The number of background tasks currently running."),
	)
	if err != nil {
		panic(err)
	}
	otr, err := m.Int64UpDownCounter(
		"once_tasks_running",
		metric.WithDescription("The number of once tasks currently running."),
	)
	if err != nil {
		panic(err)
	}
	btt, err := m.Int64Counter(
		"background_tasks_total",
		metric.WithDescription("The total number of background tasks run."),
	)
	if err != nil {
		panic(err)
	}
	ott, err := m.Int64Counter(
		"once_tasks_total",
		metric.WithDescription("The total number of once tasks run."),
	)
	if err != nil {
		panic(err)
	}

	return &tasksMetrics{
		BackgroundTasksRunning: btr,
		OnceTasksRunning:       otr,
		BackgroundTasksTotal:   btt,
		OnceTasksTotal:         ott,
		BackgroundTasks:        make(map[string]*backgroundTaskMetrics),
		OnceTasks:              make(map[string]*onceTaskMetrics),
	}
}

// backgroundTaskMetrics is a struct that contains metrics for tasks run via Run().
type backgroundTaskMetrics struct {
	// IsRunning indicates if the task is currently running.
	Restarts metric.Int64Counter
}

func newBackgroundTaskMetrics(m metric.Meter) *backgroundTaskMetrics {
	r, err := m.Int64Counter("restarts", metric.WithDescription("The number of times the task has been restarted."))
	if err != nil {
		panic(err)
	}

	return &backgroundTaskMetrics{
		Restarts: r,
	}
}

// onceTaskMetrics is a struct that contains metrics for tasks run via Once().
type onceTaskMetrics struct {
	// ExecutedTotal is a counter that tracks the total number of times the task has been executed.
	ExecutedTotal metric.Int64Counter
	// ErrorsTotal is a counter that tracks the total number of errors encountered while executing the task.
	ErrorsTotal metric.Int64Counter
}

func newOnceTaskMetrics(m metric.Meter) *onceTaskMetrics {
	et, err := m.Int64Counter(
		"executed_total",
		metric.WithDescription("The total number of times the task has been executed."),
	)
	if err != nil {
		panic(err)
	}
	errt, err := m.Int64Counter(
		"errors_total",
		metric.WithDescription("The total number of errors encountered while executing the task."),
	)
	if err != nil {
		panic(err)
	}

	return &onceTaskMetrics{
		ExecutedTotal: et,
		ErrorsTotal:   errt,
	}
}
