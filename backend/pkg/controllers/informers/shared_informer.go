package informers

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// contents heavily modifed from from from k8s.io/client-go/cache

// `*sharedIndexInformer` implements Controller
type sharedIndexInformer struct {
	queue cache.Queue

	// queueItemPopFn can process a popped Deltas.
	queueItemPopFn cache.PopProcessFunc

	listerWatcher ListerWatcher

	indexedStore cache.Indexer
	processor    *sharedProcessor

	defaultEventHandlerResyncPeriod time.Duration
	resyncCheckPeriod               time.Duration
	relistPeriod                    time.Duration

	started, stopped bool
	startedLock      sync.Mutex

	clock clock.Clock

	// blockDeltas gives a way to stop all event distribution so that a late event handler
	// can safely join the shared informer.
	blockDeltas sync.Mutex
}

func (s *sharedIndexInformer) GetIndexer() cache.Indexer {
	return s.indexedStore
}

// RunWithContext implements [Controller.RunWithContext].
func (s *sharedIndexInformer) Run(ctx context.Context, _ int) {
	defer utilruntime.HandleCrashWithContext(ctx)
	go func() {
		<-ctx.Done()
		s.queue.Close()
	}()

	func() {
		s.startedLock.Lock()
		defer s.startedLock.Unlock()

		wait.UntilWithContext(ctx, s.resync, s.resyncCheckPeriod)
		wait.UntilWithContext(ctx, s.relist, s.relistPeriod)
		wait.UntilWithContext(ctx, s.processLoop, time.Second)

		s.started = true
	}()

	processorStopCtx, stopProcessor := context.WithCancelCause(context.WithoutCancel(ctx))
	var wg wait.Group
	defer wg.Wait()                                         // Wait for Processor to stop
	defer stopProcessor(errors.New("informer is stopping")) // Tell Processor to stop
	wg.StartWithContext(processorStopCtx, s.processor.run)

	<-ctx.Done()
}

// Returns true once this sharedIndexInformer has completed an initial resource listing
func (s *sharedIndexInformer) HasSynced() bool {
	return s.queue.HasSynced()
}

// processLoop drains the work queue.
// TODO: Consider doing the processing in parallel. This will require a little thought
// to make sure that we don't end up processing the same object multiple times
// concurrently.
func (s *sharedIndexInformer) processLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// otherwise fallback to non-batch process behavior
			_, err := s.queue.Pop(s.queueItemPopFn)
			if err != nil {
				if errors.Is(err, cache.ErrFIFOClosed) {
					return
				}
			}
		}
	}
}

func (s *sharedIndexInformer) HandleDeltas(obj interface{}, isInInitialList bool) error {
	s.blockDeltas.Lock()
	defer s.blockDeltas.Unlock()

	if deltas, ok := obj.(cache.Deltas); ok {
		return processDeltas(s, s.indexedStore, deltas, isInInitialList)
	}
	return errors.New("object given as Process argument is not Deltas")
}

func (s *sharedIndexInformer) resync(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)
	if !s.processor.shouldResync() {
		logger.Info("skipping resync")
		return
	}
	logger.Info("resyncing")

	err := s.queue.Resync()
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("failed to resync: %w", err))
	}
}

func (s *sharedIndexInformer) relist(ctx context.Context) {
	err := s.relistWithError(ctx)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("failed to relist: %w", err))
	}
}

func (s *sharedIndexInformer) relistWithError(ctx context.Context) error {
	newObjects := []any{}
	newObjectsIterator, err := s.listerWatcher.List(ctx, nil)
	if err != nil {
		return utils.TrackError(err)
	}
	for _, subscription := range newObjectsIterator.Items(ctx) {
		newObjects = append(newObjects, subscription)
	}
	if err := newObjectsIterator.GetError(); err != nil {
		return utils.TrackError(err)
	}

	return s.queue.Replace(newObjects, "")
}

// Conforms to ResourceEventHandler
func (s *sharedIndexInformer) OnAdd(obj interface{}, isInInitialList bool) {
	// Invocation of this function is locked under s.blockDeltas, so it is
	// safe to distribute the notification
	s.processor.distribute(addNotification{newObj: obj, isInInitialList: isInInitialList}, false)
}

