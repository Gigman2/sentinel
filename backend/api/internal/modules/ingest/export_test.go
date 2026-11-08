package ingest

// SetAfterEnqueue installs a hook that runs inside the ingest transaction
// after analysis jobs are enqueued, and returns a function restoring the
// previous hook. Tests that use it must not run in parallel.
func SetAfterEnqueue(f func() error) (restore func()) {
	prev := afterEnqueue
	afterEnqueue = f
	return func() { afterEnqueue = prev }
}
