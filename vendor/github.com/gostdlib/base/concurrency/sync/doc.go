/*
Package sync is a replacement for the sync package in the Go standard library. This package provides
replacements for the sync.WaitGroup and sync.Pool types. These replacements provide additional
safety and functionality over the standard library versions.

The Group type is a replacement for the stdlib/sync.WaitGroup type. It provides additional safety and functionality
over the standard library version. It has some DNA from the golang.org/x/sync/errgroup package.
Using it as a replacement for WaitGroup is as follows:

	g.Group{}
	for i := 0; i < 100; i++ {
		g.Go( // Generally you can ignore the error, as this only happens if context is cancelled.
			ctx,
			func(ctx context.Context) error {
				// Do some work
				return nil
			},
		)
	}

	// err will be of the Errors type, which implements the error interface.
	// This will be nil if no errors were returned.
	// If it is not nil, it will contains IndexErr that wraps the error.
	// If you did not use the WithIndex() option, the IndexErr will have an index of -1.
	err := g.Wait(ctx)

You can also cause the Group to cancel all goroutines if any of them return an error by supplying the CancelOnErr
field. This context.Cancel() will be called the moment an error is encountered. All inflight goroutines
will be cancelled, but any goroutines that have already started will continue to run. Here is an example:

	ctx, cancel := context.WithCancel(context.Background())
	g := Group{CancelOnErr: cancel}
	for i := 0; i < 100; i++ {
		err := g.Go(
			ctx,
			func(ctx context.Context) error {
				if i == 3{
					return errors.New("error")
				}
				return nil
			},
		)
		// The returned err is simply used to allow us to stop the loop when we hit an error.
		// It does not contain any errors from the Group.
		if err != nil {
			break
		}
	}

	if err := g.Wait(ctx); err != nil {
		fmt.Println(err)
	}

You can also use an exponential backoff to retry failed function calls. This is done by setting the Backoff field.

Finally, you can also set the .Pool so this uses a worker.Pool type for concurrency control and reuse. However,
it is recommended to generally create a Group{} from one of the types in the worker package. This will
prepopulate the Group with a pool and if using a Limited pool, it will have the prescribed concurrency level.

And in most situations, you should get the Pool from our context.Context object. This will provide a default
worker.Pool.

This package also introduces the Pool type, which is a replacement for the stdlib/sync.Pool type. It provides
additional safety and functionality over the standard library version in that it uses generics to ensure
that you cannot put the wrong type into the pool. It also provides some advanced functionality for resetting
types that implement the Resetter interface. Finally it is tied in with Open Telemetry to provide metrics.

Using it as a replacement for Pool is as follows:

	p := sync.Pool[*bytes.Buffer]{
		New: func() *Bytes.Buffer {
			return bytes.NewBuffer(make([]byte, 0, 1024))
		},
	}
	b := p.Get(ctx)
	defer p.Put(b)

If you want to get all the benefits of Pool metrics, create the Pool with NewPool():

	p := sync.NewPool[*bytes.Buffer](
		ctx,
		name: "bufferPool",
		func() *Bytes.Buffer {
			return bytes.NewBuffer(make([]byte, 0, 1024))
		},
	)

There are also a few new synchronization primitives for reducing lock contention.

WProtect provides a type that allows you to protect a value via atomic.Pointer. This is useful for when you
have a value that is read frequently and written to infrequently. It is not a replacement for a mutex as you must
make a deep copy of the value to change it.

Example:

	wp := bsync.WProtect[Record, *Record]
	wp.Set(&Record{Value: 1}) // This takes a lock that only locks our writers, readers are never blocked.

The key thing here is that you must make a deep copy of the value to change it. This is because the value is
protected by an atomic.Pointer. This can be done via the GetModifySet() method.

This also provides a ShardedMap type. These are sharded maps that allow you to reduce lock contention
by sharding the map into multiple maps. This reduces lock contention when you have maps that are written to frequently.

Using the benchmarks I found for tidwall's sharded map, I modified the benchmarks for testing this:

	go version go1.23.4 darwin/arm64

	     number of cpus: 10
	     number of keys: 1000000
	            keysize: 10
	        random seed: 1737043406258270000

	-- sync.Map --
	set: 1,000,000 ops over 10 threads in 781ms, 1,280,559/sec, 780 ns/op
	get: 1,000,000 ops over 10 threads in 343ms, 2,911,610/sec, 343 ns/op
	rng:       100 ops over 10 threads in 591ms, 169/sec, 5913101 ns/op
	del: 1,000,000 ops over 10 threads in 387ms, 2,584,923/sec, 386 ns/op

	-- stdlib map --
	set: 1,000,000 ops over 10 threads in 511ms, 1,956,468/sec, 511 ns/op
	get: 1,000,000 ops over 10 threads in 146ms, 6,828,640/sec, 146 ns/op
	rng:       100 ops over 10 threads in 127ms, 787/sec, 1269165 ns/op
	del: 1,000,000 ops over 10 threads in 351ms, 2,848,299/sec, 351 ns/op

	-- github.com/orcaman/concurrent-map --
	set: 1,000,000 ops over 10 threads in 134ms, 7,477,008/sec, 133 ns/op
	get: 1,000,000 ops over 10 threads in 30ms, 33,587,102/sec, 29 ns/op
	rng:       100 ops over 10 threads in 2228ms, 44/sec, 22282607 ns/op
	del: 1,000,000 ops over 10 threads in 76ms, 13,135,001/sec, 76 ns/op

	-- github.com/tidwall/shardmap --
	set: 1,000,000 ops over 10 threads in 61ms, 16,479,736/sec, 60 ns/op
	get: 1,000,000 ops over 10 threads in 29ms, 34,268,482/sec, 29 ns/op
	rng:       100 ops over 10 threads in 139ms, 718/sec, 1392182 ns/op
	del: 1,000,000 ops over 10 threads in 48ms, 20,699,879/sec, 48 ns/op

	-- sync.ShardedMap -- [ours]
	set: 1,000,000 ops over 10 threads in 199ms, 5,027,980/sec, 198 ns/op
	get: 1,000,000 ops over 10 threads in 66ms, 15,164,247/sec, 65 ns/op
	del: 1,000,000 ops over 10 threads in 154ms, 6,475,097/sec, 154 ns/op

	This has ours as an improvement over the stdlib map with locks and the sync.Map one. It is not
	as fast as tidwall's sharded map, but it is generic and can use non-string keys. We may improve this in
	the future by using a faster hashmap implementation, which is what tidwall's sharded map uses.
	However, we have a new map implemtation coming to the stdlib in 1.24, so we are going to wait to
	see how that performs.

	In case anyone is wondering, if the sharded versions use a sync.Pool for the maphash, this adds significant
	overhead.

	maphash currently beats fnv 32 by significant margins.

	The key generation that is done with fmt.Sprintf is as fast as unsafe methods I tried.
*/
package sync
