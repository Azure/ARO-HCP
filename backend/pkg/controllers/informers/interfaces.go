package informers

import (
	"context"
	"time"

	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/internal/database"
)

type ListerWatcher interface {
	List(ctx context.Context, opts *database.DBClientListResourceDocsOptions) (database.DBClientIterator[any], error)
}

type SharedInformer interface {
	// AddEventHandler adds an event handler to the shared informer using
	// the shared informer's resync period.  Events to a single handler are
	// delivered sequentially, but there is no coordination between
	// different handlers.
	// It returns a registration handle for the handler that can be used to
	// remove the handler again, or to tell if the handler is synced (has
	// seen every item in the initial list).
	//
	// Contextual logging: AddEventHandlerWithOptions together with a logger in the options should be used instead of AddEventHandler in code which supports contextual logging.
	AddEventHandler(handler cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error)
	// AddEventHandlerWithResyncPeriod adds an event handler to the
	// shared informer with the requested resync period; zero means
	// this handler does not care about resyncs.  The resync operation
	// consists of delivering to the handler an update notification
	// for every object in the informer's local cache; it does not add
	// any interactions with the authoritative storage.  Some
	// informers do no resyncs at all, not even for handlers added
	// with a non-zero resyncPeriod.  For an informer that does
	// resyncs, and for each handler that requests resyncs, that
	// informer develops a nominal resync period that is no shorter
	// than the requested period but may be longer.  The actual time
	// between any two resyncs may be longer than the nominal period
	// because the implementation takes time to do work and there may
	// be competing load and scheduling noise.
	// It returns a registration handle for the handler that can be used to remove
	// the handler again and an error if the handler cannot be added.
	//
	// Contextual logging: AddEventHandlerWithOptions together with a logger in the options should be used instead of AddEventHandlerWithResyncPeriod in code which supports contextual logging.
	AddEventHandlerWithResyncPeriod(handler cache.ResourceEventHandler, resyncPeriod time.Duration) (cache.ResourceEventHandlerRegistration, error)
	// AddEventHandlerWithOptions is a variant of AddEventHandlerWithResyncPeriod where
	// all optional parameters are passed in a struct.
	AddEventHandlerWithOptions(handler cache.ResourceEventHandler, options cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error)

	Run(ctx context.Context, threadiness int)

	GetIndexer() cache.Indexer
	// HasSynced delegates to the Config's Queue
	HasSynced() bool
}

func NewSharedInformer(
	lw ListerWatcher,
	resyncPeriod time.Duration,
	relistPeriod time.Duration,
	handler cache.ResourceEventHandlerDetailedFuncs,
	indexers cache.Indexers,
) SharedInformer {
	// This will hold the cache, as we know it.
	// the indexedStore is updated *first* in the sharedIndexInformer.HandleDeltas function
	indexedStore := cache.NewIndexer(DeletionHandlingResourceIDKeyFunc, indexers)
	queue := cache.NewRealFIFO(DeletionHandlingResourceIDKeyFunc, indexedStore, nil)
	processor := &sharedProcessor{clock: &clock.RealClock{}}

	ret := &sharedIndexInformer{
		queue:                           queue,
		listerWatcher:                   lw,
		indexedStore:                    indexedStore,
		relistPeriod:                    relistPeriod,
		processor:                       processor,
		defaultEventHandlerResyncPeriod: 24 * time.Hour, // this way we'll always get complaints about slowness and eventually recover
		resyncCheckPeriod:               resyncPeriod,
		clock:                           &clock.RealClock{},
	}
	ret.queueItemPopFn = ret.HandleDeltas

	return ret
}

// Multiplexes updates in the form of a list of Deltas into a Store, and informs
// a given handler of events OnUpdate, OnAdd, OnDelete
func processDeltas(
	// Object which receives event notifications from the given deltas
	handler cache.ResourceEventHandler,
	clientState cache.Store,
	deltas cache.Deltas,
	isInInitialList bool,
) error {
	// from oldest to newest
	for _, d := range deltas {
		obj := d.Object

		switch d.Type {
		case cache.Sync, cache.Replaced, cache.Added, cache.Updated:
			if old, exists, err := clientState.Get(obj); err == nil && exists {
				if err := clientState.Update(obj); err != nil {
					return err
				}
				handler.OnUpdate(old, obj)
			} else {
				if err := clientState.Add(obj); err != nil {
					return err
				}
				handler.OnAdd(obj, isInInitialList)
			}
		case cache.Deleted:
			if err := clientState.Delete(obj); err != nil {
				return err
			}
			handler.OnDelete(obj)
		}
	}
	return nil
}
