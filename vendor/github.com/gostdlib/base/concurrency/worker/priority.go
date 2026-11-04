package worker

import (
	"context"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gostdlib/base/concurrency/worker/internal/heap"
)

// QJob represents a job to be done in a priority queue.
type QJob struct {
	// Priority is the job's priority.
	Priority uint64
	// Work is the work to be done by the job.
	Work func()

	// submit is the submit the job was submitted.
	submit time.Time
}

// queue implements the heap interface. We are using a custom generic heap instead of the stdlib.
type queue struct {
	jobs []QJob
}

func (p *queue) Len() int {
	return len(p.jobs)
}

func (p *queue) Less(i, j int) bool {
	// Make submission time a tiebreaker.
	if p.jobs[i].Priority == p.jobs[j].Priority {
		return p.jobs[i].submit.Before(p.jobs[j].submit)
	}

	return p.jobs[i].Priority > p.jobs[j].Priority
}

func (p *queue) Swap(i, j int) {
	defer func() {
		if a := recover(); a != nil {
			log.Printf("i %d, j %d", i, j)
			panic(a)
		}
	}()

	p.jobs[i], p.jobs[j] = p.jobs[j], p.jobs[i]
}

func (p *queue) Push(ctx context.Context, x QJob) {
	p.jobs = append(p.jobs, x)
}

func (p *queue) Pop(ctx context.Context) QJob {
	l := len(p.jobs)
	if l == 0 {
		panic("bug: trying to Pop off an empty queue, which is a serious flaw in this package")
	}
	if l == 1 {
		job := p.jobs[0]
		p.jobs = nil
		return job
	}

	n := len(p.jobs) - 1
	job := p.jobs[n]
	p.jobs = p.jobs[0:n]
	return job
}

// Queue represents a priority queue for jobs. This can be created from a Limited Pool via Queue().
// If two jobs have the same priority, the job that was submitted first will be processed first.
type Queue struct {
	done        chan struct{}
	running     atomic.Int64
	queueLen    atomic.Int64
	queueWait   sync.WaitGroup
	processWait sync.WaitGroup
	size        chan struct{}
	mu          sync.Mutex
	next        chan QJob
	pool        *Pool

	queue *queue
}

// newQueue returns a new priority queue. This is called from Limited.PriorityQueue().
func newQueue(maxSize int, p *Pool) *Queue {
	if maxSize < 1 {
		panic("maxSize must be greater than 0")
	}
	d := &Queue{
		queue: &queue{},
		done:  make(chan struct{}),
		size:  make(chan struct{}, maxSize),
		next:  make(chan QJob, 1),
		pool:  p,
	}

	Default().Submit(
		context.Background(),
		d.doWork,
	)
	return d
}

// Close closes the queue. Be sure that the queue is empty before closing.
func (d *Queue) Close() {
	close(d.done)
}

func (d *Queue) Running() int {
	return int(d.running.Load())
}

// QueueLen returns the size of the queue not processed. This does not include QJobs that are
// currently being processed.
func (d *Queue) QueueLen() int {
	return int(d.queueLen.Load()) + len(d.next)
}

// Wait waits for the queue to empty and processing to be completed. This will return an
// error if the context is canceled and will stop waiting. Otherwise the error will be nil.
func (d *Queue) Wait(ctx context.Context) error {
	done := make(chan struct{})

	Default().Submit(
		ctx,
		func() {
			d.queueWait.Wait()
			d.processWait.Wait()
			close(done)
		},
	)

	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-done:
	}
	return nil
}

// Submit will submit a job to the queue. If the queue is full, it will block until there is room
// in the queue or the context is canceled. A job with priority 0 will be assigned a default priority of 100.
// Valid priority values are 1 - uint64Max. Higher priority jobs (highest being uint64Max) will be
// processed first.
func (d *Queue) Submit(ctx context.Context, job QJob) error {
	if job.Work == nil {
		return errors.New("job has no work")
	}
	if job.Priority == 0 {
		job.Priority = 100
	}
	job.submit = time.Now()

	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case d.size <- struct{}{}:
	}

	d.queueLen.Add(1)
	d.processWait.Add(1)

	d.mu.Lock()
	heap.Push(ctx, d.queue, job)
	if len(d.next) == 0 && d.queue.Len() != 0 {
		d.next <- heap.Pop(ctx, d.queue)
	}
	d.mu.Unlock()

	return nil
}

// doWork simply sends our QJobs to be done by the worker pool.
func (d *Queue) doWork() {
	ctx := context.Background()

	for {
		var job QJob
		select {
		case <-d.done:
			return
		case job = <-d.next:
			<-d.size
			d.mu.Lock()
			if len(d.next) == 0 && d.queue.Len() != 0 {
				d.next <- heap.Pop(ctx, d.queue)
			}
			d.mu.Unlock()
		}

		d.queueLen.Add(-1)

		if job.Work == nil {
			panic("Bug: job has no work")
		}

		f := func() {
			d.running.Add(1)
			job.Work()
			d.running.Add(-1)
			d.processWait.Done()
		}

		d.pool.Submit(context.Background(), f)
	}
}
