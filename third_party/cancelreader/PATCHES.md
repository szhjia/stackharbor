# Local lifecycle patch

Based on github.com/muesli/cancelreader v0.2.2, with its MIT license retained.

On BSD/macOS, Bubble Tea can close its input reader while the input goroutine is
still returning from cancellation. Upstream Close frees the kqueue and pipe while
Read still calls Fd on the cancellation pipe, causing a data race and unsafe
descriptor lifetime.

The BSD implementation now cancels and joins an active Read before releasing
resources. New reads observe cancellation; concurrent Close calls release resources
once. The other platform implementations are unchanged. The regression test holds
the reader inside its cancellation dispatch and verifies Close waits for it.

Run the patched module tests from the repository root with:

    go test -race github.com/muesli/cancelreader

Remove the local replacement when an upstream release provides equivalent lifetime
synchronization. Do not patch the shared Go module cache.