// Conforms to ResourceEventHandler
func (s *sharedIndexInformer) OnUpdate(old, new interface{}) {
	isSync := false

	// TODO decide if we determine the sync based on etag

	// safe to distribute the notification
	s.processor.distribute(updateNotification{oldObj: old, newObj: new}, isSync)
}

// Conforms to ResourceEventHandler
func (s *sharedIndexInformer) OnDelete(old interface{}) {
	// Invocation of this function is locked under s.blockDeltas, so it is
	// safe to distribute the notification
	s.processor.distribute(deleteNotification{oldObj: old}, false)
}

func (s *sharedIndexInformer) RemoveEventHandler(handle cache.ResourceEventHandlerRegistration) error {
	s.startedLock.Lock()
	defer s.startedLock.Unlock()

	// in order to safely remove, we have to
	// 1. stop sending add/update/delete notifications
	// 2. remove and stop listener
	// 3. unblock
	s.blockDeltas.Lock()
	defer s.blockDeltas.Unlock()
	return s.processor.removeListener(handle)
}

func (s *sharedIndexInformer) AddEventHandler(handler cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
	return s.AddEventHandlerWithOptions(handler, cache.HandlerOptions{})
}

func (s *sharedIndexInformer) AddEventHandlerWithResyncPeriod(handler cache.ResourceEventHandler, resyncPeriod time.Duration) (cache.ResourceEventHandlerRegistration, error) {
	return s.AddEventHandlerWithOptions(handler, cache.HandlerOptions{ResyncPeriod: &resyncPeriod})
}

func (s *sharedIndexInformer) AddEventHandlerWithOptions(handler cache.ResourceEventHandler, options cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	s.startedLock.Lock()
	defer s.startedLock.Unlock()

	if s.stopped {
		return nil, fmt.Errorf("handler %v was not added to shared informer because it has stopped already", handler)
	}

	logger := ptr.Deref(options.Logger, klog.Background())
	resyncPeriod := ptr.Deref(options.ResyncPeriod, s.defaultEventHandlerResyncPeriod)
	if resyncPeriod > 0 {
		if resyncPeriod < minimumResyncPeriod {
			logger.Info("Warning: resync period is too small. Changing it to the minimum allowed value", "resyncPeriod", resyncPeriod, "minimumResyncPeriod", minimumResyncPeriod)
			resyncPeriod = minimumResyncPeriod
		}

		if resyncPeriod < s.resyncCheckPeriod {
			if s.started {
				logger.Info("Warning: resync period is smaller than resync check period and the informer has already started. Changing it to the resync check period", "resyncPeriod", resyncPeriod, "resyncCheckPeriod", s.resyncCheckPeriod)

				resyncPeriod = s.resyncCheckPeriod
			} else {
				// if the event handler's resyncPeriod is smaller than the current resyncCheckPeriod, update
				// resyncCheckPeriod to match resyncPeriod and adjust the resync periods of all the listeners
				// accordingly
				s.resyncCheckPeriod = resyncPeriod
				s.processor.resyncCheckPeriodChanged(logger, resyncPeriod)
			}
		}
	}

	listener := newProcessListener(logger, handler, resyncPeriod, determineResyncPeriod(logger, resyncPeriod, s.resyncCheckPeriod), s.clock.Now(), initialBufferSize, s.HasSynced)

	if !s.started {
		return s.processor.addListener(listener), nil
	}

	// in order to safely join, we have to
	// 1. stop sending add/update/delete notifications
	// 2. do a list against the store
	// 3. send synthetic "Add" events to the new handler
	// 4. unblock
	s.blockDeltas.Lock()
	defer s.blockDeltas.Unlock()

	handle := s.processor.addListener(listener)
	for _, item := range s.indexedStore.List() {
		// Note that we enqueue these notifications with the lock held
		// and before returning the handle. That means there is never a
		// chance for anyone to call the handle's HasSynced method in a
		// state when it would falsely return true (i.e., when the
		// shared informer is synced but it has not observed an Add
		// with isInitialList being true, nor when the thread
		// processing notifications somehow goes faster than this
		// thread adding them and the counter is temporarily zero).
		listener.add(addNotification{newObj: item, isInInitialList: true})
	}
	return handle, nil
}

const (
	initialBufferSize   = 1024
	minimumResyncPeriod = 1 * time.Second
)
