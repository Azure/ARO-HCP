// Package background provides a way to run background tasks that run outside of a
// request/response cycle. This allows tracking of tasks that are running and
// the ability to cancel them.
package background

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/concurrency/worker"
	internalCtx "github.com/gostdlib/base/internal/context"
	"github.com/gostdlib/base/telemetry/log"
	"github.com/gostdlib/base/telemetry/otel/metrics"
	"go.opentelemetry.io/otel/metric"

	"github.com/Azure/retry/exponential"
)

const (
	backgroundKey = "backgroundTask"
	logNameKey    = "name"
	logEventKey   = "event"
	logErrorKey   = "error"
)

// Task is a function that runs in the background.
type Task func(context.Context) error

// Tasks tracks background tasks that are not in the path of a request/response cycle. Task errors are
// logged, but do not appear in OTEL traces. However this does provide metrics on the tasks.
type Tasks struct {
	pool *worker.Pool
	done bool

	mu      sync.Mutex
	cancels []context.CancelFunc

	meter metric.Meter
	tm    *tasksMetrics
}

// New creates a new Tasks object. An application should only create one Tasks object. This is usually
// retrieved via context.Tasks().
func New(ctx context.Context) *Tasks {
	p := worker.Default().Sub(ctx, "background tasks")

	mp := internalCtx.MeterProvider(ctx)
	meter := mp.Meter(metrics.MeterName(2))
	tm := newTaskMetrics(meter)
	return &Tasks{
		pool:  p,
		tm:    tm,
		meter: meter,
	}
}

type runOpts struct{}

// RunOption is an optional argument for Run().
type RunOption func(runOpts) (runOpts, error)

// Run submits a task to run in the background. The name is used to identify the task and
// to gather metrics on it. This name is appended to the name of the package + function Run() is called from,
// meaning the name has to be unique within that function. The Task is the function to run. If
// task() ends, it will use the Backoff provided to restart the Task. If Context is canceled, this will stop
// launching the task, however, the task itself has to honor the context passed to it in order for it to be stopped.
// An error is only returned if an option fails.
// Do not try to use this for a cron like task. If you need to run background cron like tasks,
// use the .Once() method wrapped with some timer.
func (t *Tasks) Run(ctx context.Context, name string, task Task, boff *exponential.Backoff, options ...RunOption) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.done {
		return fmt.Errorf("background/Tasks.Run: tasks already closed")
	}

	if name == "" {
		return fmt.Errorf("background/Tasks.Run: name cannot be empty")
	}
	if task == nil {
		return fmt.Errorf("background/Tasks.Run: task cannot be nil")
	}
	if boff == nil {
		return fmt.Errorf("background/Tasks.Run: backoff cannot be nil")
	}

	opts := runOpts{}
	for _, o := range options {
		var err error
		opts, err = o(opts)
		if err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	t.cancels = append(t.cancels, cancel)

	name = fmt.Sprintf("%s.%s", metrics.MeterName(2), name)

	var bm *backgroundTaskMetrics
	var ok bool
	bm, ok = t.tm.BackgroundTasks[name]
	if !ok {
		bm = newBackgroundTaskMetrics(t.meter)
		t.tm.BackgroundTasks[name] = bm
	}

	// Restarts the task if it ends.
	t.pool.Submit(
		ctx,
		func() {
			boff.Retry(
				ctx,
				t.taskWrapper(name, bm, task),
			)
		},
	)

	return nil
}

func (t *Tasks) taskWrapper(name string, bm *backgroundTaskMetrics, task Task) exponential.Op {
	return func(ctx context.Context, rec exponential.Record) (err error) {
		defer func() {
			if ctx.Err() == nil {
				bm.Restarts.Add(ctx, 1)
			}
			if err != nil {
				log.Default().LogAttrs(
					ctx,
					slog.LevelError,
					err.Error(),
					slog.String(backgroundKey, "task"),
					slog.String(logNameKey, name),
				)
			}
		}()

		log.Default().LogAttrs(
			ctx,
			slog.LevelInfo,
			"start background task",
			slog.String(backgroundKey, "task"),
			slog.String(logNameKey, name),
			slog.String(logEventKey, "start"),
		)
		defer log.Default().LogAttrs(
			ctx,
			slog.LevelInfo,
			"end background task",
			slog.String(backgroundKey, "task"),
			slog.String(logNameKey, name),
			slog.String(logEventKey, "end"),
		)

		t.tm.BackgroundTasksRunning.Add(ctx, 1)
		defer t.tm.BackgroundTasksRunning.Add(ctx, -1)
		t.tm.BackgroundTasksTotal.Add(ctx, 1)

		err = task(ctx)
		return err
	}
}

// Once is like Run, but it only runs the function once. If the function ends, it will not
// be restarted. name can be reused if you want to keep stats on a collection of one shot tasks.
func (t *Tasks) Once(ctx context.Context, name string, task Task, options ...RunOption) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.done {
		return fmt.Errorf("background/Tasks.Once: tasks already closed")
	}

	opts := runOpts{}
	for _, o := range options {
		var err error
		opts, err = o(opts)
		if err != nil {
			return err
		}
	}

	name = fmt.Sprintf("%s.%s", metrics.MeterName(2), name)

	var otm *onceTaskMetrics
	if _, ok := t.tm.OnceTasks[name]; ok {
		otm = t.tm.OnceTasks[name]
	} else {
		otm = newOnceTaskMetrics(t.meter)
		t.tm.OnceTasks[name] = otm
	}

	t.pool.Submit(
		ctx,
		func() {
			otm.ExecutedTotal.Add(ctx, 1)
			t.tm.OnceTasksTotal.Add(ctx, 1)
			t.tm.OnceTasksRunning.Add(ctx, 1)
			defer t.tm.OnceTasksRunning.Add(ctx, -1)
			var err error
			defer func() {
				if err != nil {
					otm.ErrorsTotal.Add(ctx, 1)
					attrs := []slog.Attr{
						{Key: backgroundKey, Value: slog.StringValue("once")},
						{Key: logNameKey, Value: slog.StringValue(name)},
					}
					log.Default().LogAttrs(ctx, slog.LevelError, err.Error(), attrs...)
				}
			}()
			err = task(ctx)
		},
	)
	return nil
}

// Close stops the running of all tasks and waits for them to finish. This will only stop
// new tasks from starting or restarting. It will not stop a task that is currently running.
// If you want to stop a task that is currently running, you will need to use a context
// passed to the invoked function with support in that function. The context passed to Close()
// is only used to wait for the tasks to finish. If that context has a deadline, this will wait
// for everything to finish or for that deadline. If not, it will set a deadline of 30 seconds.
// If this doesn't finish by then, it will return an error.
func (t *Tasks) Close(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.done = true
	for _, cancel := range t.cancels {
		cancel()
	}
	return t.pool.Close(ctx)
}

// Meter returns the OpenTelemetry meter for the Tasks.
func (t *Tasks) Meter() metric.Meter {
	return t.meter
}
