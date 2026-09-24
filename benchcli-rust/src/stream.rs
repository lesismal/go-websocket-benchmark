// The socket under every connection's WebSocketStream: a TcpStream whose reads go through one
// large buffer per worker thread, the way uSockets reads through one receive buffer per loop.
//
// tungstenite reads at most its read_buffer_size at a time, and that size is also what it keeps
// reserved for the connection, so it is 4KiB here (see engine.rs). Handed the socket directly,
// that is a recv per 4KiB: a Rate batch of ten 1KiB echoes takes three reads and the probe that
// finds the socket empty, and at 50000 connections the client spent its CPU on recv - 2.6 frames
// a call against benchcli-uwscpp's 13 - read slower than the server wrote, and left the server
// holding the difference (fib's output queues reached 424MB of heap in BenchRate).
//
// So a read here takes everything the socket has, up to SCRATCH, in one recv; when that comes
// back short of the buffer, tokio takes the socket as drained and clears its readiness, so there
// is no probe either. tungstenite still gets its 4KiB at a time, from memory. What it has not
// taken yet waits in the connection's stash, which comes from a per-thread pool and goes back
// as soon as it is empty. tungstenite reads until it has no whole frame left, so that is almost
// always before the connection's task yields: an idle connection holds no more than it did.
//
// Writes have a slot of their own: the frames the engine hands over (see engine.rs), which drain
// in the background of whatever the connection's task is doing - reading, above all - rather
// than holding the task until the socket takes the last byte, the way uWS keeps the unsent tail
// of a write and goes on reading. Awaiting each write whole left a connection whose socket was
// full unread until it drained, so the server's echoes to it piled up in the server. Anything
// tungstenite writes itself, a pong or a close, goes out after those frames, never inside one.
use std::cell::RefCell;
use std::io;
use std::pin::Pin;
use std::task::{Context, Poll};

use tokio::io::{AsyncRead, AsyncWrite, ReadBuf};
use tokio::net::TcpStream;

// The most one recv takes: uSockets' LIBUS_RECV_BUFFER_LENGTH, which benchcli-uwscpp reads with.
const SCRATCH: usize = 512 * 1024;
// Stashes kept for reuse per thread, and the largest capacity worth keeping.
const POOLED: usize = 64;
const POOLED_CAPACITY: usize = 64 * 1024;

thread_local! {
    static SCRATCH_BUF: RefCell<Box<[u8]>> = RefCell::new(vec![0; SCRATCH].into_boxed_slice());
    static POOL: RefCell<Vec<Vec<u8>>> = const { RefCell::new(Vec::new()) };
}

pub struct Stream {
    inner: TcpStream,
    // Read from the socket but not yet handed to tungstenite, from `pos` on.
    stash: Vec<u8>,
    pos: usize,
    // Frames queued by the engine and not yet written, which go before anything else.
    out: &'static [u8],
}

impl Stream {
    pub fn new(inner: TcpStream) -> Stream {
        Stream {
            inner,
            stash: Vec::new(),
            pos: 0,
            out: &[],
        }
    }

    // Queues whole frames to go out before anything written after them. One lot at a time.
    pub fn queue(&mut self, frames: &'static [u8]) {
        debug_assert!(self.out.is_empty());
        self.out = frames;
    }

    pub fn draining(&self) -> bool {
        !self.out.is_empty()
    }

    // Writes what is queued, as far as the socket takes it; Ready once it is all out.
    pub fn poll_drain(&mut self, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        while !self.out.is_empty() {
            match Pin::new(&mut self.inner).poll_write(cx, self.out) {
                Poll::Ready(Ok(0)) => return Poll::Ready(Err(io::ErrorKind::WriteZero.into())),
                Poll::Ready(Ok(n)) => self.out = &self.out[n..],
                Poll::Ready(Err(e)) => return Poll::Ready(Err(e)),
                Poll::Pending => return Poll::Pending,
            }
        }
        Poll::Ready(Ok(()))
    }

    fn release_stash(&mut self) {
        let mut stash = std::mem::take(&mut self.stash);
        self.pos = 0;
        if stash.capacity() == 0 || stash.capacity() > POOLED_CAPACITY {
            return;
        }
        stash.clear();
        POOL.with(|pool| {
            let mut pool = pool.borrow_mut();
            if pool.len() < POOLED {
                pool.push(stash);
            }
        });
    }
}

impl Drop for Stream {
    fn drop(&mut self) {
        self.release_stash();
    }
}

impl AsyncRead for Stream {
    fn poll_read(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &mut ReadBuf<'_>,
    ) -> Poll<io::Result<()>> {
        let this = self.get_mut();
        if this.pos < this.stash.len() {
            let n = buf.remaining().min(this.stash.len() - this.pos);
            buf.put_slice(&this.stash[this.pos..this.pos + n]);
            this.pos += n;
            if this.pos == this.stash.len() {
                this.release_stash();
            }
            return Poll::Ready(Ok(()));
        }
        SCRATCH_BUF.with(|scratch| {
            let mut scratch = scratch.borrow_mut();
            let mut read = ReadBuf::new(&mut scratch[..]);
            match Pin::new(&mut this.inner).poll_read(cx, &mut read) {
                Poll::Ready(Ok(())) => {}
                other => return other,
            }
            let data = read.filled();
            let n = buf.remaining().min(data.len());
            buf.put_slice(&data[..n]);
            if n < data.len() {
                let mut stash = POOL
                    .with(|pool| pool.borrow_mut().pop())
                    .unwrap_or_default();
                stash.extend_from_slice(&data[n..]);
                this.stash = stash;
                this.pos = 0;
            }
            Poll::Ready(Ok(()))
        })
    }
}

// tungstenite's own writes, each after the queued frames.
impl AsyncWrite for Stream {
    fn poll_write(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &[u8],
    ) -> Poll<io::Result<usize>> {
        let this = self.get_mut();
        std::task::ready!(this.poll_drain(cx))?;
        Pin::new(&mut this.inner).poll_write(cx, buf)
    }

    fn poll_write_vectored(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        bufs: &[io::IoSlice<'_>],
    ) -> Poll<io::Result<usize>> {
        let this = self.get_mut();
        std::task::ready!(this.poll_drain(cx))?;
        Pin::new(&mut this.inner).poll_write_vectored(cx, bufs)
    }

    fn is_write_vectored(&self) -> bool {
        self.inner.is_write_vectored()
    }

    fn poll_flush(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        let this = self.get_mut();
        std::task::ready!(this.poll_drain(cx))?;
        Pin::new(&mut this.inner).poll_flush(cx)
    }

    fn poll_shutdown(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        let this = self.get_mut();
        std::task::ready!(this.poll_drain(cx))?;
        Pin::new(&mut this.inner).poll_shutdown(cx)
    }
}
